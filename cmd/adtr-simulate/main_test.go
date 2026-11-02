package main

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestCommands(t *testing.T) {
	cases := map[string]string{
		"assets": `{"version":"simulated-asset-v1","source":"synthetic","tenantId":"t","domainId":"d","objectId":"u","observationId":"o","kind":"user","observedAt":"2026-01-01T00:00:00Z","asOf":"2026-01-01T00:00:00Z","account":{"disabled":false,"locked":false,"login":"never"}}`,
		"groups": `{"snapshot":{"version":"adtr.simulated-groupgraph.v1","simulated":true,"scope":{"tenantId":"t","domainId":"d"},"observationId":"o","nodes":[{"id":"g","kind":"group","membersKnown":true},{"id":"u","kind":"user","membersKnown":false}],"edges":[{"groupId":"g","memberId":"u"}]},"query":{"scope":{"tenantId":"t","domainId":"d"},"objectId":"g","direction":"members","transitive":true,"maxDepth":8,"maxResults":10}}`,
		"risk":   `{"scope":{"tenant_id":"t","domain_id":"d","user_id":"u"},"as_of":"2026-01-01T01:00:00Z","events":[{"id":"e","scope":{"tenant_id":"t","domain_id":"d","user_id":"u"},"kind":"high","occurred_at":"2026-01-01T00:00:00Z"}]}`,
		"rules":  `{"scope":{"tenant":"t","domain":"d"},"rules":[{"contract":"adtr.detection.simulated.v1","provenance":"synthetic-business-contract-2026-11-01","id":"r","version":1,"scope":{"tenant":"t","domain":"d"},"enabled":true,"combine":"all","conditions":[{"field":"event.code","operator":"eq","values":["4625"]}]}],"events":[{"provenance":"synthetic-business-contract-2026-11-01","id":"e","scope":{"tenant":"t","domain":"d"},"fields":{"event.code":"4625"}}]}`,
		"dns":    `{"records":[{"id":"a","domain":"d","dnsName":"HOST.example","ip":"192.0.2.1","observation":"o","source":"fixture","version":"dnsip-sim-v1","simulation":true}],"filter":{"domain":"d","observation":"o"}}`,
		"events": `{"scope":{"Tenant":"t","Domain":"d","Read":true},"provenance":{"Contract":"adtr.securityevents.simulated.v1","Fixture":"f","Synthetic":true},"events":[],"filter":{"Limit":10},"windowSeconds":60,"threshold":2}`,
	}
	cases["dns-xlsx"] = cases["dns"]
	for command, input := range cases {
		t.Run(command, func(t *testing.T) {
			var out, errout bytes.Buffer
			if code := run([]string{command}, strings.NewReader(input), &out, &errout); code != 0 {
				t.Fatalf("code %d: %s", code, errout.String())
			}
			var v struct {
				Simulation bool
				Contract   string
				Result     json.RawMessage
			}
			if err := json.Unmarshal(out.Bytes(), &v); err != nil || !v.Simulation || v.Contract != "adtr.offline-simulation.v1" {
				t.Fatal(out.String(), err)
			}
			if command == "dns-xlsx" {
				var w struct{ WorkbookBase64 string }
				json.Unmarshal(v.Result, &w)
				data, err := base64.StdEncoding.DecodeString(w.WorkbookBase64)
				if err != nil {
					t.Fatal(err)
				}
				z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
				if err != nil || len(z.File) < 5 {
					t.Fatal("not an XLSX", err)
				}
			}
		})
	}
}
func TestRejectInput(t *testing.T) {
	for _, s := range []string{`null`, `{}`, `{"unknown":"secret"}`, `{} {}`, strings.Repeat(" ", maxInput+1), `{"scope":`} {
		var out, errout bytes.Buffer
		if run([]string{"risk"}, strings.NewReader(s), &out, &errout) != 2 || out.Len() != 0 || strings.Contains(errout.String(), "secret") {
			t.Fatal("unsafe rejection")
		}
	}
	var b bytes.Buffer
	if run(nil, strings.NewReader(""), &b, &b) != 2 {
		t.Fatal("usage")
	}
	if run([]string{"unknown"}, strings.NewReader("{}"), &b, &b) != 2 {
		t.Fatal("command")
	}
}

type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) { return 0, errors.New("private input") }
func TestReadFailure(t *testing.T) {
	var out, e bytes.Buffer
	if run([]string{"risk"}, brokenReader{}, &out, &e) != 2 || out.Len() != 0 || strings.Contains(e.String(), "private") {
		t.Fatal("read failure")
	}
}

func TestDuplicateAndDepth(t *testing.T) {
	for _, v := range []string{`{"x":1,"x":2}`, `{"Scope":{},"scope":{}}`, `{"scope":{},"ſcope":{}}`, strings.Repeat("[", 66) + "0" + strings.Repeat("]", 66)} {
		var dst any
		if decode([]byte(v), &dst) == nil {
			t.Fatal("accepted ambiguous input")
		}
	}
}
