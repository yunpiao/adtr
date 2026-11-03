package directorychanges_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	dc "github.com/yunpiao/adtr/internal/directorychanges"
)

func baseline(t *testing.T, objects ...dc.Object) dc.State {
	t.Helper()
	s, e := dc.New(dc.Baseline{SourceEpoch: "epoch-1", Complete: true, Objects: objects})
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func object(id string, k dc.Kind, name string) dc.Object {
	return dc.Object{ID: id, Kind: k, Version: 1, Fields: dc.Fields{Name: name, Enabled: true}}
}
func batch(t *testing.T, s dc.State, changes ...dc.Change) dc.Batch {
	t.Helper()
	c := s.Snapshot().Cursor
	b := dc.Batch{SourceEpoch: c.SourceEpoch, Sequence: c.Sequence + 1, PreviousHash: c.Hash, Complete: true, Changes: changes}
	return seal(t, b)
}
func seal(t *testing.T, b dc.Batch) dc.Batch {
	t.Helper()
	h, e := dc.HashBatch(b)
	if e != nil {
		t.Fatal(e)
	}
	b.BatchHash = h
	return b
}
func apply(t *testing.T, s dc.State, b dc.Batch) (dc.State, []dc.Record) {
	t.Helper()
	n, r, e := s.Apply(b)
	if e != nil {
		t.Fatal(e)
	}
	return n, r
}
func reject(t *testing.T, s dc.State, b dc.Batch, want error) {
	t.Helper()
	before := s.Snapshot()
	n, r, e := s.Apply(b)
	if !errors.Is(e, want) {
		t.Fatalf("error %v, want %v", e, want)
	}
	if !reflect.DeepEqual(n, dc.State{}) || r != nil {
		t.Fatal("partial result on failure")
	}
	if !reflect.DeepEqual(s.Snapshot(), before) {
		t.Fatal("input state mutated on failure")
	}
}

func TestHandwrittenTwoBatchOracle(t *testing.T) {
	u := object("u-1", dc.User, "User One")
	g := object("g-1", dc.Group, "Group One")
	c := object("c-1", dc.Computer, "Computer One")
	s := baseline(t, u, c, g)
	frozen := s.Snapshot()
	b1 := batch(t, s, dc.Change{ID: "u-1", Operation: dc.Update, Kind: dc.User, ExpectedVersion: 1, Fields: dc.Fields{Name: "Renamed", Enabled: false}}, dc.Change{ID: "u-2", Operation: dc.Create, Kind: dc.User, Fields: dc.Fields{Name: "New", Enabled: true}})
	s1, r1 := apply(t, s, b1)
	updated := dc.Object{ID: "u-1", Kind: dc.User, Version: 2, Fields: dc.Fields{Name: "Renamed"}}
	created := object("u-2", dc.User, "New")
	want1 := []dc.Record{{Sequence: 1, ID: "u-1", Operation: dc.Update, Before: &u, After: updated}, {Sequence: 1, ID: "u-2", Operation: dc.Create, After: created}}
	if !reflect.DeepEqual(r1, want1) {
		t.Fatalf("records %+v, want %+v", r1, want1)
	}
	b2 := batch(t, s1, dc.Change{ID: "c-1", Operation: dc.Tombstone, Kind: dc.Computer, ExpectedVersion: 1}, dc.Change{ID: "u-1", Operation: dc.Update, Kind: dc.User, ExpectedVersion: 2, Fields: dc.Fields{Name: "Final", Enabled: true}})
	s2, r2 := apply(t, s1, b2)
	deleted := c
	deleted.Version = 2
	deleted.Deleted = true
	final := updated
	final.Version = 3
	final.Fields = dc.Fields{Name: "Final", Enabled: true}
	want2 := []dc.Record{{Sequence: 2, ID: "c-1", Operation: dc.Tombstone, Before: &c, After: deleted}, {Sequence: 2, ID: "u-1", Operation: dc.Update, Before: &updated, After: final}}
	if !reflect.DeepEqual(r2, want2) {
		t.Fatalf("second records %+v", r2)
	}
	want := dc.Snapshot{Cursor: dc.Cursor{SourceEpoch: "epoch-1", Sequence: 2, Hash: b2.BatchHash}, Objects: []dc.Object{deleted, g, final, created}, Records: append(want1, want2...)}
	if !reflect.DeepEqual(s2.Snapshot(), want) {
		t.Fatalf("projection %#v, want %#v", s2.Snapshot(), want)
	}
	if !reflect.DeepEqual(s.Snapshot(), frozen) {
		t.Fatal("baseline mutated")
	}
	if s1.Snapshot().Objects[0].Deleted {
		t.Fatal("prior state mutated")
	}
	replay, rr := apply(t, s2, b1)
	if rr != nil || !reflect.DeepEqual(replay.Snapshot(), want) {
		t.Fatal("historical replay changed projection")
	}
	reject(t, s2, batch(t, s2, dc.Change{ID: "c-1", Operation: dc.Create, Kind: dc.Computer}), dc.ErrConflict)
	reject(t, s2, batch(t, s2, dc.Change{ID: "c-1", Operation: dc.Update, Kind: dc.Computer, ExpectedVersion: 2}), dc.ErrVersion)
}

func TestCanonicalHashIndependentBytes(t *testing.T) {
	s := baseline(t, object("u-1", dc.User, "A"))
	raw := `{"Format":"adtr.synthetic.directorychanges.baseline.v1","SourceEpoch":"epoch-1","Objects":[{"ID":"u-1","Kind":"user","Version":1,"Fields":{"Name":"A","Enabled":true},"Deleted":false}]}`
	sum := sha256.Sum256([]byte(raw))
	want := hex.EncodeToString(sum[:])
	if s.Snapshot().Cursor.Hash != want {
		t.Fatal("baseline hash schema drift")
	}
	b := batch(t, s, dc.Change{ID: "u-1", Operation: dc.Update, Kind: dc.User, ExpectedVersion: 1, Fields: dc.Fields{Name: "B"}})
	raw = `{"Format":"adtr.synthetic.directorychanges.batch.v1","SourceEpoch":"epoch-1","Sequence":1,"PreviousHash":"` + want + `","Complete":true,"Changes":[{"ID":"u-1","Operation":"update","Kind":"user","ExpectedVersion":1,"Fields":{"Name":"B","Enabled":false}}]}`
	sum = sha256.Sum256([]byte(raw))
	if b.BatchHash != hex.EncodeToString(sum[:]) {
		t.Fatal("batch hash schema drift")
	}
	b.BatchHash = "ignored"
	h, e := dc.HashBatch(b)
	if e != nil || h != hex.EncodeToString(sum[:]) {
		t.Fatal("BatchHash included in hash")
	}
}

func TestPermutationsAndReplayConflict(t *testing.T) {
	a := object("a", dc.User, "A")
	z := object("z", dc.Group, "Z")
	s := baseline(t, z, a)
	other := baseline(t, a, z)
	if !reflect.DeepEqual(s.Snapshot(), other.Snapshot()) {
		t.Fatal("baseline ordering")
	}
	b := batch(t, s, dc.Change{ID: "z", Operation: dc.Update, Kind: dc.Group, ExpectedVersion: 1}, dc.Change{ID: "a", Operation: dc.Tombstone, Kind: dc.User, ExpectedVersion: 1})
	rev := b
	rev.Changes = []dc.Change{b.Changes[1], b.Changes[0]}
	rev = seal(t, rev)
	if rev.BatchHash != b.BatchHash {
		t.Fatal("permutation hash")
	}
	n, r := apply(t, s, b)
	m, q := apply(t, other, rev)
	if !reflect.DeepEqual(n.Snapshot(), m.Snapshot()) || !reflect.DeepEqual(r, q) {
		t.Fatal("permutation result")
	}
	replayed, rr := apply(t, n, rev)
	if rr != nil || !reflect.DeepEqual(replayed.Snapshot(), n.Snapshot()) {
		t.Fatal("permutation replay")
	}
	changed := b
	changed.Changes = append([]dc.Change(nil), b.Changes...)
	changed.Changes[0].Fields.Name = "Changed"
	changed = seal(t, changed)
	reject(t, n, changed, dc.ErrConflict)
}

func TestEmptyCompleteAndIncomplete(t *testing.T) {
	s := baseline(t, object("u", dc.User, "U"))
	b := batch(t, s)
	n, r := apply(t, s, b)
	if r != nil || n.Snapshot().Cursor.Sequence != 1 || !reflect.DeepEqual(s.Snapshot().Objects, n.Snapshot().Objects) {
		t.Fatal("empty complete batch must advance only cursor")
	}
	b = batch(t, n)
	b.Complete = false
	reject(t, n, b, dc.ErrInvalid)
	if _, e := dc.New(dc.Baseline{SourceEpoch: "epoch-1"}); !errors.Is(e, dc.ErrInvalid) {
		t.Fatal("incomplete baseline accepted")
	}
}

func TestFailureAtomicity(t *testing.T) {
	s := baseline(t, object("a", dc.User, "A"), object("z", dc.Group, "Z"))
	good := dc.Change{ID: "a", Operation: dc.Update, Kind: dc.User, ExpectedVersion: 1, Fields: dc.Fields{Name: "changed"}}
	for _, tc := range []struct {
		name   string
		change dc.Change
		err    error
	}{
		{"wrong-version", dc.Change{ID: "z", Operation: dc.Update, Kind: dc.Group, ExpectedVersion: 9}, dc.ErrVersion},
		{"wrong-kind", dc.Change{ID: "z", Operation: dc.Update, Kind: dc.User, ExpectedVersion: 1}, dc.ErrVersion},
		{"missing", dc.Change{ID: "zz", Operation: dc.Tombstone, Kind: dc.User, ExpectedVersion: 1}, dc.ErrVersion},
		{"existing-create", dc.Change{ID: "z", Operation: dc.Create, Kind: dc.Group}, dc.ErrConflict},
	} {
		t.Run(tc.name, func(t *testing.T) { reject(t, s, batch(t, s, good, tc.change), tc.err) })
	}
	valid := batch(t, s, good)
	for _, tc := range []struct {
		name string
		edit func(*dc.Batch)
		err  error
	}{
		{"gap", func(b *dc.Batch) { b.Sequence = 3; *b = seal(t, *b) }, dc.ErrSequence},
		{"epoch", func(b *dc.Batch) { b.SourceEpoch = "epoch-2"; *b = seal(t, *b) }, dc.ErrEpoch},
		{"previous", func(b *dc.Batch) { b.PreviousHash = strings.Repeat("0", 64); *b = seal(t, *b) }, dc.ErrHash},
		{"hash", func(b *dc.Batch) { b.BatchHash = strings.Repeat("0", 64) }, dc.ErrHash},
		{"duplicate", func(b *dc.Batch) { b.Changes = append(b.Changes, b.Changes[0]) }, dc.ErrConflict},
		{"zero-sequence", func(b *dc.Batch) { b.Sequence = 0 }, dc.ErrInvalid},
		{"incomplete", func(b *dc.Batch) { b.Complete = false }, dc.ErrInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := valid
			b.Changes = append([]dc.Change(nil), valid.Changes...)
			tc.edit(&b)
			reject(t, s, b, tc.err)
		})
	}
	reject(t, dc.State{}, valid, dc.ErrInvalid)
}

