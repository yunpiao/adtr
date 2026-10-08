package ldapconnection

import (
	"strings"
	"testing"
)

func TestDirectoryBaseDN(t *testing.T) {
	for _, tc := range []struct {
		domain string
		want   string
	}{
		{"synthetic.test", "dc=synthetic,dc=test"},
		{"SYNTHETIC.Test.", "dc=synthetic,dc=test"},
		{"sub-domain.synthetic.test", "dc=sub-domain,dc=synthetic,dc=test"},
		{"xn--bcher-kva.test", "dc=xn--bcher-kva,dc=test"},
		{"", ""}, {"single", ""}, {".test", ""}, {"synthetic..test", ""},
		{"synthetic.test..", ""}, {" synthetic.test", ""}, {"synthetic.test ", ""},
		{"bad_domain.test", ""}, {"-bad.test", ""}, {"bad-.test", ""},
		{"bücher.test", ""}, {"K.test", ""}, {"127.0.0.1", ""}, {"[::1]", ""},
		{"synthetic,test", ""}, {"dc=synthetic.test", ""}, {"synthetic\\.test", ""},
		{strings.Repeat("a", 64) + ".test", ""},
		{strings.Repeat("a.", 127) + "a", ""},
	} {
		t.Run(tc.domain, func(t *testing.T) {
			got, ok := directoryBaseDN(tc.domain)
			if got != tc.want || ok != (tc.want != "") {
				t.Fatalf("directoryBaseDN(%q) = %q, %v; want %q, %v", tc.domain, got, ok, tc.want, tc.want != "")
			}
		})
	}
}

