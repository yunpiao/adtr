package ldapconnection

import (
	"context"
	"io"
	"net"
	"strings"

	"github.com/yunpiao/adtr/internal/directoryassets"
)

const (
	pagedResultsOID = "1.2.840.113556.1.4.319"
	// A maximum-size cookie plus its BER envelope exceeds the RootDSE limit.
	// This limit is local to directory messages; Probe remains capped at 64KiB.
	maxDirectoryMessageBytes = 128 * 1024
)

func directoryInteger(n int) []byte {
	var raw [5]byte
	i := len(raw) - 1
	raw[i] = byte(n)
	for n >>= 8; n > 0; n >>= 8 {
		i--
		raw[i] = byte(n)
	}
	if raw[i]&0x80 != 0 {
		i--
		raw[i] = 0
	}
	return element(0x02, raw[i:])
}

func directorySearchPacket(base string, id, size int, cookie []byte) []byte {
	// subtree, neverDerefAliases, sizeLimit=0 (RFC2696 paging), timeLimit=3,
	// typesOnly=false. Client row/page/byte limits remain mandatory and finite.
	filter := element(0xa1,
		element(0xa3, octets("objectClass"), octets("user")),
		element(0xa3, octets("objectClass"), octets("group")))
	body := element(0x63, octets(base), []byte{0x0a, 1, 2, 0x0a, 1, 0, 2, 1, 0, 2, 1, 3, 1, 1, 0}, filter,
		element(0x30, octets("objectGUID"), octets("objectClass"), octets("sAMAccountName"), octets("userAccountControl")))
	cookieField := element(0x04, cookie)
	defer clear(cookieField)
	value := element(0x30, directoryInteger(size), cookieField)
	defer clear(value)
	valueField := element(0x04, value)
	defer clear(valueField)
	control := element(0x30, octets(pagedResultsOID), []byte{1, 1, 0xff}, valueField)
	defer clear(control)
	controls := element(0xa0, control)
	defer clear(controls)
	return element(0x30, directoryInteger(id), body, controls)
}

func directoryPage(parent context.Context, conn net.Conn, cfg DirectoryConfig, base string, id int, cookie []byte) (entries []directoryassets.Entry, next []byte, err error) {
	return directoryPageProfile(parent, conn, cfg, base, id, cookie, directoryProfileV1)
}

func directoryPageProfile(parent context.Context, conn net.Conn, cfg DirectoryConfig, base string, id int, cookie []byte, profile directoryProfile) (entries []directoryassets.Entry, next []byte, err error) {
	if !profile.valid() {
		return nil, nil, failure(CodeInvalidConfig, StageValidate)
	}
	ctx, cancel := context.WithTimeout(parent, stepTimeout)
	defer cancel()
	complete := false
	defer func() {
		if !complete {
			clearDirectoryEntries(entries)
			clear(next)
			entries = nil
			next = nil
		}
	}()
	if err = directoryAuthorization(ctx, cfg, DirectoryPage); err != nil {
		return
	}
	if err = setStepDeadline(ctx, conn); err != nil {
		err = classifyIO(ctx, err, StageSearch)
		return
	}
	packet := profile.searchPacket(base, id, cfg.Limits.MaxPageEntries, cookie)
	defer clear(packet)
	err = writeMessage(ctx, conn, packet, StageSearch)
	clear(packet)
	if err != nil {
		return
	}
	// Bound wire allocations before allocation as well as decoded output in the
	// accumulator. The fixed allowance covers bounded BER framing and controls.
	remaining := cfg.Limits.MaxBytes + maxDirectoryMessageBytes
	for messages := 0; messages <= cfg.Limits.MaxPageEntries; messages++ {
		var done bool
		done, err = func() (bool, error) {
			m, e := readDirectoryMessageProfile(conn, id, remaining, profile)
			if e != nil {
				if e == directoryassets.ErrLimit && ctx.Err() == nil {
					return false, directoryProfileError(profile, e)
				}
				return false, classifyIO(ctx, e, StageSearch)
			}
			defer clear(m.packet)
			remaining -= len(m.packet)
			if e := ctx.Err(); e != nil {
				return false, classifyIO(ctx, e, StageSearch)
			}
			switch m.tag {
			case 0x73:
				return false, failure(CodeReferralRejected, StageSearch)
			case 0x64:
				if len(m.controls) != 0 {
					return false, failure(CodeInvalidResponse, StageSearch)
				}
				if len(entries) >= cfg.Limits.MaxPageEntries {
					return false, directoryProfileError(profile, directoryassets.ErrLimit)
				}
				entry, e := profile.entry(m.body, base)
				if e != nil {
					return false, e
				}
				entries = append(entries, entry)
				return false, nil
			case 0x65:
				if e := resultCode(m.body, StageSearch); e != nil {
					return false, e
				}
				next, e = directoryCookieProfile(m.controls, cfg.Limits.MaxCookieBytes, profile)
				return true, e
			default:
				return false, failure(CodeInvalidResponse, StageSearch)
			}
		}()
		if err != nil || done {
			complete = err == nil && done
			return
		}
	}
	err = failure(CodeInvalidResponse, StageSearch)
	return
}