func TestCallerMutationIsolation(t *testing.T) {
	objects := []dc.Object{object("a", dc.User, "A")}
	s := baseline(t, objects...)
	original := s.Snapshot()
	objects[0].Fields.Name = "external"
	if !reflect.DeepEqual(s.Snapshot(), original) {
		t.Fatal("baseline alias")
	}
	b := batch(t, s, dc.Change{ID: "a", Operation: dc.Update, Kind: dc.User, ExpectedVersion: 1, Fields: dc.Fields{Name: "B"}})
	n, r := apply(t, s, b)
	want := n.Snapshot()
	b.Changes[0].Fields.Name = "external"
	r[0].Before.Fields.Name = "external"
	r[0].After.Fields.Name = "external"
	if !reflect.DeepEqual(n.Snapshot(), want) {
		t.Fatal("input/output alias")
	}
	snap := n.Snapshot()
	snap.Objects[0].Fields.Name = "external"
	snap.Records[0].Before.Fields.Name = "external"
	snap.Records[0].After.Fields.Name = "external"
	snap.Cursor.Hash = "external"
	if !reflect.DeepEqual(n.Snapshot(), want) || !reflect.DeepEqual(s.Snapshot(), original) {
		t.Fatal("snapshot alias")
	}
}

func TestLimits(t *testing.T) {
	var objects []dc.Object
	for i := 0; i < 64; i++ {
		objects = append(objects, object(fmt.Sprintf("u-%02d", i), dc.User, "U"))
	}
	s := baseline(t, objects...)
	over := append(append([]dc.Object{}, objects...), object("u-extra", dc.User, "extra"))
	if n, e := dc.New(dc.Baseline{SourceEpoch: "epoch-1", Complete: true, Objects: over}); !errors.Is(e, dc.ErrLimit) || !reflect.DeepEqual(n, dc.State{}) {
		t.Fatal("baseline limit")
	}
	reject(t, s, batch(t, s, dc.Change{ID: "u-extra", Operation: dc.Create, Kind: dc.User}), dc.ErrLimit)
	changes := []dc.Change{}
	for i := 0; i < 32; i++ {
		changes = append(changes, dc.Change{ID: fmt.Sprintf("u-%02d", i), Operation: dc.Update, Kind: dc.User, ExpectedVersion: 1, Fields: dc.Fields{Name: "changed"}})
	}
	b := batch(t, s, changes...)
	n, r := apply(t, s, b)
	if len(r) != 32 {
		t.Fatal("boundary changes truncated")
	}
	b.Changes = append(b.Changes, dc.Change{ID: "u-32", Operation: dc.Update, Kind: dc.User, ExpectedVersion: 1})
	reject(t, s, b, dc.ErrLimit)
	empty := baseline(t)
	var first dc.Batch
	for i := 0; i < 32; i++ {
		b := batch(t, empty)
		if i == 0 {
			first = b
		}
		empty, _ = apply(t, empty, b)
	}
	reject(t, empty, batch(t, empty), dc.ErrLimit)
	replayed, rr := apply(t, empty, first)
	if rr != nil || !reflect.DeepEqual(empty.Snapshot(), replayed.Snapshot()) {
		t.Fatal("replay at limit")
	}
	dead, _ := apply(t, n, batch(t, n, dc.Change{ID: "u-00", Operation: dc.Tombstone, Kind: dc.User, ExpectedVersion: 2}))
	reject(t, dead, batch(t, dead, dc.Change{ID: "new", Operation: dc.Create, Kind: dc.User}), dc.ErrLimit)
}

