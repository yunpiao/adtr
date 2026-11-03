// Package directorychanges implements synthetic, bounded, in-memory replay only.
// It is not an LDAP change source, authorization boundary, or persistence layer.
package directorychanges

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"unicode"
	"unicode/utf8"
)

const (
	MaxObjects   = 64 // Includes logical tombstones; IDs cannot be reused.
	MaxBatches   = 32
	MaxChanges   = 32
	MaxTextBytes = 256
)

var (
	ErrInvalid  = errors.New("invalid_synthetic_change_input")
	ErrLimit    = errors.New("synthetic_change_limit")
	ErrEpoch    = errors.New("synthetic_change_epoch")
	ErrSequence = errors.New("synthetic_change_sequence")
	ErrHash     = errors.New("synthetic_change_hash")
	ErrConflict = errors.New("synthetic_change_conflict")
	ErrVersion  = errors.New("synthetic_change_version")
)

type Kind string

const (
	User     Kind = "user"
	Group    Kind = "group"
	Computer Kind = "computer"
)

type Operation string

const (
	Create    Operation = "create"
	Update    Operation = "update"
	Tombstone Operation = "tombstone"
)

// Fields is the entire synthetic scalar payload, not a patch. Empty Name and
// false Enabled are known values, not unknown/null or real AD interpretations.
type Fields struct {
	Name    string
	Enabled bool
}
type Object struct {
	ID      string
	Kind    Kind
	Version uint64
	Fields  Fields
	Deleted bool
}
type Baseline struct {
	SourceEpoch string
	Complete    bool
	Objects     []Object
}
type Change struct {
	ID              string
	Operation       Operation
	Kind            Kind
	ExpectedVersion uint64
	Fields          Fields
}
type Batch struct {
	SourceEpoch  string
	Sequence     uint64
	PreviousHash string
	Complete     bool
	Changes      []Change
	BatchHash    string
}
type Cursor struct {
	SourceEpoch string
	Sequence    uint64
	Hash        string
}

// Record retains complete before/after typed values, including unchanged fields.
// Before is nil only for creation. Tombstones preserve the previous scalar values.
type Record struct {
	Sequence  uint64
	ID        string
	Operation Operation
	Before    *Object
	After     Object
}
type Snapshot struct {
	Cursor  Cursor
	Objects []Object
	Records []Record
}

// State has no mutable exported internals. Its zero value is invalid. Each Apply
// returns a new state; Snapshot returns detached slices and before pointers.
type State struct {
	cursor   Cursor
	objects  map[string]Object
	records  []Record
	receipts map[uint64]string
}

