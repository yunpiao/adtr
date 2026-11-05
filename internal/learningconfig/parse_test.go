package learningconfig

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

const validSnapshot = `{"format":"synthetic-learning-config-v1","synthetic":true,"ruleLabel":"rule-a","dataSourceLabel":"source-a","domainLabel":"domain-a","windowSeconds":3600,"allowedFields":["user.name","event.id"],"selectedFields":["user.name"]}`
const canonicalSnapshot = `{"format":"synthetic-learning-config-v1","synthetic":true,"ruleLabel":"rule-a","dataSourceLabel":"source-a","domainLabel":"domain-a","windowSeconds":3600,"allowedFields":["event.id","user.name"],"selectedFields":["user.name"]}`

func snapshotObject(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(validSnapshot), &object); err != nil {
		t.Fatal(err)
	}
	return object
}

func encodeObject(t *testing.T, object map[string]json.RawMessage) []byte {
	t.Helper()
	data, err := json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func assertInvalidParse(t *testing.T, data []byte) {
	t.Helper()
	value, err := Parse(data)
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("error = %v, want ErrInvalid", err)
	}
	if _, err := value.CanonicalJSON(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("failure returned valid configuration: %v", err)
	}
	if _, err := value.Digest(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("failure returned valid digest: %v", err)
	}
}

func TestParseCanonicalRoundTripAndIsolation(t *testing.T) {
	for _, data := range [][]byte{
		[]byte(validSnapshot),
		[]byte("\n\t " + validSnapshot + " \r\n"),
		encodeObject(t, snapshotObject(t)), // Different object-key order.
		[]byte(strings.ReplaceAll(strings.Replace(validSnapshot, `"ruleLabel"`, `"rule\u004cabel"`, 1), "rule-a", `rule\u002da`)),
	} {
		value, err := Parse(data)
		if err != nil {
			t.Fatal(err)
		}
		canonical, _ := value.CanonicalJSON()
		if string(canonical) != canonicalSnapshot {
			t.Fatalf("canonical = %s", canonical)
		}
		digest, _ := value.Digest()
		sum := sha256.Sum256([]byte(canonicalSnapshot))
		if digest != hex.EncodeToString(sum[:]) {
			t.Fatalf("digest = %s", digest)
		}
		roundTrip, err := Parse(canonical)
		if err != nil {
			t.Fatal(err)
		}
		again, _ := roundTrip.Digest()
		if again != digest {
			t.Fatal("round trip changed digest")
		}
		for i := range data {
			data[i] = 'x'
		}
		canonical[0] = 'x'
		retained, _ := value.CanonicalJSON()
		if string(retained) != canonicalSnapshot {
			t.Fatal("input or returned byte mutation changed configuration")
		}
	}
}

func TestParseRequiredExactKeysAndTypes(t *testing.T) {
	for key := range snapshotObject(t) {
		t.Run(key, func(t *testing.T) {
			missing := snapshotObject(t)
			delete(missing, key)
			assertInvalidParse(t, encodeObject(t, missing))
			for _, bad := range []string{"null", `{}`, `[]`, `false`, `true`, `0`, `""`} {
				// Some replacements have the right JSON type but invalid values.
				// synthetic:true is the single valid replacement in this list.
				if key == "synthetic" && bad == "true" {
					continue
				}
				object := snapshotObject(t)
				object[key] = json.RawMessage(bad)
				assertInvalidParse(t, encodeObject(t, object))
			}
			alias := snapshotObject(t)
			alias[strings.ToUpper(key)] = alias[key]
			delete(alias, key)
			assertInvalidParse(t, encodeObject(t, alias))
		})
	}
	for _, extra := range []string{
		`"unexpected":"private-input-value"`,
		`"machineTime":1`,
		`"ruleLabel":"rule-a"`,
		`"ruleLabel":"other"`,
		`"rule\u004cabel":"rule-a"`,
		`"Synthetic":true`,
	} {
		assertInvalidParse(t, []byte(strings.TrimSuffix(validSnapshot, "}")+","+extra+"}"))
	}
}

func TestParseMalformedAndBoundedJSON(t *testing.T) {
	for _, input := range []string{
		"", " ", "null", "true", `[]`, `"text"`, `{}`,
		validSnapshot + `{}`, validSnapshot + `null`, validSnapshot + "garbage",
		strings.TrimSuffix(validSnapshot, "}"), strings.Replace(validSnapshot, `,"synthetic"`, `,,"synthetic"`, 1),
		strings.TrimSuffix(validSnapshot, "}") + ",}",
		strings.Replace(validSnapshot, `"rule-a"`, `"rule-\q"`, 1),
		strings.Replace(validSnapshot, `"rule-a"`, `"rule-\u12"`, 1),
		strings.Replace(validSnapshot, `"rule-a"`, "\"rule-\n\"", 1),
		strings.Replace(validSnapshot, `["user.name","event.id"]`, `["user.name",]`, 1),
		strings.Replace(validSnapshot, `["user.name","event.id"]`, `[`+strings.Repeat("[", 1000)+`0`+strings.Repeat("]", 1000)+`]`, 1),
	} {
		assertInvalidParse(t, []byte(input))
	}
	badUTF8 := []byte(strings.Replace(validSnapshot, "rule-a", "rule-\xff", 1))
	assertInvalidParse(t, badUTF8)
	exact := append([]byte(validSnapshot), bytes.Repeat([]byte(" "), MaxJSONBytes-len(validSnapshot))...)
	if _, err := Parse(exact); err != nil {
		t.Fatalf("exact byte bound rejected: %v", err)
	}
	assertInvalidParse(t, append(exact, ' '))
}