func TestValidation(t *testing.T) {
	for _, o := range []dc.Object{{ID: "x", Kind: dc.User, Version: 0}, {ID: "X", Kind: dc.User, Version: 1}, {ID: "x", Kind: "unknown", Version: 1}, {ID: "x", Kind: dc.User, Version: 1, Deleted: true}, {ID: "x", Kind: dc.User, Version: 1, Fields: dc.Fields{Name: "\n"}}, {ID: "x", Kind: dc.User, Version: 1, Fields: dc.Fields{Name: string([]byte{0xff})}}, {ID: "x", Kind: dc.User, Version: 1, Fields: dc.Fields{Name: strings.Repeat("x", 257)}}} {
		if n, e := dc.New(dc.Baseline{SourceEpoch: "epoch-1", Complete: true, Objects: []dc.Object{o}}); !errors.Is(e, dc.ErrInvalid) || !reflect.DeepEqual(n, dc.State{}) {
			t.Fatalf("invalid baseline accepted: %#v, %v", o, e)
		}
	}
	o := object("x", dc.User, "X")
	if _, e := dc.New(dc.Baseline{SourceEpoch: "epoch-1", Complete: true, Objects: []dc.Object{o, o}}); !errors.Is(e, dc.ErrConflict) {
		t.Fatal("duplicate baseline")
	}
	s := baseline(t, o)
	for _, tc := range []struct {
		c dc.Change
		e error
	}{
		{dc.Change{ID: "x", Operation: dc.Update, Kind: dc.User}, dc.ErrVersion},
		{dc.Change{ID: "x", Operation: dc.Tombstone, Kind: dc.User}, dc.ErrVersion},
		{dc.Change{ID: "new", Operation: dc.Create, Kind: dc.User, ExpectedVersion: 1}, dc.ErrVersion},
		{dc.Change{ID: "x", Operation: dc.Tombstone, Kind: dc.User, ExpectedVersion: 1, Fields: dc.Fields{Name: "not-a-patch"}}, dc.ErrInvalid},
		{dc.Change{ID: "x", Operation: "delete", Kind: dc.User, ExpectedVersion: 1}, dc.ErrInvalid},
		{dc.Change{ID: "X", Operation: dc.Update, Kind: dc.User, ExpectedVersion: 1}, dc.ErrInvalid},
	} {
		b := batch(t, s)
		b.Changes = []dc.Change{tc.c}
		reject(t, s, b, tc.e)
	}
	baseline(t, object("x", dc.User, strings.Repeat("x", 256)))
}

