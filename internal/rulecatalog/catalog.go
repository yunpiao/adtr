// Package rulecatalog models an offline synthetic rule lifecycle. Snapshots and
// reviewer names are caller supplied, not authenticated or durable audit records.
package rulecatalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/yunpiao/adtr/internal/detectionrules"
)

const Contract = "adtr.rulecatalog.simulated.v1"
const MaxEntries = 64
const MaxPublished = 8
const MaxCases = 32

var ErrInvalid = errors.New("invalid simulated rule catalog")
var ErrConflict = errors.New("simulated catalog revision conflict")
var ErrTransition = errors.New("invalid simulated catalog transition")
var ErrNotFound = errors.New("simulated catalog entry not found")

type TestCase struct {
	Event     detectionrules.Event `json:"event"`
	WantMatch bool                 `json:"want_match"`
}
type TestReport struct {
	Digest string     `json:"digest"`
	Cases  []TestCase `json:"cases"`
	Passed bool       `json:"passed"`
}
type Published struct {
	Rule     detectionrules.Rule `json:"rule"`
	Digest   string              `json:"digest"`
	Test     TestReport          `json:"test"`
	Reviewer string              `json:"reviewer"`
}
type Entry struct {
	ID            string               `json:"id"`
	Draft         *detectionrules.Rule `json:"draft"`
	Test          *TestReport          `json:"test"`
	Published     []Published          `json:"published"`
	ActiveVersion uint64               `json:"active_version"`
	Archived      bool                 `json:"archived"`
}
type Snapshot struct {
	Contract   string               `json:"contract"`
	Provenance string               `json:"provenance"`
	Scope      detectionrules.Scope `json:"scope"`
	Revision   uint64               `json:"revision"`
	Entries    []Entry              `json:"entries"`
}
type Request struct {
	Scope            detectionrules.Scope `json:"scope"`
	Snapshot         *Snapshot            `json:"snapshot"`
	Action           string               `json:"action"`
	ExpectedRevision uint64               `json:"expected_revision"`
	ID               string               `json:"id"`
	Rule             *detectionrules.Rule `json:"rule"`
	Cases            []TestCase           `json:"cases"`
	Reviewer         string               `json:"reviewer"`
	Query            string               `json:"query"`
}
type Response struct {
	Snapshot Snapshot    `json:"snapshot"`
	Entries  []Entry     `json:"entries,omitempty"`
	Entry    *Entry      `json:"entry,omitempty"`
	Exported []Published `json:"exported,omitempty"`
}

