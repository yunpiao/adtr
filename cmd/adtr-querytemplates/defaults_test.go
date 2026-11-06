package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/yunpiao/adtr/internal/querytemplates"
)

func runDefaultsScenario(t *testing.T, raw string) result {
	t.Helper()
	var out bytes.Buffer
	if err := run(strings.NewReader(raw), &out); err != nil {
		t.Fatal(err)
	}
	var actual result
	if err := json.Unmarshal(out.Bytes(), &actual); err != nil {
		t.Fatal(err)
	}
	if !actual.Simulation {
		t.Fatal("missing simulation marker")
	}
	return actual
}

func TestDefaultsFullValuesAndIndependentLists(t *testing.T) {
	raw := `{"defaults":[
		{"id":"builtin-z","name":"合成 Z","params":{}},
		{"id":"builtin-a","name":"合成 A","params":{
			"appName":[0,2147483647],"rulesID":["rule-two","rule-one"],
			"time":["2026-11-04T08:00:00.123456789+08:00","2026-11-05T00:00:00Z"],
			"dc":["synthetic-dc"],"dataSource":[{"server":"synthetic.invalid","appType":17}],
			"userList":["合成用户"],"ipList":["192.0.2.1","2001:db8::1"],"types":["synthetic-event"]}}
	],"operations":[
		{"op":"create","id":"custom","tplName":"合成 C","content":{"types":["custom-event"]}},
		{"op":"list"},{"op":"delete","id":"custom"},{"op":"list"},
		{"op":"restore","id":"custom"},{"op":"list"}
	]}`
	actual := runDefaultsScenario(t, raw)
	if len(actual.Lists) != 3 {
		t.Fatalf("lists = %d", len(actual.Lists))
	}
	params := querytemplates.Params{
		AppName: []int{0, 2147483647}, RulesID: []string{"rule-two", "rule-one"},
		Time: []string{"2026-11-04T08:00:00.123456789+08:00", "2026-11-05T00:00:00Z"},
		DC:   []string{"synthetic-dc"}, DataSource: []querytemplates.DataSource{{Server: "synthetic.invalid", AppType: 17}},
		UserList: []string{"合成用户"}, IPList: []string{"192.0.2.1", "2001:db8::1"}, Types: []string{"synthetic-event"},
	}
	for _, list := range actual.Lists {
		if len(list.Default) != 2 || list.Default[0].ID != "builtin-a" || list.Default[1].ID != "builtin-z" {
			t.Fatal("defaults not separated in stable name order")
		}
		if !reflect.DeepEqual(list.Default[0], querytemplates.Template{ID: "builtin-a", Name: "合成 A", Params: params}) {
			t.Fatal("default field values or array order changed")
		}
		empty := list.Default[1].Params
		if empty.AppName == nil || empty.RulesID == nil || empty.Time == nil || empty.DC == nil ||
			empty.DataSource == nil || empty.UserList == nil || empty.IPList == nil || empty.Types == nil {
			t.Fatal("empty params must normalize to arrays")
		}
	}
	if len(actual.Lists[0].Customize) != 1 || len(actual.Lists[1].Customize) != 0 || len(actual.Lists[2].Customize) != 1 ||
		!reflect.DeepEqual(actual.Lists[0].Customize, actual.Lists[2].Customize) {
		t.Fatal("custom delete/restore changed defaults or earlier snapshots")
	}
}