func TestConcurrentImmutableBranches(t *testing.T) {
	s := baseline(t, object("u", dc.User, "original"))
	b := batch(t, s, dc.Change{ID: "u", Operation: dc.Update, Kind: dc.User, ExpectedVersion: 1, Fields: dc.Fields{Name: "updated"}})
	want := s.Snapshot()
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n, r, e := s.Apply(b)
			if e != nil {
				t.Error(e)
				return
			}
			snap := n.Snapshot()
			snap.Objects[0].Fields.Name = "caller"
			r[0].Before.Fields.Name = "caller"
			if n.Snapshot().Objects[0].Fields.Name != "updated" {
				t.Error("branch alias")
			}
			if !reflect.DeepEqual(s.Snapshot(), want) {
				t.Error("prior state mutation")
			}
		}()
	}
	wg.Wait()
}

func TestMaxUint64CannotEnterCursorOrVersions(t *testing.T) {
	const max = ^uint64(0)
	s := baseline(t, object("u", dc.User, "original"))
	b := batch(t, s)
	b.Sequence = max
	reject(t, s, seal(t, b), dc.ErrSequence)
	for _, op := range []dc.Operation{dc.Create, dc.Update, dc.Tombstone} {
		t.Run(string(op), func(t *testing.T) {
			b := batch(t, s)
			b.Changes = []dc.Change{{ID: "u", Operation: op, Kind: dc.User, ExpectedVersion: max}}
			if op != dc.Create {
				b = seal(t, b)
			}
			reject(t, s, b, dc.ErrVersion)
		})
	}
	o := object("u", dc.User, "original")
	o.Version = max
	n, err := dc.New(dc.Baseline{SourceEpoch: "epoch-1", Complete: true, Objects: []dc.Object{o}})
	if !errors.Is(err, dc.ErrInvalid) || !reflect.DeepEqual(n, dc.State{}) {
		t.Fatal("max baseline version accepted", err)
	}
	// Failed inputs did not poison the original state: ordinary version 1 -> 2 works.
	n, _ = apply(t, s, batch(t, s, dc.Change{ID: "u", Operation: dc.Update, Kind: dc.User, ExpectedVersion: 1}))
	if n.Snapshot().Cursor.Sequence != 1 || n.Snapshot().Objects[0].Version != 2 {
		t.Fatal("overflow rejection changed state")
	}
}

