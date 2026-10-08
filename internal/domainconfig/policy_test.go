package domainconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"sync"
	"testing"
)

func TestPolicyExactAllowlistAndUnsafeAddresses(t *testing.T) {
	policy, err := loadPolicy([]byte(syntheticPolicy), syntheticCertificate(t, true))
	if err != nil {
		t.Fatal(err)
	}
	for _, address := range []string{"10.20.1.2", "10.20.0.1", "fd12:3456::a", "::ffff:10.20.1.2"} {
		prefixes, err := policy.AuthorizeTarget("tenant-a", "EXAMPLE.Test.", "DC.EXAMPLE.TEST.", netip.MustParseAddr(address))
		if err != nil || len(prefixes) != 2 {
			t.Fatalf("explicitly authorized address %s rejected: %v", address, err)
		}
	}
	for _, address := range []string{
		"168.63.129.16", "::ffff:168.63.129.16", "100.100.100.200", "::ffff:100.100.100.200", "fd00:ec2::254", "10.21.0.1", "192.168.1.1", "172.16.0.1", "8.8.8.8", "fd13::1",
		"0.0.0.0", "0.1.2.3", "127.0.0.1", "127.10.0.1", "169.254.169.254", "224.0.0.1", "255.255.255.255",
		"::", "::1", "::ffff:10.21.0.1", "::ffff:127.0.0.1", "::ffff:169.254.169.254", "::ffff:10.20.1.2%eth0", "fe80::1", "fe80::1%eth0", "ff02::1", "fd12:3456::a%eth0",
	} {
		if got, err := policy.AuthorizeTarget("tenant-a", "example.test", "dc.example.test", netip.MustParseAddr(address)); got != nil || !errors.Is(err, ErrTargetDenied) {
			t.Errorf("unauthorized address %s accepted", address)
		}
	}
	for _, tuple := range [][3]string{
		{"tenant-b", "example.test", "dc.example.test"},
		{"Tenant-A", "example.test", "dc.example.test"},
		{"tenant-a", "other.test", "dc.example.test"},
		{"tenant-a", "example.test", "other.example.test"},
		{"tenant-a", "example.test", "dc.example.test.evil.test"},
		{"tenant-a", "example.test", "*.example.test"},
		{"tenant-a", "example.test", "10.20.1.2"},
		{"tenant-a", "example.test", "dc.example.test:636"},
		{"tenant-a", "example.test", " dc.example.test"},
	} {
		if _, err := policy.AuthorizeTarget(tuple[0], tuple[1], tuple[2], netip.Addr{}); !errors.Is(err, ErrTargetDenied) || err.Error() != "domain target denied" {
			t.Fatal("unauthorized tuple accepted or error contained details")
		}
	}
	for _, empty := range []*Policy{nil, {}} {
		if _, err := empty.AuthorizeTarget("tenant-a", "example.test", "dc.example.test", netip.Addr{}); !errors.Is(err, ErrTargetDenied) {
			t.Fatal("empty policy accepted target")
		}
		if empty.Roots() != nil || empty.Revision() != "" {
			t.Fatal("empty policy provided trust or revision")
		}
	}
}

func TestPolicySnapshotsCannotBeMutated(t *testing.T) {
	ca := syntheticCertificate(t, true)
	data := []byte(syntheticPolicy)
	policy, err := loadPolicy(data, ca)
	if err != nil {
		t.Fatal(err)
	}
	revision := policy.Revision()
	clear(data)
	clear(ca)
	prefixes, err := policy.AuthorizeTarget("tenant-a", "example.test", "dc.example.test", netip.Addr{})
	if err != nil {
		t.Fatal(err)
	}
	prefixes[0] = netip.MustParsePrefix("127.0.0.0/8")
	if _, err := policy.AuthorizeTarget("tenant-a", "example.test", "dc.example.test", netip.MustParseAddr("10.20.1.2")); err != nil {
		t.Fatal("caller changed stored prefixes")
	}
	again, err := policy.AuthorizeTarget("tenant-a", "example.test", "dc.example.test", netip.Addr{})
	if err != nil || again[0] == prefixes[0] {
		t.Fatal("returned prefixes share mutable backing storage")
	}
	roots := policy.Roots()
	before := len(roots.Subjects())
	if !roots.AppendCertsFromPEM(syntheticCertificate(t, true)) || len(roots.Subjects()) != before+1 {
		t.Fatal("test failed to mutate returned roots")
	}
	if len(policy.Roots().Subjects()) != before || policy.Revision() != revision {
		t.Fatal("caller changed startup roots or revision")
	}
	encoded, err := json.Marshal(policy)
	if err != nil || string(encoded) != "{}" {
		t.Fatal("policy JSON exposed its private state")
	}
	if formatted := fmt.Sprintf("%#v", policy); strings.Contains(formatted, "tenant-a") || strings.Contains(formatted, "example.test") {
		t.Fatal("policy formatting exposed identities")
	}
}

