// Package sensitivelabels derives explicit labels from synthetic observations.
// It neither infers AD privilege nor performs notifications or authorization.
package sensitivelabels

import (
	"errors"
	"reflect"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/yunpiao/adtr/internal/groupgraph"
)

const ContractVersion = "adtr.simulated-sensitive-labels.v1"
const MaxLabels = 16384
const SensitiveLabel = "sensitive"

var (
	ErrInvalid      = errors.New("sensitivelabels: invalid simulated contract")
	ErrScope        = errors.New("sensitivelabels: scope mismatch")
	ErrLimit        = errors.New("sensitivelabels: budget exceeded")
	ErrIncomplete   = errors.New("sensitivelabels: incomplete observation")
	ErrIncompatible = errors.New("sensitivelabels: incompatible observations")
)

type ManualLabel struct {
	ObjectID string `json:"objectId"`
	Label    string `json:"label"`
}
type Input struct {
	Version   string              `json:"version"`
	Simulated bool                `json:"simulated"`
	Scope     groupgraph.Scope    `json:"scope"`
	Snapshot  groupgraph.Snapshot `json:"snapshot"`
	// Complete explicitly attests object coverage in this synthetic observation.
	Complete          bool          `json:"complete"`
	PolicyID          string        `json:"policyId"`
	SensitiveGroupIDs []string      `json:"sensitiveGroupIds"`
	ManualLabels      []ManualLabel `json:"manualLabels"`
	MaxDepth          int           `json:"maxDepth"`
	MaxLabels         int           `json:"maxLabels"`
}
type Provenance struct {
	Label    string `json:"label"`
	Source   string `json:"source"`
	GroupID  string `json:"groupId,omitempty"`
	Distance int    `json:"distance"`
}
type ObjectLabels struct {
	ObjectID string          `json:"objectId"`
	Kind     groupgraph.Kind `json:"kind"`
	// State is labelled, unlabelled_within_snapshot, or unknown; never safe.
	State  string       `json:"state"`
	Labels []Provenance `json:"labels"`
}
type Result struct {
	Version                 string            `json:"version"`
	Simulated               bool              `json:"simulated"`
	Scope                   groupgraph.Scope  `json:"scope"`
	ObservationID           string            `json:"observationId"`
	PolicyID                string            `json:"policyId"`
	CompleteWithinSnapshot  bool              `json:"completeWithinSnapshot"`
	UnknownMembershipGroups []string          `json:"unknownMembershipGroups"`
	UnknownReferences       []groupgraph.Edge `json:"unknownReferences"`
	Objects                 []ObjectLabels    `json:"objects"`
}