func directoryEntry(body []byte, base string) (directoryassets.Entry, error) {
	invalid := failure(CodeInvalidResponse, StageSearch)
	fields := directoryBER(body)
	dn, err := fields.expect(4)
	if err != nil || len(dn) > 4096 || !directoryDNWithin(string(dn), base) {
		return directoryassets.Entry{}, invalid
	}
	encoded, err := fields.expect(0x30)
	if err != nil || len(fields) != 0 {
		return directoryassets.Entry{}, invalid
	}
	entry := directoryassets.Entry{DN: string(dn)}
	complete := false
	defer func() {
		if !complete {
			clearDirectoryEntries([]directoryassets.Entry{entry})
		}
	}()
	attributes := directoryBER(encoded)
	for len(attributes) > 0 {
		if len(entry.Attributes) >= 4 {
			return directoryassets.Entry{}, invalid
		}
		encoded, err = attributes.expect(0x30)
		if err != nil {
			return directoryassets.Entry{}, invalid
		}
		attribute := directoryBER(encoded)
		name, err := attribute.expect(4)
		if err != nil || len(name) > 64 {
			return directoryassets.Entry{}, invalid
		}
		count, valueLimit := 1, 0
		switch strings.ToLower(string(name)) {
		case "objectguid":
			valueLimit = 16
		case "objectclass":
			count, valueLimit = 32, 64
		case "samaccountname":
			valueLimit = 1024
		case "useraccountcontrol":
			valueLimit = 10
		default:
			return directoryassets.Entry{}, invalid
		}
		encoded, err = attribute.expect(0x31)
		if err != nil || len(attribute) != 0 {
			return directoryassets.Entry{}, invalid
		}
		values := directoryBER(encoded)
		entry.Attributes = append(entry.Attributes, directoryassets.Attribute{Name: string(name)})
		a := &entry.Attributes[len(entry.Attributes)-1]
		for len(values) > 0 {
			value, err := values.expect(4)
			if err != nil || len(value) > valueLimit || len(a.Values) >= count {
				return directoryassets.Entry{}, invalid
			}
			a.Values = append(a.Values, append([]byte(nil), value...))
		}
	}
	complete = true
	return entry, nil
}

func directoryCookie(encoded []byte, limit int) ([]byte, error) {
	return directoryCookieProfile(encoded, limit, directoryProfileV1)
}

func directoryCookieProfile(encoded []byte, limit int, profile directoryProfile) ([]byte, error) {
	if !profile.valid() {
		return nil, failure(CodeInvalidConfig, StageValidate)
	}
	invalid := failure(CodeInvalidResponse, StageSearch)
	controls := directoryBER(encoded)
	control, err := controls.expect(0x30)
	if err != nil || len(controls) != 0 {
		return nil, invalid
	}
	fields := directoryBER(control)
	oid, err := fields.expect(4)
	if err != nil || string(oid) != pagedResultsOID {
		return nil, invalid
	}
	if len(fields) > 0 && fields[0] == 1 {
		critical, err := fields.expect(1)
		if err != nil || len(critical) != 1 || (critical[0] != 0 && critical[0] != 0xff) {
			return nil, invalid
		}
	}
	value, err := fields.expect(4)
	if err != nil || len(fields) != 0 {
		return nil, invalid
	}
	fields = directoryBER(value)
	sequence, err := fields.expect(0x30)
	if err != nil || len(fields) != 0 {
		return nil, invalid
	}
	fields = directoryBER(sequence)
	size, err := fields.expect(2)
	if err != nil {
		return nil, invalid
	}
	if _, err := number(size); err != nil {
		return nil, invalid
	}
	cookie, err := fields.expect(4)
	if err != nil || len(fields) != 0 {
		return nil, invalid
	}
	if len(cookie) > limit {
		return nil, directoryProfileError(profile, directoryassets.ErrLimit)
	}
	return append([]byte(nil), cookie...), nil
}