func TestPolicyRevisionCanonicalizationAndCAChanges(t *testing.T) {
	ca := syntheticCertificate(t, true)
	base := `{"version":1,"targets":[{"tenantId":"tenant-b","domain":"EXAMPLE.test.","serverNames":["dc2.example.test","DC1.EXAMPLE.TEST."],"cidrs":["fd12:3456::/48","10.20.0.0/16"]},{"tenantId":"tenant-a","domain":"other.test","serverNames":["dc.other.test"],"cidrs":["192.168.1.0/24"]}]}`
	ordered := `{
 "targets":[
 {"cidrs":["192.168.1.0/24"],"domain":"other.test","tenantId":"tenant-a","serverNames":["dc.other.test"]},
 {"domain":"example.test","tenantId":"tenant-b","cidrs":["10.20.0.0/16","fd12:3456:0:0::/48"],"serverNames":["dc1.example.test","dc2.example.test"]}],"version":1}`
	a, err := loadPolicy([]byte(base), ca)
	if err != nil {
		t.Fatal(err)
	}
	b, err := loadPolicy([]byte(ordered), ca)
	if err != nil || a.Revision() != b.Revision() || len(a.Revision()) != 64 {
		t.Fatal("semantically equal policy did not have equal revision", err)
	}
	for _, mutation := range []struct{ policy, ca []byte }{
		{[]byte(strings.Replace(base, "10.20.0.0/16", "10.21.0.0/16", 1)), ca},
		{[]byte(strings.Replace(base, "tenant-b", "tenant-c", 1)), ca},
		{[]byte(base), syntheticCertificate(t, true)},
		{[]byte(base), append(append([]byte(nil), ca...), '\n')},
	} {
		changed, err := loadPolicy(mutation.policy, mutation.ca)
		if err != nil || a.Revision() == changed.Revision() {
			t.Fatal("policy/CA mutation did not change revision", err)
		}
	}
}