func text(s string) bool {
	if len(s) == 0 || len(s) > 128 || !utf8.ValidString(s) || strings.TrimSpace(s) != s {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func validScope(s detectionrules.Scope) bool { return text(s.Tenant) && text(s.Domain) }
func digest(r detectionrules.Rule) string {
	b, _ := json.Marshal(r)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func validateRule(s detectionrules.Scope, id string, r detectionrules.Rule) error {
	if r.ID != id || r.Enabled {
		return ErrInvalid
	}
	_, err := detectionrules.Compile(s, []detectionrules.Rule{r})
	return err
}

// testCandidate always activates a private copy; catalog draft enabled bits
// cannot silently control whether positive fixtures are actually exercised.
func testCandidate(ctx context.Context, r detectionrules.Rule, cases []TestCase) (TestReport, error) {
	if len(cases) < 2 || len(cases) > MaxCases {
		return TestReport{}, ErrInvalid
	}
	positive, negative := false, false
	events := make([]detectionrules.Event, len(cases))
	for i, c := range cases {
		events[i] = c.Event
		if c.WantMatch {
			positive = true
		} else {
			negative = true
		}
	}
	if !positive || !negative {
		return TestReport{}, ErrInvalid
	}
	hash := digest(r)
	r.Enabled = true
	p, err := detectionrules.Compile(r.Scope, []detectionrules.Rule{r})
	if err != nil {
		return TestReport{}, err
	}
	matches, err := p.Evaluate(ctx, events)
	if err != nil {
		return TestReport{}, err
	}
	matched := map[string]bool{}
	for _, m := range matches {
		matched[m.EventID] = true
	}
	passed := true
	for _, c := range cases {
		if matched[c.Event.ID] != c.WantMatch {
			passed = false
		}
	}
	return TestReport{Digest: hash, Cases: cases, Passed: passed}, nil
}
func validateReport(ctx context.Context, r detectionrules.Rule, report TestReport) error {
	actual, err := testCandidate(ctx, r, report.Cases)
	if err != nil {
		return err
	}
	if actual.Digest != report.Digest || actual.Passed != report.Passed {
		return ErrInvalid
	}
	return nil
}
func validate(ctx context.Context, s Snapshot, scope detectionrules.Scope) error {
	if !validScope(scope) || s.Scope != scope || s.Contract != Contract || s.Provenance != detectionrules.Provenance || s.Revision == 0 || len(s.Entries) > MaxEntries {
		return ErrInvalid
	}
	ids := map[string]bool{}
	for _, e := range s.Entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !text(e.ID) || ids[e.ID] || len(e.Published) > MaxPublished {
			return ErrInvalid
		}
		ids[e.ID] = true
		last := uint64(0)
		active := e.ActiveVersion == 0
		for _, p := range e.Published {
			if err := validateRule(scope, e.ID, p.Rule); err != nil {
				return err
			}
			if p.Rule.Version <= last || p.Digest != digest(p.Rule) || !text(p.Reviewer) || !p.Test.Passed {
				return ErrInvalid
			}
			if err := validateReport(ctx, p.Rule, p.Test); err != nil {
				return err
			}
			last = p.Rule.Version
			if e.ActiveVersion == last {
				active = true
			}
		}
		if !active || (e.Archived && e.ActiveVersion != 0) {
			return ErrInvalid
		}
		if e.Draft == nil {
			if e.Test != nil || len(e.Published) == 0 {
				return ErrInvalid
			}
		} else {
			if err := validateRule(scope, e.ID, *e.Draft); err != nil {
				return err
			}
			if e.Draft.Version <= last {
				return ErrInvalid
			}
			if e.Test != nil {
				if err := validateReport(ctx, *e.Draft, *e.Test); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// shape rejects irrelevant parameters so a misspelled or confused operation
// cannot silently discard an intended edit, test or reviewer.
func shape(in Request) bool {
	if in.Query != "" && (in.Action != "list" || !text(in.Query)) {
		return false
	}
	if in.Rule != nil && in.Action != "create" && in.Action != "edit" {
		return false
	}
	if len(in.Cases) != 0 && in.Action != "test" {
		return false
	}
	if in.Reviewer != "" && in.Action != "publish-simulation" {
		return false
	}
	switch in.Action {
	case "init", "list":
		return in.ID == "" && in.ExpectedRevision == 0
	case "detail", "export":
		return text(in.ID) && in.ExpectedRevision == 0
	case "create", "edit":
		return text(in.ID) && in.Rule != nil
	case "test":
		return text(in.ID) && len(in.Cases) > 0
	case "publish-simulation":
		return text(in.ID) && text(in.Reviewer)
	case "enable", "disable", "archive":
		return text(in.ID)
	}
	return false
}

// Execute validates supplied history, deep-copies it, then applies at most one
// transition. Revision conflicts and all failures leave caller input untouched.
func Execute(ctx context.Context, in Request) (Response, error) {
	if ctx == nil || !validScope(in.Scope) || !shape(in) {
		return Response{}, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return Response{}, err
	}
	if in.Action == "init" {
		if in.Snapshot != nil {
			return Response{}, ErrInvalid
		}
		return Response{Snapshot: Snapshot{Contract: Contract, Provenance: detectionrules.Provenance, Scope: in.Scope, Revision: 1, Entries: []Entry{}}}, nil
	}
	if in.Snapshot == nil {
		return Response{}, ErrInvalid
	}
	if err := validate(ctx, *in.Snapshot, in.Scope); err != nil {
		return Response{}, err
	}
	if in.Rule != nil {
		if err := validateRule(in.Scope, in.ID, *in.Rule); err != nil {
			return Response{}, err
		}
	}
	if in.Action == "test" {
		var draft *detectionrules.Rule
		for _, e := range in.Snapshot.Entries {
			if e.ID == in.ID {
				draft = e.Draft
				break
			}
		}
		if draft == nil {
			return Response{}, ErrTransition
		}
		if _, err := testCandidate(ctx, *draft, in.Cases); err != nil {
			return Response{}, err
		}
	}
	// Round-trip only after validation bounds every retained collection/string.
	b, err := json.Marshal(in)
	if err != nil {
		return Response{}, ErrInvalid
	}
	var owned Request
	if json.Unmarshal(b, &owned) != nil {
		return Response{}, ErrInvalid
	}
	in = owned
	s := *in.Snapshot
	out := Response{Snapshot: s}
	if in.Action == "list" {
		out.Entries = []Entry{}
		for _, e := range s.Entries {
			if strings.Contains(e.ID, in.Query) {
				out.Entries = append(out.Entries, e)
			}
		}
		return out, nil
	}
	index := -1
	for i, e := range s.Entries {
		if e.ID == in.ID {
			index = i
			break
		}
	}
	if in.Action == "detail" || in.Action == "export" {
		if index < 0 {
			return Response{}, ErrNotFound
		}
		if in.Action == "detail" {
			out.Entry = &s.Entries[index]
		} else {
			out.Exported = append([]Published{}, s.Entries[index].Published...)
		}
		return out, nil
	}
	if in.ExpectedRevision != s.Revision || s.Revision == math.MaxUint64 {
		return Response{}, ErrConflict
	}
	if in.Action == "create" {
		if index >= 0 || len(s.Entries) == MaxEntries || in.Rule.Version != 1 {
			return Response{}, ErrTransition
		}
		if err := validateRule(in.Scope, in.ID, *in.Rule); err != nil {
			return Response{}, err
		}
		s.Entries = append(s.Entries, Entry{ID: in.ID, Draft: in.Rule, Published: []Published{}})
		index = len(s.Entries) - 1
	} else {
		if index < 0 {
			return Response{}, ErrNotFound
		}
		e := &s.Entries[index]
		if e.Archived {
			return Response{}, ErrTransition
		}
		switch in.Action {
		case "edit":
			latest := uint64(0)
			if len(e.Published) > 0 {
				latest = e.Published[len(e.Published)-1].Rule.Version
			}
			if e.Draft != nil {
				latest = e.Draft.Version
			}
			if latest == math.MaxUint64 || in.Rule.Version != latest+1 {
				return Response{}, ErrTransition
			}
			if err := validateRule(in.Scope, in.ID, *in.Rule); err != nil {
				return Response{}, err
			}
			e.Draft = in.Rule
			e.Test = nil
		case "test":
			if e.Draft == nil {
				return Response{}, ErrTransition
			}
			report, err := testCandidate(ctx, *e.Draft, in.Cases)
			if err != nil {
				return Response{}, err
			}
			e.Test = &report
		case "publish-simulation":
			if e.Draft == nil || e.Test == nil || !e.Test.Passed || e.Test.Digest != digest(*e.Draft) || len(e.Published) == MaxPublished {
				return Response{}, ErrTransition
			}
			e.Published = append(e.Published, Published{Rule: *e.Draft, Digest: e.Test.Digest, Test: *e.Test, Reviewer: in.Reviewer})
			e.Draft = nil
			e.Test = nil
		case "enable":
			if len(e.Published) == 0 {
				return Response{}, ErrTransition
			}
			e.ActiveVersion = e.Published[len(e.Published)-1].Rule.Version
		case "disable":
			e.ActiveVersion = 0
		case "archive":
			e.ActiveVersion = 0
			e.Archived = true
		default:
			return Response{}, ErrInvalid
		}
	}
	if err := ctx.Err(); err != nil {
		return Response{}, err
	}
	s.Revision++
	out.Snapshot = s
	out.Entry = &s.Entries[index]
	return out, nil
}
