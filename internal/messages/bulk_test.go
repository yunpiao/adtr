package messages

import (
	"strings"
	"testing"
)

func TestBulkCanonicalBindingAndInputBounds(t *testing.T) {
	original := BulkRequest{ResultID: "r", OperationID: "op", Selection: "selected", MessageIDs: []string{"b", "a"}}
	hash, err := bulkHash(original)
	if err != nil {
		t.Fatal(err)
	}
	other := original
	other.MessageIDs = []string{"a", "b"}
	same, err := bulkHash(other)
	if err != nil || hash != same || original.MessageIDs[0] != "b" {
		t.Fatal("noncanonical or mutated input")
	}
	for _, in := range []BulkRequest{{ResultID: "r", OperationID: "op", Selection: "all", MessageIDs: []string{}}, {ResultID: "r", OperationID: "op", Selection: "selected"}, {ResultID: "r", OperationID: "op", Selection: "selected", MessageIDs: []string{"a", "a"}}, {ResultID: "r", OperationID: "op", Selection: "selected", MessageIDs: []string{""}}, {ResultID: "r", OperationID: "op", Selection: "selected", MessageIDs: []string{strings.Repeat("a", 129)}}, {ResultID: "r", OperationID: "op", Selection: "unknown"}, {ResultID: "r", OperationID: "op", Selection: "selected", MessageIDs: make([]string, 1001)}} {
		if _, err := bulkHash(in); err == nil {
			t.Fatal("accepted invalid bulk", in)
		}
	}
	for _, in := range []BulkRequest{{ResultID: "r2", OperationID: "op", Selection: "selected", MessageIDs: []string{"a", "b"}}, {ResultID: "r", OperationID: "op", Selection: "all"}, {ResultID: "r", OperationID: "op", Selection: "selected", MessageIDs: []string{"a"}}} {
		different, err := bulkHash(in)
		if err != nil || different == hash {
			t.Fatal("request binding lost", in)
		}
	}
}
