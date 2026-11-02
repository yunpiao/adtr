// Package dnsipassets implements an explicitly simulated, offline DNS/IP catalog.
// It performs no DNS lookup, directory access, or authorization decisions.
package dnsipassets

import (
	"errors"
	"net/netip"
	"sort"
	"strings"
)

const Version = "dnsip-sim-v1"
const MaxRecords = 10000

var ErrInvalid = errors.New("dnsipassets: invalid simulated record or query")

// Record is one synthetic observation, never a discovered or verified DNS mapping.
type Record struct {
	ID          string `json:"id"`
	Domain      string `json:"domain"`
	DNSName     string `json:"dnsName"`
	IP          string `json:"ip"`
	Observation string `json:"observation"`
	Source      string `json:"source"`
	Version     string `json:"version"`
	Simulation  bool   `json:"simulation"`
}

// Filter pins one domain and observation. Keyword is a case-insensitive literal
// substring of DNS name or canonical IP; Family is empty, ipv4, or ipv6.
type Filter struct {
	Domain      string `json:"domain"`
	Observation string `json:"observation"`
	Keyword     string `json:"keyword"`
	Family      string `json:"family"`
}

func token(s string) bool {
	if len(s) == 0 || len(s) > 128 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
			return false
		}
	}
	return true
}
func dns(s string) (string, bool) {
	// Reject original non-ASCII bytes before case folding: Unicode characters
	// such as Kelvin sign otherwise fold into ASCII and alias a domain scope.
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return "", false
		}
	}
	s = strings.ToLower(strings.TrimSuffix(s, "."))
	if len(s) == 0 || len(s) > 253 {
		return "", false
	}
	for _, label := range strings.Split(s, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return "", false
			}
		}
	}
	return s, true
}

// Normalize validates explicit provenance and canonicalizes ASCII DNS names and
// IPs. IPv4-mapped IPv6 becomes IPv4; scoped IPv6 is rejected. No input is mutated.
func Normalize(r Record) (Record, error) {
	if !r.Simulation || r.Version != Version || !token(r.ID) || !token(r.Observation) || !token(r.Source) {
		return Record{}, ErrInvalid
	}
	var ok bool
	r.Domain, ok = dns(r.Domain)
	if !ok {
		return Record{}, ErrInvalid
	}
	r.DNSName, ok = dns(r.DNSName)
	if !ok {
		return Record{}, ErrInvalid
	}
	a, err := netip.ParseAddr(r.IP)
	if err != nil || a.Zone() != "" {
		return Record{}, ErrInvalid
	}
	r.IP = a.Unmap().String()
	return r, nil
}

// Select is the single selection path for list and export. It validates the
// complete bounded input before returning any rows, rejects duplicate IDs within
// a domain/observation, and returns independent records in DNS/IP/ID order.
// The caller must authorize Filter.Domain before calling; this is not an ACL.
func Select(records []Record, f Filter) ([]Record, error) {
	domain, ok := dns(f.Domain)
	if !ok || !token(f.Observation) || len(records) > MaxRecords || len(f.Keyword) > 253 || (f.Family != "" && f.Family != "ipv4" && f.Family != "ipv6") {
		return nil, ErrInvalid
	}
	for _, c := range f.Keyword {
		if c < 32 || c > 126 {
			return nil, ErrInvalid
		}
	}
	keyword := strings.ToLower(f.Keyword)
	out := make([]Record, 0)
	seen := make(map[string]bool, len(records))
	for _, raw := range records {
		r, err := Normalize(raw)
		if err != nil {
			return nil, err
		}
		key := r.Domain + "/" + r.Observation + "/" + r.ID
		if seen[key] {
			return nil, ErrInvalid
		}
		seen[key] = true
		if r.Domain != domain || r.Observation != f.Observation {
			continue
		}
		a, _ := netip.ParseAddr(r.IP)
		if f.Family == "ipv4" && !a.Is4() || f.Family == "ipv6" && !a.Is6() {
			continue
		}
		if !strings.Contains(r.DNSName, keyword) && !strings.Contains(r.IP, keyword) {
			continue
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.DNSName != b.DNSName {
			return a.DNSName < b.DNSName
		}
		ai, _ := netip.ParseAddr(a.IP)
		bi, _ := netip.ParseAddr(b.IP)
		if c := ai.Compare(bi); c != 0 {
			return c < 0
		}
		return a.ID < b.ID
	})
	return out, nil
}

// List returns a zero-based bounded page and the total matching row count.
// Export uses Select without paging, so its rows equal concatenated list pages.
func List(records []Record, f Filter, offset, limit int) ([]Record, int, error) {
	if offset < 0 || limit < 1 || limit > 1000 {
		return nil, 0, ErrInvalid
	}
	rows, err := Select(records, f)
	if err != nil {
		return nil, 0, err
	}
	total := len(rows)
	if offset >= total {
		return []Record{}, total, nil
	}
	end := total
	if limit < total-offset {
		end = offset + limit
	}
	return rows[offset:end], total, nil
}