func TestDefaultsSearchAndInvocationIsolation(t *testing.T) {
	raw := `{"defaults":[{"id":"builtin","name":"Case 😀","params":{}}],"operations":[
		{"op":"create","id":"custom","tplName":"Case custom","content":{}},
		{"op":"list","search":"Case"},{"op":"list","search":"case"},{"op":"list","search":"😀"}]}`
	actual := runDefaultsScenario(t, raw)
	if len(actual.Lists) != 3 || len(actual.Lists[0].Default) != 1 || len(actual.Lists[0].Customize) != 1 ||
		len(actual.Lists[1].Default) != 0 || len(actual.Lists[1].Customize) != 0 ||
		len(actual.Lists[2].Default) != 1 || len(actual.Lists[2].Customize) != 0 {
		t.Fatal("search did not apply independently to both categories")
	}
	var omitted, empty bytes.Buffer
	if err := run(strings.NewReader(`{"operations":[{"op":"list"}]}`), &omitted); err != nil {
		t.Fatal(err)
	}
	if err := run(strings.NewReader(`{"defaults":[],"operations":[{"op":"list"}]}`), &empty); err != nil {
		t.Fatal(err)
	}
	if omitted.String() != "{\"simulation\":true,\"lists\":[{\"defaultTplList\":[],\"customizeTplList\":[]}]}\n" ||
		!bytes.Equal(omitted.Bytes(), empty.Bytes()) {
		t.Fatal("empty or omitted defaults changed the old wire output or retained prior state")
	}
}

func TestDefaultsImmutableAndReservedNames(t *testing.T) {
	prefix := `{"defaults":[{"id":"builtin","name":"Immutable","params":{}}],"operations":[{"op":"list"},`
	for _, test := range []struct {
		name, operation string
		err             error
	}{
		{"delete", `{"op":"delete","id":"builtin"}`, querytemplates.ErrBuiltin},
		{"restore", `{"op":"restore","id":"builtin"}`, querytemplates.ErrBuiltin},
		{"id collision", `{"op":"create","id":"builtin","tplName":"Different","content":{}}`, querytemplates.ErrExists},
		{"name collision", `{"op":"create","id":"custom","tplName":"Immutable","content":{}}`, querytemplates.ErrExists},
	} {
		t.Run(test.name, func(t *testing.T) {
			var out bytes.Buffer
			if err := run(strings.NewReader(prefix+test.operation+`]}`), &out); !errors.Is(err, test.err) || out.Len() != 0 {
				t.Fatalf("err = %v, output bytes = %d", err, out.Len())
			}
		})
	}
}

func TestDefaultsStrictInputsAndAtomicFailure(t *testing.T) {
	good := `{"id":"builtin","name":"Default","params":{}}`
	cases := map[string]string{
		"null array": `null`, "object not array": `{}`, "null item": `[null]`,
		"missing id":        `[{"name":"Default","params":{}}]`,
		"missing name":      `[{"id":"builtin","params":{}}]`,
		"missing params":    `[{"id":"builtin","name":"Default"}]`,
		"null params":       `[{"id":"builtin","name":"Default","params":null}]`,
		"array params":      `[{"id":"builtin","name":"Default","params":[]}]`,
		"id null":           `[{"id":null,"name":"Default","params":{}}]`,
		"unknown field":     `[ {"id":"builtin","name":"Default","params":{},"trusted":true} ]`,
		"wrong case":        `[{"ID":"builtin","name":"Default","params":{}}]`,
		"duplicate key":     `[{"id":"builtin","id":"other","name":"Default","params":{}}]`,
		"escaped duplicate": `[{"id":"builtin","\u0069d":"other","name":"Default","params":{}}]`,
		"surrogate":         `[{"id":"builtin","name":"\ud800","params":{}}]`,
		"control name":      `[{"id":"builtin","name":"\u0000","params":{}}]`,
		"invalid ip":        `[{"id":"builtin","name":"Default","params":{"ipList":["secret-not-an-ip"]}}]`,
		"nested unknown":    `[{"id":"builtin","name":"Default","params":{"secret":"must-not-echo"}}]`,
		"nested duplicate":  `[{"id":"builtin","name":"Default","params":{"types":[],"types":[]}}]`,
		"duplicate id":      `[` + good + `,{"id":"builtin","name":"Other","params":{}}]`,
		"duplicate name":    `[` + good + `,{"id":"other","name":"Default","params":{}}]`,
	}
	for name, defaults := range cases {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			err := run(strings.NewReader(`{"defaults":`+defaults+`,"operations":[{"op":"list"}]}`), &out)
			if err == nil || out.Len() != 0 {
				t.Fatal("invalid default emitted results")
			}
			if strings.Contains(err.Error(), "secret-not-an-ip") || strings.Contains(err.Error(), "must-not-echo") {
				t.Fatal("error echoed input")
			}
		})
	}
	for _, raw := range []string{
		`{"defaults":[],"defaults":[],"operations":[{"op":"list"}]}`,
		`{"Defaults":[],"operations":[{"op":"list"}]}`,
		`{"defaults":[` + good + `]}`,
		`{"defaults":[` + good + `],"operations":[]}`,
		`{"defaults":[` + good + `],"operations":[{"op":"list"}]} {}`,
		`{"defaults":[{"id":"builtin","name":"` + string([]byte{0xff}) + `","params":{}}],"operations":[{"op":"list"}]}`,
	} {
		var out bytes.Buffer
		if err := run(strings.NewReader(raw), &out); err == nil || out.Len() != 0 {
			t.Fatal("invalid scenario emitted results")
		}
	}
}

