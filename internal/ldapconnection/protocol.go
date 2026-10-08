package ldapconnection

import (
	"context"
	"io"
	"net"
	"strings"
	"unicode/utf8"
)

const startTLSOID = "1.3.6.1.4.1.1466.20037"

func writeMessage(ctx context.Context, conn net.Conn, packet []byte, stage Stage) error {
	if err := ctx.Err(); err != nil {
		return classifyIO(ctx, err, stage)
	}
	for len(packet) > 0 {
		n, err := conn.Write(packet)
		if err != nil {
			return classifyIO(ctx, err, stage)
		}
		if n <= 0 || n > len(packet) {
			return classifyIO(ctx, io.ErrShortWrite, stage)
		}
		packet = packet[n:]
	}
	return nil
}

func response(ctx context.Context, conn net.Conn, id int, stage Stage) (byte, []byte, error) {
	tag, body, err := readMessage(conn, id)
	if err != nil {
		return 0, nil, classifyIO(ctx, err, stage)
	}
	if tag == 0x73 {
		clear(body)
		return 0, nil, failure(CodeReferralRejected, stage)
	}
	return tag, body, nil
}

func startTLS(ctx context.Context, conn net.Conn) error {
	if err := writeMessage(ctx, conn, message(1, 0x77, element(0x80, []byte(startTLSOID))), StageStartTLS); err != nil {
		return err
	}
	tag, body, err := response(ctx, conn, 1, StageStartTLS)
	if err != nil {
		return err
	}
	defer clear(body)
	if tag != 0x78 {
		return failure(CodeInvalidResponse, StageStartTLS)
	}
	return resultCode(body, StageStartTLS)
}

func bindPacket(credential Credential) []byte {
	password := element(0x80, credential.Password)
	defer clear(password)
	body := append([]byte{0x02, 0x01, 0x03}, octets(credential.Username)...)
	body = append(body, password...)
	defer clear(body)
	operation := element(0x60, body)
	defer clear(operation)
	return element(0x30, []byte{0x02, 0x01, 0x02}, operation)
}

func bind(ctx context.Context, conn net.Conn, credential Credential) error {
	packet := bindPacket(credential)
	defer clear(packet)
	err := writeMessage(ctx, conn, packet, StageBind)
	clear(packet)
	if err != nil {
		return err
	}
	tag, body, err := response(ctx, conn, 2, StageBind)
	if err != nil {
		return err
	}
	defer clear(body)
	if tag != 0x61 {
		return failure(CodeInvalidResponse, StageBind)
	}
	return resultCode(body, StageBind)
}

func searchPacket() []byte {
	// baseObject, neverDerefAliases, sizeLimit=1, timeLimit=3, typesOnly=false;
	// presence filter (objectClass=*); only the four RootDSE attributes.
	body := element(0x04, nil)
	body = append(body, 0x0a, 0x01, 0x00, 0x0a, 0x01, 0x00,
		0x02, 0x01, 0x01, 0x02, 0x01, 0x03, 0x01, 0x01, 0x00)
	body = append(body, element(0x87, []byte("objectClass"))...)
	body = append(body, element(0x30, octets("dnsHostName"), octets("defaultNamingContext"),
		octets("supportedCapabilities"), octets("supportedLDAPVersion"))...)
	return message(3, 0x63, body)
}

func search(ctx context.Context, conn net.Conn) (Result, error) {
	if err := writeMessage(ctx, conn, searchPacket(), StageSearch); err != nil {
		return Result{}, err
	}
	var result Result
	entrySeen := false
	// Exactly one entry and one success result. There is no unbounded message
	// loop, paged search, continuation, or reference handling.
	for range 2 {
		tag, body, err := response(ctx, conn, 3, StageSearch)
		if err != nil {
			return Result{}, err
		}
		defer clear(body)
		switch tag {
		case 0x64:
			if entrySeen {
				return Result{}, failure(CodeInvalidResponse, StageSearch)
			}
			result, err = rootDSE(body)
			if err != nil {
				return Result{}, err
			}
			entrySeen = true
		case 0x65:
			if err := resultCode(body, StageSearch); err != nil {
				return Result{}, err
			}
			if !entrySeen {
				return Result{}, failure(CodeInvalidResponse, StageSearch)
			}
			return result, nil
		default:
			return Result{}, failure(CodeInvalidResponse, StageSearch)
		}
	}
	return Result{}, failure(CodeInvalidResponse, StageSearch)
}

