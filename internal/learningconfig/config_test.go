package learningconfig

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func fixture() Input {
	return Input{"rule-1", "source-1", "domain-1", 60, []string{"user.name", "event.kind"}, []string{"user.name"}}
}
func mustConfig(t *testing.T, in Input) Configuration {
	t.Helper()
	c, err := New(in)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCanonicalContract(t *testing.T) {
	c := mustConfig(t, fixture())
	got, err := c.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	want := `{"format":"synthetic-learning-config-v1","synthetic":true,"ruleLabel":"rule-1","dataSourceLabel":"source-1","domainLabel":"domain-1","windowSeconds":60,"allowedFields":["event.kind","user.name"],"selectedFields":["user.name"]}`
	if string(got) != want {
		t.Fatalf("canonical = %s", got)
	}
	sum := sha256.Sum256([]byte(want))
	digest, err := c.Digest()
	if err != nil || digest != hex.EncodeToString(sum[:]) {
		t.Fatalf("digest = %q, %v", digest, err)
	}
}

func TestOrderAndImmutability(t *testing.T) {
	in := fixture()
	in.SelectedFields = []string{"user.name", "event.kind"}
	c := mustConfig(t, in)
	original, _ := c.CanonicalJSON()
	digest, _ := c.Digest()
	reordered := fixture()
	reordered.AllowedFields = []string{"event.kind", "user.name"}
	reordered.SelectedFields = []string{"event.kind", "user.name"}
	other := mustConfig(t, reordered)
	otherDigest, _ := other.Digest()
	if digest != otherDigest {
		t.Fatal("set order changed digest")
	}
	in.AllowedFields[0] = "changed"
	in.SelectedFields[0] = "changed"
	output, _ := c.CanonicalJSON()
	output[0] = '!'
	after, _ := c.CanonicalJSON()
	if !bytes.Equal(after, original) {
		t.Fatal("caller mutation changed config")
	}
	if reordered.AllowedFields[0] != "event.kind" {
		t.Fatal("input mutated")
	}
}

func TestDigestBindsEveryInput(t *testing.T) {
	base := mustConfig(t, fixture())
	original, _ := base.Digest()
	changes := []func(*Input){
		func(i *Input) { i.RuleLabel = "rule-2" }, func(i *Input) { i.DataSourceLabel = "source-2" },
		func(i *Input) { i.DomainLabel = "domain-2" }, func(i *Input) { i.WindowSeconds++ },
		func(i *Input) { i.AllowedFields = append(i.AllowedFields, "unused") },
		func(i *Input) { i.SelectedFields = []string{"event.kind"} },
	}
	for n, change := range changes {
		in := fixture()
		change(&in)
		c := mustConfig(t, in)
		digest, _ := c.Digest()
		if digest == original {
			t.Fatalf("change %d not bound", n)
		}
	}
}

func TestRejectInvalid(t *testing.T) {
	cases := map[string]func(*Input){
		"empty rule": func(i *Input) { i.RuleLabel = "" }, "empty source": func(i *Input) { i.DataSourceLabel = "" }, "empty domain": func(i *Input) { i.DomainLabel = "" },
		"space": func(i *Input) { i.RuleLabel = " rule" }, "control": func(i *Input) { i.DomainLabel = "a\nb" }, "unicode": func(i *Input) { i.DataSourceLabel = "域" },
		"invalid utf8": func(i *Input) { i.RuleLabel = string([]byte{0xff}) }, "label length": func(i *Input) { i.RuleLabel = strings.Repeat("a", 129) },
		"zero window": func(i *Input) { i.WindowSeconds = 0 }, "negative window": func(i *Input) { i.WindowSeconds = -1 }, "large window": func(i *Input) { i.WindowSeconds = MaxWindowSeconds + 1 },
		"nil allowed": func(i *Input) { i.AllowedFields = nil }, "empty selected": func(i *Input) { i.SelectedFields = []string{} },
		"unknown field": func(i *Input) { i.SelectedFields = []string{"unknown"} }, "duplicate allowed": func(i *Input) { i.AllowedFields = append(i.AllowedFields, "user.name") },
		"duplicate selected": func(i *Input) { i.SelectedFields = []string{"user.name", "user.name"} },
		"uppercase field":    func(i *Input) { i.AllowedFields = []string{"USER"} }, "field whitespace": func(i *Input) { i.SelectedFields = []string{" user.name"} },
		"empty field": func(i *Input) { i.AllowedFields = []string{""} }, "field length": func(i *Input) { i.AllowedFields = []string{strings.Repeat("a", 65)} },
		"too many allowed":  func(i *Input) { i.AllowedFields = names(MaxAllowedFields + 1) },
		"too many selected": func(i *Input) { i.AllowedFields = names(MaxSelectedFields + 1); i.SelectedFields = i.AllowedFields },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			in := fixture()
			mutate(&in)
			c, err := New(in)
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("error %v", err)
			}
			if _, err = c.CanonicalJSON(); !errors.Is(err, ErrInvalid) {
				t.Fatal("invalid constructor returned usable config")
			}
			if _, err = c.Digest(); !errors.Is(err, ErrInvalid) {
				t.Fatal("invalid constructor returned digest")
			}
		})
	}
}
func names(n int) []string {
	values := make([]string, n)
	for i := range values {
		values[i] = fmt.Sprintf("field_%03d", i)
	}
	return values
}
func TestAcceptedBoundaries(t *testing.T) {
	for _, window := range []int64{1, MaxWindowSeconds} {
		in := fixture()
		in.WindowSeconds = window
		in.RuleLabel = strings.Repeat("a", 128)
		in.AllowedFields = names(MaxAllowedFields)
		in.SelectedFields = in.AllowedFields[:MaxSelectedFields]
		mustConfig(t, in)
	}
	in := fixture()
	in.AllowedFields = []string{strings.Repeat("a", 64)}
	in.SelectedFields = in.AllowedFields
	mustConfig(t, in)
}
func TestZeroValueAndConcurrentReads(t *testing.T) {
	var zero Configuration
	if b, err := zero.CanonicalJSON(); b != nil || !errors.Is(err, ErrInvalid) {
		t.Fatal("zero JSON accepted")
	}
	if d, err := zero.Digest(); d != "" || !errors.Is(err, ErrInvalid) {
		t.Fatal("zero digest accepted")
	}
	c := mustConfig(t, fixture())
	want, _ := c.Digest()
	var wg sync.WaitGroup
	for n := 0; n < 16; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				d, e := c.Digest()
				if e != nil || d != want {
					t.Error("unstable digest")
				}
				b, e := c.CanonicalJSON()
				if e != nil {
					t.Error(e)
				} else {
					b[0] = '!'
				}
			}
		}()
	}
	wg.Wait()
}