func validText(s string, empty bool) bool {
	if (!empty && s == "") || len(s) > MaxTextBytes || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func validKind(k Kind) bool { return k == User || k == Group || k == Computer }

// IDs and epochs use exact lowercase ASCII tokens; there is no case folding or
// whitespace normalization that could silently merge distinct source identities.
func validID(s string) bool {
	if len(s) == 0 || len(s) > 64 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}
func validHash(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func digest(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic("fixed synthetic hash schema is not encodable")
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// New fixes a complete baseline at sequence zero. Every baseline version is one.
func New(b Baseline) (State, error) {
	if !b.Complete || !validID(b.SourceEpoch) {
		return State{}, ErrInvalid
	}
	if len(b.Objects) > MaxObjects {
		return State{}, ErrLimit
	}
	objects := append([]Object{}, b.Objects...)
	m := make(map[string]Object, len(objects))
	for _, o := range objects {
		if !validID(o.ID) || !validKind(o.Kind) || o.Version != 1 || o.Deleted || !validText(o.Fields.Name, true) {
			return State{}, ErrInvalid
		}
		if _, ok := m[o.ID]; ok {
			return State{}, ErrConflict
		}
		m[o.ID] = o
	}
	sort.Slice(objects, func(i, j int) bool { return objects[i].ID < objects[j].ID })
	h := digest(struct {
		Format      string
		SourceEpoch string
		Objects     []Object
	}{"adtr.synthetic.directorychanges.baseline.v1", b.SourceEpoch, objects})
	return State{Cursor{b.SourceEpoch, 0, h}, m, nil, make(map[uint64]string)}, nil
}
func canonical(b Batch) ([]Change, error) {
	if !validID(b.SourceEpoch) || b.Sequence == 0 || !validHash(b.PreviousHash) || !b.Complete {
		return nil, ErrInvalid
	}
	if len(b.Changes) > MaxChanges {
		return nil, ErrLimit
	}
	changes := append([]Change{}, b.Changes...)
	seen := make(map[string]bool, len(changes))
	for _, c := range changes {
		if !validID(c.ID) || !validKind(c.Kind) || !validText(c.Fields.Name, true) {
			return nil, ErrInvalid
		}
		if seen[c.ID] {
			return nil, ErrConflict
		}
		seen[c.ID] = true
		switch c.Operation {
		case Create:
			if c.ExpectedVersion != 0 {
				return nil, ErrVersion
			}
		case Update:
			if c.ExpectedVersion == 0 {
				return nil, ErrVersion
			}
		case Tombstone:
			if c.ExpectedVersion == 0 {
				return nil, ErrVersion
			}
			if c.Fields != (Fields{}) {
				return nil, ErrInvalid
			}
		default:
			return nil, ErrInvalid
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].ID < changes[j].ID })
	return changes, nil
}

// HashBatch hashes canonical content excluding BatchHash. It validates structural
// input, not state-dependent sequence, epoch or expected object versions.
func HashBatch(b Batch) (string, error) {
	c, err := canonical(b)
	if err != nil {
		return "", err
	}
	return batchDigest(b, c), nil
}
func batchDigest(b Batch, c []Change) string {
	return digest(struct {
		Format       string
		SourceEpoch  string
		Sequence     uint64
		PreviousHash string
		Complete     bool
		Changes      []Change
	}{"adtr.synthetic.directorychanges.batch.v1", b.SourceEpoch, b.Sequence, b.PreviousHash, b.Complete, c})
}

// Apply is fail-whole: on every error it returns zero State and nil records.
// Replay of an accepted canonical (epoch,sequence) content returns the current
// state and no new records, even when later batches have already been applied.
func (s State) Apply(b Batch) (State, []Record, error) {
	if s.objects == nil {
		return State{}, nil, ErrInvalid
	}
	c, err := canonical(b)
	if err != nil {
		return State{}, nil, err
	}
	if b.SourceEpoch != s.cursor.SourceEpoch {
		return State{}, nil, ErrEpoch
	}
	h := batchDigest(b, c)
	if b.BatchHash != h {
		return State{}, nil, ErrHash
	}
	if old, ok := s.receipts[b.Sequence]; ok {
		if old != h {
			return State{}, nil, ErrConflict
		}
		return s, nil, nil
	}
	if b.Sequence != s.cursor.Sequence+1 {
		return State{}, nil, ErrSequence
	}
	if b.PreviousHash != s.cursor.Hash {
		return State{}, nil, ErrHash
	}
	if len(s.receipts) >= MaxBatches {
		return State{}, nil, ErrLimit
	}
	m := make(map[string]Object, len(s.objects))
	for k, v := range s.objects {
		m[k] = v
	}
	var records []Record
	for _, change := range c {
		old, exists := m[change.ID]
		var before *Object
		var next Object
		switch change.Operation {
		case Create:
			if exists {
				return State{}, nil, ErrConflict
			}
			next = Object{change.ID, change.Kind, 1, change.Fields, false}
		case Update, Tombstone:
			if !exists || old.Deleted || old.Kind != change.Kind || old.Version != change.ExpectedVersion {
				return State{}, nil, ErrVersion
			}
			copy := old
			before = &copy
			next = old
			next.Version++
			if change.Operation == Tombstone {
				next.Deleted = true
			} else {
				next.Fields = change.Fields
			}
		}
		m[change.ID] = next
		records = append(records, Record{b.Sequence, change.ID, change.Operation, before, next})
	}
	if len(m) > MaxObjects {
		return State{}, nil, ErrLimit
	}
	receipts := make(map[uint64]string, len(s.receipts)+1)
	for k, v := range s.receipts {
		receipts[k] = v
	}
	receipts[b.Sequence] = h
	history := cloneRecords(s.records)
	history = append(history, cloneRecords(records)...)
	return State{Cursor{b.SourceEpoch, b.Sequence, h}, m, history, receipts}, cloneRecords(records), nil
}
func cloneRecords(in []Record) []Record {
	out := append([]Record(nil), in...)
	for i := range out {
		if out[i].Before != nil {
			v := *out[i].Before
			out[i].Before = &v
		}
	}
	return out
}
func (s State) Snapshot() Snapshot {
	out := Snapshot{Cursor: s.cursor, Records: cloneRecords(s.records)}
	for _, o := range s.objects {
		out.Objects = append(out.Objects, o)
	}
	sort.Slice(out.Objects, func(i, j int) bool { return out.Objects[i].ID < out.Objects[j].ID })
	return out
}