func TestDefaultsShareExistingCapacity(t *testing.T) {
	template := func(i int) string {
		return fmt.Sprintf(`{"id":"default-%04d","name":"Default %04d","params":{}}`, i, i)
	}
	defaults := make([]string, querytemplates.MaxTemplates)
	for i := range defaults {
		defaults[i] = template(i)
	}
	prefix := `{"defaults":[` + strings.Join(defaults, ",") + `],"operations":[`
	actual := runDefaultsScenario(t, prefix+`{"op":"list"}]}`)
	if len(actual.Lists) != 1 || len(actual.Lists[0].Default) != querytemplates.MaxTemplates ||
		actual.Lists[0].Default[0].ID != "default-0000" || actual.Lists[0].Default[999].ID != "default-0999" {
		t.Fatal("capacity boundary or ordered end rows lost")
	}
	var out bytes.Buffer
	if err := run(strings.NewReader(prefix+`{"op":"list"},{"op":"create","id":"custom","tplName":"Custom","content":{}}]}`), &out); !errors.Is(err, querytemplates.ErrCapacity) || out.Len() != 0 {
		t.Fatalf("default and custom capacity split: %v", err)
	}
	tooMany := `{"defaults":[` + strings.Join(append(defaults, template(1000)), ",") + `],"operations":[{"op":"list"}]}`
	if err := run(strings.NewReader(tooMany), &out); err == nil || out.Len() != 0 {
		t.Fatal("array over existing 1000-item budget accepted")
	}
}

func TestDefaultsUnicodeBoundariesAndIO(t *testing.T) {
	id, name := strings.Repeat("😀", 128), strings.Repeat("😀", 50)
	entry := map[string]any{"id": id, "name": name, "params": map[string]any{}}
	input := map[string]any{"defaults": []any{entry}, "operations": []any{map[string]any{"op": "list"}}}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	actual := runDefaultsScenario(t, string(raw))
	if actual.Lists[0].Default[0].ID != id || actual.Lists[0].Default[0].Name != name {
		t.Fatal("Unicode boundaries changed")
	}
	for _, writer := range []interface{ Write([]byte) (int, error) }{failingWriter{}, shortWriter{}} {
		if err := run(bytes.NewReader(raw), writer); err == nil {
			t.Fatal("default scenario writer failure accepted")
		}
	}
	entry["name"] = name + "😀"
	raw, _ = json.Marshal(input)
	var out bytes.Buffer
	if err := run(bytes.NewReader(raw), &out); !errors.Is(err, querytemplates.ErrInvalid) || out.Len() != 0 {
		t.Fatal("default name above existing bound accepted")
	}
}