func TestParseUnicodeCannotBeRepaired(t *testing.T) {
	for _, value := range []string{`"\ud800"`, `"\udfff"`, `"\ud800x"`, `"\ud800\ud800"`, `"\udc00\ud800"`, `"\ud800\u0061"`, `"\ud83d\ude00"`, `"\ufffd"`, `"用户"`, `"\\ud800"`} {
		for _, key := range []string{"format", "ruleLabel", "dataSourceLabel", "domainLabel", "allowedFields", "selectedFields"} {
			object := snapshotObject(t)
			if strings.HasSuffix(key, "Fields") {
				object[key] = json.RawMessage("[" + value + "]")
			} else {
				object[key] = json.RawMessage(value)
			}
			assertInvalidParse(t, encodeObject(t, object))
		}
		assertInvalidParse(t, []byte(strings.Replace(validSnapshot, `"ruleLabel"`, value, 1)))
	}
}

func TestParsePreservesCoreValidation(t *testing.T) {
	for _, n := range []string{"0", "-1", "2592001", "9223372036854775808", "-9223372036854775809", "1.0", "1e0", `"1"`, "+1", "01", "NaN", "1e99999"} {
		assertInvalidParse(t, []byte(strings.Replace(validSnapshot, "3600", n, 1)))
	}
	for _, n := range []string{"1", "2592000"} {
		if _, err := Parse([]byte(strings.Replace(validSnapshot, "3600", n, 1))); err != nil {
			t.Fatalf("valid integer %s: %v", n, err)
		}
	}
	for _, bad := range []struct{ key, value string }{
		{"format", `"synthetic-learning-config-v2"`}, {"ruleLabel", `" rule-a"`},
		{"dataSourceLabel", `"source/a"`}, {"domainLabel", `"domain\n"`},
		{"ruleLabel", `"` + strings.Repeat("a", 129) + `"`},
		{"allowedFields", `["user.name","user.name"]`}, {"selectedFields", `["user.name","user.name"]`},
		{"selectedFields", `["unknown"]`}, {"selectedFields", `["User.name"]`},
		{"allowedFields", `["User.name"]`}, {"allowedFields", `["user/name"]`},
		{"allowedFields", `[null]`}, {"selectedFields", `[null]`},
		{"allowedFields", `[1]`}, {"selectedFields", `[{}]`},
		{"allowedFields", `["` + strings.Repeat("a", 65) + `"]`},
	} {
		object := snapshotObject(t)
		object[bad.key] = json.RawMessage(bad.value)
		assertInvalidParse(t, encodeObject(t, object))
	}
	allowed := make([]string, MaxAllowedFields)
	for i := range allowed {
		allowed[i] = fmt.Sprintf("field_%03d", i)
	}
	object := snapshotObject(t)
	object["allowedFields"], _ = json.Marshal(allowed)
	object["selectedFields"], _ = json.Marshal(allowed[:MaxSelectedFields])
	if _, err := Parse(encodeObject(t, object)); err != nil {
		t.Fatalf("maximum field counts: %v", err)
	}
	object["allowedFields"], _ = json.Marshal(append(allowed, "overflow"))
	assertInvalidParse(t, encodeObject(t, object))
	object["allowedFields"], _ = json.Marshal(allowed)
	object["selectedFields"], _ = json.Marshal(allowed[:MaxSelectedFields+1])
	assertInvalidParse(t, encodeObject(t, object))
}

func TestParseErrorsDoNotEchoInput(t *testing.T) {
	marker := "PRIVATE_SENTINEL_DO_NOT_ECHO"
	for _, input := range []string{
		`{"` + marker + `":true}`,
		strings.Replace(validSnapshot, `"rule-a"`, `" `+marker+`"`, 1),
		validSnapshot + marker,
	} {
		_, err := Parse([]byte(input))
		if err == nil || strings.Contains(err.Error(), marker) {
			t.Fatalf("unsafe error: %v", err)
		}
	}
}

func FuzzParse(f *testing.F) {
	for _, seed := range []string{validSnapshot, canonicalSnapshot, "", "null", "{}", `{"\ud800":true}`} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		value, err := Parse(data)
		if err != nil {
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("unexpected error: %v", err)
			}
			return
		}
		canonical, err := value.CanonicalJSON()
		if err != nil {
			t.Fatal(err)
		}
		roundTrip, err := Parse(canonical)
		if err != nil {
			t.Fatal(err)
		}
		again, _ := roundTrip.CanonicalJSON()
		if !bytes.Equal(canonical, again) {
			t.Fatal("canonical round trip mismatch")
		}
	})
}