func TestPolicyRejectsMalformedJSONAndSchema(t *testing.T) {
	ca := syntheticCertificate(t, true)
	tests := map[string]string{
		"empty": "", "invalid": `{`, "root array": `[]`, "null": `null`,
		"unknown version":         `{"version":2,"targets":[]}`,
		"string version":          `{"version":"1","targets":[]}`,
		"fractional version":      `{"version":1.0,"targets":[]}`,
		"null targets":            `{"version":1,"targets":null}`,
		"missing targets":         `{"version":1}`,
		"wrong case":              `{"Version":1,"targets":[]}`,
		"unknown field":           `{"version":1,"targets":[],"allowAll":true}`,
		"duplicate root key":      `{"version":1,"version":1,"targets":[]}`,
		"escaped duplicate key":   `{"version":1,"\u0076ersion":1,"targets":[]}`,
		"trailing value":          syntheticPolicy + ` {}`,
		"trailing text":           syntheticPolicy + ` trailing`,
		"target unknown field":    strings.Replace(syntheticPolicy, `"domain":`, `"allowAll":true,"domain":`, 1),
		"target case alias":       strings.Replace(syntheticPolicy, `"tenantId"`, `"TenantId"`, 1),
		"duplicate target field":  strings.Replace(syntheticPolicy, `"tenantId":"tenant-a"`, `"tenantId":"tenant-a","tenantId":"tenant-b"`, 1),
		"null tenant":             strings.Replace(syntheticPolicy, `"tenant-a"`, `null`, 1),
		"control tenant":          strings.Replace(syntheticPolicy, `"tenant-a"`, `"tenant\na"`, 1),
		"null name":               strings.Replace(syntheticPolicy, `"dc.example.test"`, `null`, 1),
		"null CIDR":               strings.Replace(syntheticPolicy, `"10.20.0.0/16"`, `null`, 1),
		"empty names":             strings.Replace(syntheticPolicy, `["dc.example.test"]`, `[]`, 1),
		"duplicate name":          strings.Replace(syntheticPolicy, `["dc.example.test"]`, `["dc.example.test","DC.EXAMPLE.TEST."]`, 1),
		"duplicate CIDR":          strings.Replace(syntheticPolicy, `"fd12:3456::/48"`, `"10.20.0.0/16"`, 1),
		"duplicate IPv6 spelling": strings.Replace(syntheticPolicy, `"10.20.0.0/16"`, `"fd12:3456:0:0::/48"`, 1),
		"noncanonical CIDR":       strings.Replace(syntheticPolicy, `"10.20.0.0/16"`, `"10.20.1.1/16"`, 1),
		"wildcard domain":         strings.Replace(syntheticPolicy, `"example.test"`, `"*.example.test"`, 1),
		"IP domain":               strings.Replace(syntheticPolicy, `"example.test"`, `"10.20.1.2"`, 1),
		"invalid UTF8":            string(append([]byte(syntheticPolicy), 0xff)),
	}
	for _, name := range []string{"*.example.test", "ldap://dc.example.test", "dc.example.test:389", "dc..test", "dc.example.test..", "-dc.example.test", "dc_.example.test", "dc.例子.test", "K.example.test", "dc.example.test/", "127.0.0.1", strings.Repeat("a", 64) + ".test"} {
		tests["invalid name "+name] = strings.Replace(syntheticPolicy, "dc.example.test", name, 1)
	}
	for _, prefix := range []string{"168.63.129.16/32", "168.63.0.0/16", "100.100.100.200/32", "100.64.0.0/10", "fd00:ec2::254/128", "fd00:ec2::/64", "0.0.0.0/0", "127.0.0.0/8", "169.254.0.0/16", "224.0.0.0/4", "255.255.255.255/32", "::/0", "::/128", "::1/128", "fe80::/10", "ff00::/8", "::ffff:10.20.0.0/112", "bad-cidr"} {
		tests["invalid CIDR "+prefix] = strings.Replace(syntheticPolicy, "10.20.0.0/16", prefix, 1)
	}
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			if policy, err := loadPolicy([]byte(data), ca); policy != nil || !errors.Is(err, ErrConfiguration) || err.Error() != "invalid domain security configuration" {
				t.Fatal("malformed policy accepted or error contained input", err)
			}
		})
	}
}