func resultCode(body []byte, stage Stage) error {
	fields := berReader(body)
	encoded, err := fields.expect(0x0a)
	if err != nil {
		return failure(CodeInvalidResponse, stage)
	}
	code, err := number(encoded)
	if err != nil {
		return failure(CodeInvalidResponse, stage)
	}
	// Consume but never retain or expose matchedDN and diagnosticMessage.
	for range 2 {
		value, err := fields.expect(0x04)
		if err != nil || len(value) > maxValueBytes {
			return failure(CodeInvalidResponse, stage)
		}
	}
	if code == 10 {
		return failure(CodeReferralRejected, stage)
	}
	for len(fields) != 0 {
		tag, value, err := fields.take()
		if err != nil {
			return failure(CodeInvalidResponse, stage)
		}
		if tag == 0xa3 {
			return failure(CodeReferralRejected, stage)
		}
		// A successful StartTLS response may identify the operation. RFC 4511
		// forbids responseValue; simple bind and search have no optional fields.
		if stage != StageStartTLS || tag != 0x8a || string(value) != startTLSOID || len(fields) != 0 {
			return failure(CodeInvalidResponse, stage)
		}
	}
	if code == 0 {
		return nil
	}
	if stage == StageStartTLS {
		return failure(CodeStartTLSRequired, stage)
	}
	switch code {
	case 3:
		return failure(CodeLDAPTimeout, stage)
	case 8, 13:
		return failure(CodeStartTLSRequired, stage)
	case 49:
		return failure(CodeCredentialsRejected, stage)
	case 50:
		return failure(CodeLDAPAccessDenied, stage)
	default:
		return failure(CodeInvalidResponse, stage)
	}
}

func rootDSE(body []byte) (Result, error) {
	invalid := failure(CodeInvalidResponse, StageSearch)
	fields := berReader(body)
	dn, err := fields.expect(0x04)
	if err != nil || len(dn) != 0 {
		return Result{}, invalid
	}
	encoded, err := fields.expect(0x30)
	if err != nil || len(fields) != 0 {
		return Result{}, invalid
	}
	attributes := berReader(encoded)
	seen := make(map[string]bool, maxAttributes)
	result := Result{SupportedCapabilities: []string{}}
	version3 := false
	for len(attributes) != 0 {
		if len(seen) >= maxAttributes {
			return Result{}, invalid
		}
		encoded, err := attributes.expect(0x30)
		if err != nil {
			return Result{}, invalid
		}
		attribute := berReader(encoded)
		name, err := attribute.expect(0x04)
		if err != nil || len(name) > 64 {
			return Result{}, invalid
		}
		key := strings.ToLower(string(name))
		if seen[key] {
			return Result{}, invalid
		}
		seen[key] = true
		encoded, err = attribute.expect(0x31)
		if err != nil || len(attribute) != 0 {
			return Result{}, invalid
		}
		values := berReader(encoded)
		count := 0
		for len(values) != 0 {
			value, err := values.expect(0x04)
			count++
			if err != nil || len(value) > maxValueBytes || count > maxCapabilities || !safeText(value) {
				return Result{}, invalid
			}
			switch key {
			case "dnshostname":
				if count != 1 || !validDNSName(string(value)) {
					return Result{}, invalid
				}
				result.DCHostName = string(value)
			case "defaultnamingcontext":
				if count != 1 || len(value) == 0 {
					return Result{}, invalid
				}
				result.DefaultNamingContext = string(value)
			case "supportedcapabilities":
				if !validOID(string(value)) {
					return Result{}, invalid
				}
				result.SupportedCapabilities = append(result.SupportedCapabilities, string(value))
			case "supportedldapversion":
				if count > 8 || !validVersion(string(value)) {
					return Result{}, invalid
				}
				version3 = version3 || string(value) == "3"
			default:
				return Result{}, invalid
			}
		}
		if count == 0 && key != "supportedcapabilities" {
			return Result{}, invalid
		}
	}
	if result.DCHostName == "" || result.DefaultNamingContext == "" || !version3 {
		return Result{}, invalid
	}
	return result, nil
}

func safeText(value []byte) bool {
	if !utf8.Valid(value) {
		return false
	}
	for _, b := range value {
		if b < 0x20 || b == 0x7f {
			return false
		}
	}
	return true
}