func TestMalformedEpochAndHash(t *testing.T) {
	s := baseline(t)
	for _, epoch := range []string{"", "Epoch-1", "epoch 1", "é", strings.Repeat("e", 65)} {
		t.Run("epoch-"+epoch, func(t *testing.T) {
			b := batch(t, s)
			b.SourceEpoch = epoch
			reject(t, s, b, dc.ErrInvalid)
			n, err := dc.New(dc.Baseline{SourceEpoch: epoch, Complete: true})
			if !errors.Is(err, dc.ErrInvalid) || !reflect.DeepEqual(n, dc.State{}) {
				t.Fatal("malformed baseline epoch accepted", err)
			}
			if h, err := dc.HashBatch(b); h != "" || !errors.Is(err, dc.ErrInvalid) {
				t.Fatal("malformed epoch hashed", err)
			}
		})
	}
	for _, hash := range []string{"", strings.Repeat("a", 63), strings.Repeat("a", 65), strings.Repeat("A", 64), strings.Repeat("g", 64)} {
		t.Run("hash-"+hash, func(t *testing.T) {
			b := batch(t, s)
			b.PreviousHash = hash
			reject(t, s, b, dc.ErrInvalid)
			if h, err := dc.HashBatch(b); h != "" || !errors.Is(err, dc.ErrInvalid) {
				t.Fatal("malformed predecessor hashed", err)
			}
			b = batch(t, s)
			b.BatchHash = hash
			reject(t, s, b, dc.ErrHash)
		})
	}
}