// The directory decoder has a separate envelope bound. It never widens the
// RootDSE's BER reader or permits general recursion or indefinite BER lengths.
type directoryBER []byte

func directoryLength(data []byte) (header, length int, err error) {
	if len(data) == 0 {
		return 0, 0, errBER
	}
	if data[0] < 128 {
		return 1, int(data[0]), nil
	}
	count := int(data[0] & 0x7f)
	if count == 0 || count > 3 || len(data) < 1+count || data[1] == 0 {
		return 0, 0, errBER
	}
	for _, b := range data[1 : 1+count] {
		length = length<<8 | int(b)
	}
	if length < 128 || length > maxDirectoryMessageBytes {
		return 0, 0, errBER
	}
	return 1 + count, length, nil
}

func (r *directoryBER) take() (byte, []byte, error) {
	if len(*r) < 2 || (*r)[0]&0x1f == 0x1f {
		return 0, nil, errBER
	}
	header, length, err := directoryLength((*r)[1:])
	if err != nil || length > len(*r)-1-header {
		return 0, nil, errBER
	}
	tag := (*r)[0]
	value := (*r)[1+header : 1+header+length]
	*r = (*r)[1+header+length:]
	return tag, value, nil
}

func (r *directoryBER) expect(tag byte) ([]byte, error) {
	got, value, err := r.take()
	if err != nil || got != tag {
		return nil, errBER
	}
	return value, nil
}

type directoryMessage struct {
	tag                    byte
	body, controls, packet []byte
}

func readDirectoryMessage(reader io.Reader, expectedID, remaining int) (out directoryMessage, err error) {
	return readDirectoryMessageProfile(reader, expectedID, remaining, directoryProfileV1)
}

func readDirectoryMessageProfile(reader io.Reader, expectedID, remaining int, profile directoryProfile) (out directoryMessage, err error) {
	if !profile.valid() {
		return out, failure(CodeInvalidConfig, StageValidate)
	}
	var header [5]byte
	if _, err = io.ReadFull(reader, header[:2]); err != nil {
		return
	}
	if header[0] != 0x30 {
		return out, errBER
	}
	count := 0
	if header[1] >= 128 {
		count = int(header[1] & 0x7f)
		if count == 0 || count > 3 {
			return out, errBER
		}
		if _, err = io.ReadFull(reader, header[2:2+count]); err != nil {
			return
		}
	}
	_, length, err := directoryLength(header[1 : 2+count])
	if err != nil {
		return out, errBER
	}
	if length > remaining {
		if profile == directoryProfileV2 {
			return out, directoryassets.ErrLimit
		}
		return out, errBER
	}
	packet := make([]byte, length)
	transferred := false
	defer func() {
		if !transferred {
			clear(packet)
		}
	}()
	if _, err = io.ReadFull(reader, packet); err != nil {
		return
	}
	fields := directoryBER(packet)
	idBytes, err := fields.expect(2)
	if err != nil {
		return out, errBER
	}
	id, err := number(idBytes)
	if err != nil || id != expectedID {
		return out, errBER
	}
	out.tag, out.body, err = fields.take()
	if err != nil {
		return directoryMessage{}, errBER
	}
	if len(fields) > 0 {
		out.controls, err = fields.expect(0xa0)
		if err != nil || len(fields) != 0 || len(out.controls) == 0 {
			return directoryMessage{}, errBER
		}
	}
	out.packet = packet
	transferred = true
	return out, nil
}