func valid(v string) bool {
	if len(v) == 0 || len(v) > 256 || !utf8.ValidString(v) || strings.TrimSpace(v) != v {
		return false
	}
	for _, r := range v {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func Evaluate(in Input) (Result, error) {
	empty := Result{}
	if in.Version != ContractVersion || !in.Simulated || !valid(in.PolicyID) || in.MaxDepth < 1 || in.MaxDepth > groupgraph.MaxDepth || in.MaxLabels < 1 || in.MaxLabels > MaxLabels {
		return empty, ErrInvalid
	}
	if in.Scope != in.Snapshot.Scope {
		return empty, ErrScope
	}
	if len(in.SensitiveGroupIDs) > groupgraph.MaxNodes || len(in.ManualLabels) > MaxLabels {
		return empty, ErrLimit
	}
	g, err := groupgraph.Build(in.Snapshot)
	if err != nil {
		return empty, err
	}
	r := Result{Version: ContractVersion, Simulated: true, Scope: in.Scope, ObservationID: in.Snapshot.ObservationID, PolicyID: in.PolicyID, CompleteWithinSnapshot: in.Complete, UnknownMembershipGroups: []string{}, UnknownReferences: g.Diagnostics().UnknownReferences, Objects: []ObjectLabels{}}
	objects := map[string]*ObjectLabels{}
	for _, n := range in.Snapshot.Nodes {
		objects[n.ID] = &ObjectLabels{ObjectID: n.ID, Kind: n.Kind, Labels: []Provenance{}}
		if n.Kind == groupgraph.Group && !n.MembersKnown {
			r.UnknownMembershipGroups = append(r.UnknownMembershipGroups, n.ID)
		}
	}
	if len(r.UnknownMembershipGroups) > 0 || len(r.UnknownReferences) > 0 {
		r.CompleteWithinSnapshot = false
	}
	sort.Strings(r.UnknownMembershipGroups)
	count := 0
	add := func(id string, p Provenance) error {
		count++
		if count > in.MaxLabels {
			return ErrLimit
		}
		objects[id].Labels = append(objects[id].Labels, p)
		return nil
	}
	seen := map[string]bool{}
	ids := append([]string{}, in.SensitiveGroupIDs...)
	sort.Strings(ids)
	for _, id := range ids {
		o, ok := objects[id]
		if !ok || o.Kind != groupgraph.Group || seen[id] {
			return empty, ErrInvalid
		}
		seen[id] = true
		if err = add(id, Provenance{SensitiveLabel, "sensitive_group", id, 0}); err != nil {
			return empty, err
		}
		q, e := g.Query(groupgraph.Query{Scope: in.Scope, ObjectID: id, Direction: groupgraph.Members, Transitive: true, MaxDepth: in.MaxDepth, MaxResults: groupgraph.MaxNodes})
		if e != nil {
			return empty, e
		}
		for _, rel := range q.Relationships {
			source := "transitive_membership"
			if rel.Direct {
				source = "direct_membership"
			}
			if err = add(rel.Object.ID, Provenance{SensitiveLabel, source, id, rel.Distance}); err != nil {
				return empty, err
			}
		}
	}
	manualSeen := map[ManualLabel]bool{}
	for _, m := range in.ManualLabels {
		if objects[m.ObjectID] == nil || !valid(m.Label) || manualSeen[m] {
			return empty, ErrInvalid
		}
		manualSeen[m] = true
		if err = add(m.ObjectID, Provenance{Label: m.Label, Source: "manual"}); err != nil {
			return empty, err
		}
	}
	for _, o := range objects {
		sort.Slice(o.Labels, func(i, j int) bool { return less(o.Labels[i], o.Labels[j]) })
		o.State = "unknown"
		if len(o.Labels) > 0 {
			o.State = "labelled"
		} else if r.CompleteWithinSnapshot {
			o.State = "unlabelled_within_snapshot"
		}
		r.Objects = append(r.Objects, *o)
	}
	sort.Slice(r.Objects, func(i, j int) bool { return r.Objects[i].ObjectID < r.Objects[j].ObjectID })
	return r, nil
}
func less(a, b Provenance) bool {
	if a.Label != b.Label {
		return a.Label < b.Label
	}
	if a.Source != b.Source {
		return a.Source < b.Source
	}
	if a.GroupID != b.GroupID {
		return a.GroupID < b.GroupID
	}
	return a.Distance < b.Distance
}

type Change struct {
	ObjectID   string     `json:"objectId"`
	Action     string     `json:"action"`
	Provenance Provenance `json:"provenance"`
}
type ChangeSet struct {
	Version             string           `json:"version"`
	Simulated           bool             `json:"simulated"`
	Scope               groupgraph.Scope `json:"scope"`
	PolicyID            string           `json:"policyId"`
	BeforeObservationID string           `json:"beforeObservationId"`
	AfterObservationID  string           `json:"afterObservationId"`
	Changes             []Change         `json:"changes"`
}

// Compare reports provenance additions/removals only over complete, compatible
// synthetic observations. Caller order defines before/after, not wall-clock time.
func Compare(before, after Input) (ChangeSet, error) {
	empty := ChangeSet{}
	a, err := Evaluate(before)
	if err != nil {
		return empty, err
	}
	b, err := Evaluate(after)
	if err != nil {
		return empty, err
	}
	if a.Scope != b.Scope {
		return empty, ErrScope
	}
	if !a.CompleteWithinSnapshot || !b.CompleteWithinSnapshot {
		return empty, ErrIncomplete
	}
	ai := append([]string{}, before.SensitiveGroupIDs...)
	bi := append([]string{}, after.SensitiveGroupIDs...)
	sort.Strings(ai)
	sort.Strings(bi)
	if a.PolicyID != b.PolicyID || a.ObservationID == b.ObservationID || !reflect.DeepEqual(ai, bi) || len(a.Objects) != len(b.Objects) {
		return empty, ErrIncompatible
	}
	r := ChangeSet{Version: ContractVersion, Simulated: true, Scope: a.Scope, PolicyID: a.PolicyID, BeforeObservationID: a.ObservationID, AfterObservationID: b.ObservationID, Changes: []Change{}}
	for i, x := range a.Objects {
		y := b.Objects[i]
		if x.ObjectID != y.ObjectID || x.Kind != y.Kind {
			return empty, ErrIncompatible
		}
		old, new := map[Provenance]bool{}, map[Provenance]bool{}
		for _, p := range x.Labels {
			old[p] = true
		}
		for _, p := range y.Labels {
			new[p] = true
		}
		for _, p := range x.Labels {
			if !new[p] {
				r.Changes = append(r.Changes, Change{x.ObjectID, "removed", p})
			}
		}
		for _, p := range y.Labels {
			if !old[p] {
				r.Changes = append(r.Changes, Change{x.ObjectID, "added", p})
			}
		}
	}
	return r, nil
}
