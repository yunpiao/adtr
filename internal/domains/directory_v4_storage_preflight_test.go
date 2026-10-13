package domains

import (
	"github.com/yunpiao/adtr/internal/directoryassets"
	"strings"
	"testing"
)

func TestV4StoragePreflightRejectsBeforeAllocation(t *testing.T) {
	prefix := directoryObservationV4Prefix
	source := directoryObservationV4Source + `{}}`
	bounded := []byte(prefix + `{"value":"` + strings.Repeat("x", directoryassets.MaxStoredObjectV4Bytes) + `"}` + source)
	if preflightDirectoryObservationV4(bounded, 1) {
		t.Fatal("oversized object accepted")
	}
	if n := testing.AllocsPerRun(100, func() { preflightDirectoryObservationV4(bounded, 1) }); n != 0 {
		t.Fatalf("preflight allocated %v times", n)
	}
	for _, raw := range []string{
		prefix + `{}` + source,
		prefix + `{"a":"escaped \\\" } ]","b":[{},{}]}` + source,
	} {
		if !preflightDirectoryObservationV4([]byte(raw), 1) {
			t.Fatal("valid span rejected")
		}
	}
	for _, raw := range []string{
		prefix + `{},{}` + source,
		prefix + `{}` + source + ` `,
		prefix + `{"a":"unterminated}` + source,
		prefix + strings.Repeat(`{"a":`, 65) + `0` + strings.Repeat(`}`, 65) + source,
		prefix + `{}` + directoryObservationV4Source + `{"x":"` + strings.Repeat("x", 8192) + `"}}`,
	} {
		if preflightDirectoryObservationV4([]byte(raw), 1) {
			t.Fatal("invalid bound accepted")
		}
	}
}
