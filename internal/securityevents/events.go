// Package securityevents implements an offline, synthetic-only event core.
// It is not an AD collector, production authorization boundary or threat verdict.
package securityevents

import (
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	ContractVersion = "adtr.securityevents.simulated.v1"
	MaxEvents       = 4096
	MaxFieldBytes   = 128
	MaxWindow       = 24 * time.Hour
)

var (
	ErrInvalid  = errors.New("invalid simulated event input")
	ErrDenied   = errors.New("simulated event scope denied")
	ErrConflict = errors.New("simulated event revision conflict")
)

// Scope must be derived by a trusted caller, never directly from request JSON.
// These explicit grants deliberately have no administrator bypass.
type Scope struct {
	Tenant, Domain string
	Read, Manage   bool
}
type Provenance struct {
	Contract, Fixture string
	Synthetic         bool
}
type RawEvent struct {
	ID, Tenant, Domain, Kind, Actor, Device, Session, SourceIP, OccurredAt string
	Provenance                                                             Provenance
}
type Event struct {
	ID, Tenant, Domain, Kind, Actor, Device, Session, SourceIP string
	OccurredAt                                                 time.Time
	Provenance                                                 Provenance
}

// Dataset owns validated values; no caller-owned slice is retained.
type Dataset struct {
	events         []Event
	tenant, domain string
	provenance     Provenance
}