func TestCanonicalNilEmptyEquivalence(t *testing.T) {
	nilState, err := dc.New(dc.Baseline{SourceEpoch: "epoch-1", Complete: true})
	if err != nil {
		t.Fatal(err)
	}
	emptyState, err := dc.New(dc.Baseline{SourceEpoch: "epoch-1", Complete: true, Objects: []dc.Object{}})
	if err != nil {
		t.Fatal(err)
	}
	raw := `{"Format":"adtr.synthetic.directorychanges.baseline.v1","SourceEpoch":"epoch-1","Objects":[]}`
	sum := sha256.Sum256([]byte(raw))
	want := hex.EncodeToString(sum[:])
	if nilState.Snapshot().Cursor.Hash != want || emptyState.Snapshot().Cursor.Hash != want {
		t.Fatal("nil/empty baseline hash drift")
	}
	b := batch(t, nilState)
	empty := b
	empty.Changes = []dc.Change{}
	empty = seal(t, empty)
	raw = `{"Format":"adtr.synthetic.directorychanges.batch.v1","SourceEpoch":"epoch-1","Sequence":1,"PreviousHash":"` + want + `","Complete":true,"Changes":[]}`
	sum = sha256.Sum256([]byte(raw))
	want = hex.EncodeToString(sum[:])
	if b.BatchHash != want || empty.BatchHash != want {
		t.Fatal("nil/empty batch hash drift")
	}
	n, _ := apply(t, nilState, b)
	replay, r := apply(t, n, empty)
	if r != nil || !reflect.DeepEqual(n.Snapshot(), replay.Snapshot()) {
		t.Fatal("nil/empty replay differed")
	}
}

func TestCanonicalEscapingIndependentLiteralBytes(t *testing.T) {
	name := "<>&\u2028\u2029"
	s := baseline(t, object("u", dc.User, name))
	// These literal bytes intentionally do not invoke json.Marshal or the core's
	// serializer. They pin lowercase JSON escapes, including HTML-safe encoding.
	raw := `{"Format":"adtr.synthetic.directorychanges.baseline.v1","SourceEpoch":"epoch-1","Objects":[{"ID":"u","Kind":"user","Version":1,"Fields":{"Name":"\u003c\u003e\u0026\u2028\u2029","Enabled":true},"Deleted":false}]}`
	sum := sha256.Sum256([]byte(raw))
	want := hex.EncodeToString(sum[:])
	if s.Snapshot().Cursor.Hash != want {
		t.Fatal("baseline escaping drift")
	}
	b := batch(t, s, dc.Change{ID: "u", Operation: dc.Update, Kind: dc.User, ExpectedVersion: 1, Fields: dc.Fields{Name: name}})
	raw = `{"Format":"adtr.synthetic.directorychanges.batch.v1","SourceEpoch":"epoch-1","Sequence":1,"PreviousHash":"` + want + `","Complete":true,"Changes":[{"ID":"u","Operation":"update","Kind":"user","ExpectedVersion":1,"Fields":{"Name":"\u003c\u003e\u0026\u2028\u2029","Enabled":false}}]}`
	sum = sha256.Sum256([]byte(raw))
	if b.BatchHash != hex.EncodeToString(sum[:]) {
		t.Fatal("batch escaping drift")
	}
	n, _ := apply(t, s, b)
	if n.Snapshot().Objects[0].Fields.Name != name {
		t.Fatal("hash escaping changed stored scalar")
	}
}