func TestDirectoryDNDomainScope(t *testing.T) {
	const base = "dc=synthetic,dc=test"
	for _, tc := range []struct {
		name   string
		dn     string
		within bool
		equal  bool
	}{
		{"base", base, true, true},
		{"case", "DC=SYNTHETIC,dC=Test", true, true},
		{"escaped domain", `DC=\73ynthetic,DC=te\73t`, true, true},
		{"DC OID", "0.9.2342.19200300.100.1.25=synthetic,DC=test", true, true},
		{"DC descriptor", "domainComponent=synthetic,DC=test", true, true},
		{"ordinary", "CN=Synthetic,OU=Users,DC=synthetic,DC=test", true, false},
		{"unicode", "CN=用户,OU=Bücher,DC=synthetic,DC=test", true, false},
		{"hex unicode", `CN=Lu\C4\8Di\C4\87,DC=synthetic,DC=test`, true, false},
		{"escaped comma", `CN=hello\,DC=evil,DC=synthetic,DC=test`, true, false},
		{"hex comma", `CN=hello\2cDC=evil,DC=synthetic,DC=test`, true, false},
		{"escaped plus", `CN=a\+b,DC=synthetic,DC=test`, true, false},
		{"multivalue prefix", `CN=Smith\, John+UID=jsmith,OU=People,DC=synthetic,DC=test`, true, false},
		{"double slash delimiter", `CN=x\\,DC=synthetic,DC=test`, true, false},
		{"hex slash delimiter", `CN=x\5c,DC=synthetic,DC=test`, true, false},
		{"escaped spaces", `CN=\ hello\ ,DC=synthetic,DC=test`, true, false},
		{"escaped specials", `CN=\#\"\;\<\>\=,DC=synthetic,DC=test`, true, false},
		{"internal equals", `CN=x=DC=evil,DC=synthetic,DC=test`, true, false},
		{"escaped newline", `CN=x\0ADEL:y,DC=synthetic,DC=test`, true, false},
		{"empty string value", `CN=,DC=synthetic,DC=test`, true, false},
		{"empty", "", false, false},
		{"other domain", "CN=x,DC=other,DC=test", false, false},
		{"lookalike suffix", "CN=x,DC=notsynthetic,DC=test", false, false},
		{"longer suffix", "CN=x,DC=synthetic,DC=test,DC=evil", false, false},
		{"child context", "DC=child,DC=synthetic,DC=test", false, false},
		{"child object", "CN=x,DC=child,DC=synthetic,DC=test", false, false},
		{"child alias", "CN=x,domainComponent=child,DC=synthetic,DC=test", false, false},
		{"child OID", "CN=x,0.9.2342.19200300.100.1.25=child,DC=synthetic,DC=test", false, false},
		{"hidden child", "CN=x+DC=child,DC=synthetic,DC=test", false, false},
		{"escaped fake suffix", `CN=x\,DC=synthetic,DC=test`, false, false},
		{"hex fake suffix", `CN=x\2cDC=synthetic,DC=test`, false, false},
		{"triple slash fake suffix", `CN=x\\\,DC=synthetic,DC=test`, false, false},
		{"escaped plus fake suffix", `CN=x\+DC=synthetic,DC=test`, false, false},
		{"multivalue domain", `CN=x,DC=synthetic+OU=evil,DC=test`, false, false},
		{"multivalue terminal", `CN=x,DC=synthetic,DC=test+CN=evil`, false, false},
		{"repeated domain AVA", `CN=x,DC=synthetic+DC=synthetic,DC=test`, false, false},
		{"domain escaped delimiter", `CN=x,DC=synthetic\,DC=evil,DC=test`, false, false},
		{"domain embedded dot", `CN=x,DC=sub.synthetic,DC=test`, false, false},
		{"domain leading space", `CN=x,DC=\ synthetic,DC=test`, false, false},
		{"domain trailing space", `CN=x,DC=synthetic\20,DC=test`, false, false},
		{"missing equals", "broken,DC=synthetic,DC=test", false, false},
		{"empty attribute", "=x,DC=synthetic,DC=test", false, false},
		{"escaped attribute", `C\4e=x,DC=synthetic,DC=test`, false, false},
		{"attribute whitespace", "CN =x,DC=synthetic,DC=test", false, false},
		{"attribute option", "CN;lang-en=x,DC=synthetic,DC=test", false, false},
		{"malformed OID", "01.2=x,DC=synthetic,DC=test", false, false},
		{"trailing comma", base + ",", false, false},
		{"trailing plus", base + "+", false, false},
		{"empty RDN", "CN=x,," + base, false, false},
		{"empty AVA", "CN=x+," + base, false, false},
		{"missing hex digit", `CN=x\2,DC=synthetic,DC=test`, false, false},
		{"invalid hex digit", `CN=x\2G,DC=synthetic,DC=test`, false, false},
		{"invalid escape", `CN=x\q,DC=synthetic,DC=test`, false, false},
		{"terminal escape", base + `\`, false, false},
		{"nonrecursive escape", `CN=x\5c,DC=other,DC=test`, false, false},
		{"raw quote", `CN="x",DC=synthetic,DC=test`, false, false},
		{"raw semicolon", `CN=x;y,DC=synthetic,DC=test`, false, false},
		{"raw angle", `CN=x<y,DC=synthetic,DC=test`, false, false},
		{"raw control", "CN=x\x00y," + base, false, false},
		{"raw DEL", "CN=x\x7fy," + base, false, false},
		{"raw leading space", "CN= x," + base, false, false},
		{"raw trailing space", "CN=x ," + base, false, false},
		{"separator whitespace", "CN=x, " + base, false, false},
		{"unsupported BER value", "CN=#04024869," + base, false, false},
		{"invalid UTF8", "CN=\xff," + base, false, false},
		{"invalid escaped UTF8", `CN=\C4,` + base, false, false},
		{"oversized", "CN=" + strings.Repeat("x", maxValueBytes) + "," + base, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := directoryDNWithin(tc.dn, base); got != tc.within {
				t.Errorf("directoryDNWithin(%q, %q) = %v; want %v", tc.dn, base, got, tc.within)
			}
			if got := directoryDNEqual(tc.dn, base); got != tc.equal {
				t.Errorf("directoryDNEqual(%q, %q) = %v; want %v", tc.dn, base, got, tc.equal)
			}
		})
	}
}

func TestDirectoryDNRejectsInvalidBase(t *testing.T) {
	for _, base := range []string{
		"", "DC=test", "CN=synthetic,DC=test", "DC=synthetic+OU=x,DC=test",
		"DC=synthetic,DC=", "DC=synthetic,DC=test,", "DC=-synthetic,DC=test",
		"DC=synthetic.test,DC=test", "DC=127,DC=0,DC=0,DC=1",
		"DC=K,DC=test", `DC=\C4,DC=test`, "DC=" + strings.Repeat("a", 64) + ",DC=test",
	} {
		t.Run(base, func(t *testing.T) {
			if directoryDNWithin(base, base) || directoryDNEqual(base, base) || directoryDNWithin("CN=x,"+base, base) {
				t.Fatalf("accepted invalid base %q", base)
			}
		})
	}
	if directoryDNWithin("CN=x,DC=K,DC=test", "DC=k,DC=test") {
		t.Fatal("Unicode case-folding lookalike escaped ASCII domain boundary")
	}
}