func field(s string) bool {
	if s == "" || len(s) > MaxFieldBytes || !utf8.ValidString(s) || strings.TrimSpace(s) != s {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func validScope(s Scope, manage bool) bool {
	return field(s.Tenant) && field(s.Domain) && s.Read && (!manage || s.Manage)
}
func validKind(k string) bool {
	return k == "login_failure" || k == "login_success" || k == "service_access"
}
func validProvenance(p Provenance) bool {
	return p.Contract == ContractVersion && p.Synthetic && field(p.Fixture)
}
func (d *Dataset) authorize(s Scope, manage bool) error {
	if d == nil || !validScope(s, manage) || s.Tenant != d.tenant || s.Domain != d.domain {
		return ErrDenied
	}
	return nil
}

// Normalize atomically validates a single scope/fixture batch, canonicalizes IPs
// and UTC timestamps, rejects duplicate IDs, and orders by (time, ID).
func Normalize(s Scope, p Provenance, raw []RawEvent) (*Dataset, error) {
	if !validScope(s, false) {
		return nil, ErrDenied
	}
	if !validProvenance(p) || len(raw) > MaxEvents {
		return nil, ErrInvalid
	}
	d := &Dataset{tenant: s.Tenant, domain: s.Domain, provenance: p, events: make([]Event, 0, len(raw))}
	seen := map[string]bool{}
	for _, r := range raw {
		if r.Tenant != s.Tenant || r.Domain != s.Domain {
			return nil, ErrDenied
		}
		if r.Provenance != p || !validKind(r.Kind) || !field(r.ID) || !field(r.Actor) || !field(r.Device) || !field(r.Session) || len(r.OccurredAt) > 64 || len(r.SourceIP) > 64 || seen[r.ID] {
			return nil, ErrInvalid
		}
		at, err := time.Parse(time.RFC3339Nano, r.OccurredAt)
		if err != nil || at.Year() < 2000 || at.Year() > 2100 {
			return nil, ErrInvalid
		}
		ip, err := netip.ParseAddr(r.SourceIP)
		if err != nil || ip.Zone() != "" {
			return nil, ErrInvalid
		}
		seen[r.ID] = true
		d.events = append(d.events, Event{r.ID, r.Tenant, r.Domain, r.Kind, r.Actor, r.Device, r.Session, ip.Unmap().String(), at.UTC(), p})
	}
	sort.Slice(d.events, func(i, j int) bool {
		a, b := d.events[i], d.events[j]
		if a.OccurredAt.Equal(b.OccurredAt) {
			return a.ID < b.ID
		}
		return a.OccurredAt.Before(b.OccurredAt)
	})
	return d, nil
}

// Filter is an AND of exact identifiers, optional inclusive From and exclusive
// Until. Limit is mandatory (1..4096); Offset is a bounded snapshot index.
type Filter struct {
	Kind, Actor, Device, Session, SourceIP string
	From, Until                            time.Time
	Offset, Limit                          int
}
type Page struct {
	Events     []Event
	Total      int
	Provenance Provenance
}

func (d *Dataset) Query(s Scope, f Filter) (Page, error) {
	if err := d.authorize(s, false); err != nil {
		return Page{}, err
	}
	if f.Limit < 1 || f.Limit > MaxEvents || f.Offset < 0 || f.Offset > MaxEvents || (!f.From.IsZero() && !f.Until.IsZero() && !f.From.Before(f.Until)) {
		return Page{}, ErrInvalid
	}
	for _, v := range []string{f.Actor, f.Device, f.Session} {
		if v != "" && !field(v) {
			return Page{}, ErrInvalid
		}
	}
	if f.Kind != "" && !validKind(f.Kind) {
		return Page{}, ErrInvalid
	}
	if len(f.SourceIP) > 64 {
		return Page{}, ErrInvalid
	}
	if f.SourceIP != "" {
		ip, err := netip.ParseAddr(f.SourceIP)
		if err != nil || ip.Zone() != "" {
			return Page{}, ErrInvalid
		}
		f.SourceIP = ip.Unmap().String()
	}
	p := Page{Events: []Event{}, Provenance: d.provenance}
	for _, e := range d.events {
		if (f.Kind != "" && e.Kind != f.Kind) || (f.Actor != "" && e.Actor != f.Actor) || (f.Device != "" && e.Device != f.Device) || (f.Session != "" && e.Session != f.Session) || (f.SourceIP != "" && e.SourceIP != f.SourceIP) || (!f.From.IsZero() && e.OccurredAt.Before(f.From)) || (!f.Until.IsZero() && !e.OccurredAt.Before(f.Until)) {
			continue
		}
		if p.Total >= f.Offset && len(p.Events) < f.Limit {
			p.Events = append(p.Events, e)
		}
		p.Total++
	}
	return p, nil
}

// Detection is a synthetic signal, not a validated attack classification.
type Detection struct {
	Rule                   string
	Actor, Device, Session string
	Start, End             time.Time
	EvidenceIDs            []string
	Provenance             Provenance
}

// CorrelateFailures groups exact actor/device/session identities. Windows are
// non-overlapping, anchored on the earliest remaining failure, and [start,end).
// Successes do not reset windows. Input sorting makes output permutation-stable.
func (d *Dataset) CorrelateFailures(s Scope, window time.Duration, threshold int) ([]Detection, error) {
	if err := d.authorize(s, false); err != nil {
		return nil, err
	}
	if window < time.Second || window > MaxWindow || threshold < 2 || threshold > MaxEvents {
		return nil, ErrInvalid
	}
	type key struct{ actor, device, session string }
	groups := map[key][]Event{}
	keys := []key{}
	for _, e := range d.events {
		if e.Kind != "login_failure" {
			continue
		}
		k := key{e.Actor, e.Device, e.Session}
		if _, ok := groups[k]; !ok {
			keys = append(keys, k)
		}
		groups[k] = append(groups[k], e)
	}
	out := []Detection{}
	for _, k := range keys {
		es := groups[k]
		for i := 0; i < len(es); {
			end := es[i].OccurredAt.Add(window)
			j := i + 1
			for j < len(es) && es[j].OccurredAt.Before(end) {
				j++
			}
			if j-i >= threshold {
				ids := make([]string, j-i)
				for n := i; n < j; n++ {
					ids[n-i] = es[n].ID
				}
				out = append(out, Detection{"synthetic.login_failure_burst.v1", k.actor, k.device, k.session, es[i].OccurredAt, end, ids, d.provenance})
			}
			i = j
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Start.Equal(out[j].Start) {
			return out[i].EvidenceIDs[0] < out[j].EvidenceIDs[0]
		}
		return out[i].Start.Before(out[j].Start)
	})
	return out, nil
}

// Case is a value state machine. A future persistence adapter must atomically
// compare revisions and store returned state plus audit; this package does no IO.
type Case struct {
	id, tenant, domain, state, mark string
	revision                        uint64
	updatedAt                       time.Time
	provenance                      Provenance
}
type CaseView struct {
	ID, State, Mark string
	Revision        uint64
	Provenance      Provenance
}
type Change struct {
	Action, Actor, Reason string
	At                    time.Time
	ExpectedRevision      uint64
}
type Audit struct {
	CaseID, Action, Actor, Reason, PreviousState, State, PreviousMark, Mark string
	At                                                                      time.Time
	Revision                                                                uint64
	Provenance                                                              Provenance
}

func NewCase(s Scope, p Provenance, id string) (Case, error) {
	if !validScope(s, true) {
		return Case{}, ErrDenied
	}
	if !validProvenance(p) || !field(id) {
		return Case{}, ErrInvalid
	}
	return Case{id: id, tenant: s.Tenant, domain: s.Domain, state: "open", revision: 1, provenance: p}, nil
}
func (c Case) View(s Scope) (CaseView, error) {
	if !validScope(s, false) || c.tenant != s.Tenant || c.domain != s.Domain || c.revision == 0 {
		return CaseView{}, ErrDenied
	}
	return CaseView{c.id, c.state, c.mark, c.revision, c.provenance}, nil
}

// Apply allows mark_suspicious/mark_benign/unmark only while open; close needs
// a reason and is terminal. Stale retries conflict, including repeated close.
func (c Case) Apply(s Scope, ch Change) (Case, Audit, error) {
	fail := func(err error) (Case, Audit, error) { return c, Audit{}, err }
	if _, err := c.View(s); err != nil || !s.Manage {
		return fail(ErrDenied)
	}
	if ch.ExpectedRevision != c.revision {
		return fail(ErrConflict)
	}
	if !field(ch.Actor) || !field(ch.Reason) || ch.At.IsZero() || ch.At.Before(c.updatedAt) || ch.At.Year() < 2000 || ch.At.Year() > 2100 || c.state != "open" || c.revision == ^uint64(0) {
		return fail(ErrInvalid)
	}
	next := c
	switch ch.Action {
	case "mark_suspicious":
		next.mark = "suspicious"
	case "mark_benign":
		next.mark = "benign"
	case "unmark":
		next.mark = ""
	case "close":
		next.state = "closed"
	default:
		return fail(fmt.Errorf("%w: action", ErrInvalid))
	}
	if next.state == c.state && next.mark == c.mark {
		return fail(ErrConflict)
	}
	next.revision++
	next.updatedAt = ch.At.UTC()
	return next, Audit{c.id, ch.Action, ch.Actor, ch.Reason, c.state, next.state, c.mark, next.mark, ch.At.UTC(), next.revision, c.provenance}, nil
}

// CaseSnapshot is a persistence boundary, not an HTTP mutation input. RestoreCase
// validates every field before constructing the private state machine.
type CaseSnapshot struct {
	ID, Tenant, Domain, State, Mark string
	Revision                        uint64
	UpdatedAt                       time.Time
	Provenance                      Provenance
}

func RestoreCase(s Scope, p Provenance, v CaseSnapshot) (Case, error) {
	if !validScope(s, false) || v.Tenant != s.Tenant || v.Domain != s.Domain {
		return Case{}, ErrDenied
	}
	if !validProvenance(p) || v.Provenance != p || !field(v.ID) || v.Revision == 0 || (v.State != "open" && v.State != "closed") || (v.Mark != "" && v.Mark != "benign" && v.Mark != "suspicious") {
		return Case{}, ErrInvalid
	}
	if v.Revision == 1 {
		if v.State != "open" || v.Mark != "" || !v.UpdatedAt.IsZero() {
			return Case{}, ErrInvalid
		}
	} else if v.UpdatedAt.IsZero() || v.UpdatedAt.Year() < 2000 || v.UpdatedAt.Year() > 2100 {
		return Case{}, ErrInvalid
	}
	return Case{id: v.ID, tenant: v.Tenant, domain: v.Domain, state: v.State, mark: v.Mark, revision: v.Revision, updatedAt: v.UpdatedAt.UTC(), provenance: p}, nil
}
func (c Case) Snapshot(s Scope) (CaseSnapshot, error) {
	if _, err := c.View(s); err != nil {
		return CaseSnapshot{}, err
	}
	return CaseSnapshot{c.id, c.tenant, c.domain, c.state, c.mark, c.revision, c.updatedAt, c.provenance}, nil
}