func TestPolicyLimitsAndDuplicateTargets(t *testing.T) {
	ca := syntheticCertificate(t, true)
	var valid wirePolicy
	if err := json.Unmarshal([]byte(syntheticPolicy), &valid); err != nil {
		t.Fatal(err)
	}
	base := valid.Targets[0]
	check := func(name string, policy wirePolicy, valid bool) {
		t.Helper()
		raw, err := json.Marshal(policy)
		if err != nil {
			t.Fatal(err)
		}
		_, err = loadPolicy(raw, ca)
		if (err == nil) != valid {
			t.Errorf("%s validity mismatch: %v", name, err)
		}
	}
	duplicate := base
	duplicate.Domain = "EXAMPLE.TEST."
	check("duplicate normalized targets", wirePolicy{1, []wireTarget{base, duplicate}}, false)
	var targets []wireTarget
	for i := 0; i < maxTargets; i++ {
		target := base
		target.TenantID = fmt.Sprintf("tenant-%d", i)
		targets = append(targets, target)
	}
	check("maximum targets", wirePolicy{1, targets}, true)
	targets = append(targets, wireTarget{"additional-tenant", base.Domain, base.ServerNames, base.CIDRs})
	check("excessive targets", wirePolicy{1, targets}, false)
	target := base
	target.ServerNames = nil
	target.CIDRs = nil
	for i := 0; i < 32; i++ {
		target.ServerNames = append(target.ServerNames, fmt.Sprintf("dc%d.example.test", i))
		target.CIDRs = append(target.CIDRs, fmt.Sprintf("10.20.%d.0/24", i))
	}
	check("maximum names and CIDRs", wirePolicy{1, []wireTarget{target}}, true)
	tooMany := target
	tooMany.ServerNames = append(append([]string(nil), target.ServerNames...), "extra.example.test")
	check("excessive names", wirePolicy{1, []wireTarget{tooMany}}, false)
	tooMany = target
	tooMany.CIDRs = append(append([]string(nil), target.CIDRs...), "10.21.0.0/24")
	check("excessive CIDRs", wirePolicy{1, []wireTarget{tooMany}}, false)
	tooMany = base
	tooMany.CIDRs = nil
	check("absent CIDRs", wirePolicy{1, []wireTarget{tooMany}}, false)
	if _, err := loadPolicy(bytes.Repeat([]byte{' '}, maxConfigBytes+1), ca); !errors.Is(err, ErrConfiguration) {
		t.Fatal("oversize policy accepted")
	}
}

func TestPolicyStrictPEMCABundle(t *testing.T) {
	ca := syntheticCertificate(t, true)
	for name, data := range map[string][]byte{
		"empty":                 nil,
		"spaces":                []byte(" \n "),
		"garbage":               []byte("synthetic-private-ca-payload"),
		"leading garbage":       append([]byte("garbage\n"), ca...),
		"trailing garbage":      append(append([]byte(nil), ca...), []byte("garbage\n")...),
		"malformed PEM":         []byte("-----BEGIN CERTIFICATE-----\nnot-base64!\n-----END CERTIFICATE-----\n"),
		"malformed then valid":  append([]byte("-----BEGIN CERTIFICATE-----\ninvalid\n"), ca...),
		"leaf certificate":      syntheticCertificate(t, false),
		"duplicate certificate": append(append([]byte(nil), ca...), ca...),
		"wrong block type":      bytes.ReplaceAll(ca, []byte("CERTIFICATE"), []byte("PRIVATE KEY")),
		"oversize":              bytes.Repeat([]byte{' '}, maxConfigBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			if policy, err := loadPolicy([]byte(syntheticPolicy), data); policy != nil || !errors.Is(err, ErrConfiguration) {
				t.Fatal("invalid CA bundle accepted")
			}
		})
	}
	bundle := append(append(append([]byte("\n "), ca...), syntheticCertificate(t, true)...), '\n')
	policy, err := loadPolicy([]byte(syntheticPolicy), bundle)
	if err != nil || len(policy.Roots().Subjects()) != 2 {
		t.Fatal("valid multi-root CA bundle rejected", err)
	}
}

func TestConcurrentPolicyReads(t *testing.T) {
	policy, err := loadPolicy([]byte(syntheticPolicy), syntheticCertificate(t, true))
	if err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	for i := 0; i < 24; i++ {
		wait.Go(func() {
			for j := 0; j < 30; j++ {
				prefixes, err := policy.AuthorizeTarget("tenant-a", "example.test", "dc.example.test", netip.MustParseAddr("10.20.1.2"))
				if err != nil || len(prefixes) != 2 || policy.Roots() == nil || policy.Revision() == "" {
					t.Error("concurrent policy access failed", err)
					return
				}
				prefixes[0] = netip.Prefix{}
			}
		})
	}
	wait.Wait()
}

func TestPolicyRejectsSingleLabelNames(t *testing.T) {
	for _, raw := range []string{strings.Replace(syntheticPolicy, "example.test", "internal", 1), strings.Replace(syntheticPolicy, "dc.example.test", "localhost", 1)} {
		if _, err := loadPolicy([]byte(raw), syntheticCertificate(t, true)); err == nil {
			t.Fatal("single-label policy admitted")
		}
	}
}
