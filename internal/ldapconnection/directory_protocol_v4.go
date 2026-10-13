package ldapconnection

import (
	"strings"

	"github.com/yunpiao/adtr/internal/directoryassets"
)

func directorySearchPacketV4(base string, id, size int, cookie []byte) []byte {
	// Keep the original scope, filter and phase limit. Only this separately
	// authorized fixed attribute sequence selects the extended dictionary.
	filter := element(0xa1,
		element(0xa3, octets("objectClass"), octets("user")),
		element(0xa3, octets("objectClass"), octets("group")))
	body := element(0x63, octets(base), []byte{0x0a, 1, 2, 0x0a, 1, 0, 2, 1, 0, 2, 1, 3, 1, 1, 0}, filter,
		element(0x30, octets("objectGUID"), octets("objectClass"), octets("sAMAccountName"), octets("userAccountControl"),
			octets("objectSid"), octets("mail"), octets("description"), octets("whenCreated"), octets("primaryGroupID"), octets("servicePrincipalName"), octets("member")))
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

// The name matcher uses a stack buffer and rejects options, non-ASCII names,
// ranged forms, unknown names and duplicate aliases before any value copies.
func directoryV4Field(name []byte) (index, count, limit int, ok bool) {
	if index, count, limit, ok = directoryV3Field(name); ok {
		return
	}
	if len(name) == 6 && strings.EqualFold(string(name), "member") {
		return 10, directoryassets.MaxMemberValuesV4, directoryassets.MaxMemberBytesV4, true
	}
	return 0, 0, 0, false
}

func directoryEntryV4(body []byte, base string) (directoryassets.Entry, error) {
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
	// First retain only bounded views into the owned wire packet. All eleven
	// descriptors and all values must pass bounds and semantics before copies.
	entry := directoryassets.Entry{DN: string(dn)}
	var seen [11]bool
	attributes := directoryBER(encoded)
	for len(attributes) > 0 {
		encoded, err = attributes.expect(0x30)
		if err != nil {
			return directoryassets.Entry{}, invalid
		}
		attribute := directoryBER(encoded)
		name, err := attribute.expect(4)
		if err != nil {
			return directoryassets.Entry{}, invalid
		}
		if directoryassets.IsMemberRangeV4(string(name)) {
			return directoryassets.Entry{}, failure(CodeUnsupportedProfile, StageSearch)
		}
		if len(entry.Attributes) >= len(seen) {
			return directoryassets.Entry{}, invalid
		}
		index, count, limit, ok := directoryV4Field(name)
		if !ok || seen[index] {
			return directoryassets.Entry{}, invalid
		}
		seen[index] = true
		encoded, err = attribute.expect(0x31)
		if err != nil || len(attribute) != 0 || len(encoded) == 0 && index != 10 {
			return directoryassets.Entry{}, invalid
		}
		values := directoryBER(encoded)
		a := directoryassets.Attribute{Name: string(name), Values: make([][]byte, 0)}
		for len(values) > 0 {
			value, err := values.expect(4)
			if err != nil || len(value) == 0 {
				return directoryassets.Entry{}, invalid
			}
			if len(value) > limit || len(a.Values) >= count {
				if index >= 4 && (len(value) > limit || index == 6 || index == 9 || index == 10) {
					return directoryassets.Entry{}, failure(CodeUnsupportedProfile, StageSearch)
				}
				return directoryassets.Entry{}, invalid
			}
			a.Values = append(a.Values, value)
		}
		entry.Attributes = append(entry.Attributes, a)
	}
	decoded, err := directoryassets.DecodeV4(entry)
	if err != nil {
		return directoryassets.Entry{}, directoryProfileError(directoryProfileV4, err)
	}
	directoryassets.ClearStoredObjectsV4([]directoryassets.StoredObjectV4{decoded})
	// From here the caller owns every returned value. The packet can be cleared
	// immediately without altering the entry or a later accumulated result.
	complete := false
	owned := directoryassets.Entry{DN: entry.DN}
	defer func() {
		if !complete {
			clearDirectoryEntries([]directoryassets.Entry{owned})
		}
	}()
	for _, a := range entry.Attributes {
		owned.Attributes = append(owned.Attributes, directoryassets.Attribute{Name: a.Name, Values: make([][]byte, 0, len(a.Values))})
		out := &owned.Attributes[len(owned.Attributes)-1]
		for _, value := range a.Values {
			out.Values = append(out.Values, append([]byte(nil), value...))
		}
	}
	complete = true
	return owned, nil
}
