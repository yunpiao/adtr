// Package querytemplates is an offline, synthetic value model, not a query executor.
package querytemplates

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	MaxJSONBytes = 1 << 20
	MaxTemplates = 1000
	MaxValues    = 100
)

var (
	ErrInvalid  = errors.New("invalid synthetic template")
	ErrExists   = errors.New("synthetic template already exists")
	ErrNotFound = errors.New("synthetic template not found")
	ErrBuiltin  = errors.New("built-in template is immutable")
	ErrCapacity = errors.New("synthetic capacity reached")
)

type DataSource struct {
	Server  string `json:"server"`
	AppType int    `json:"appType"`
}
type Params struct {
	AppName    []int        `json:"appName"`
	RulesID    []string     `json:"rulesID"`
	Time       []string     `json:"time"`
	DC         []string     `json:"dc"`
	DataSource []DataSource `json:"dataSource"`
	UserList   []string     `json:"userList"`
	IPList     []string     `json:"ipList"`
	Types      []string     `json:"types"`
}
type Template struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Params Params `json:"params"`
}
type Lists struct {
	Default   []Template `json:"defaultTplList"`
	Customize []Template `json:"customizeTplList"`
}

func textOK(s string, max int) bool {
	if !utf8.ValidString(s) || strings.TrimSpace(s) != s || s == "" || utf8.RuneCountInString(s) > max {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// The lexical gate prevents time.Parse from accepting one-digit hours, comma
// fractions, oversized offsets or precision beyond nanoseconds.
var timestamp = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T([01][0-9]|2[0-3]):[0-5][0-9]:[0-5][0-9](\.[0-9]{1,9})?(Z|[+-]([01][0-9]|2[0-3]):[0-5][0-9])$`)

func Validate(p Params) error {
	for _, values := range [][]string{p.RulesID, p.DC, p.UserList, p.IPList, p.Types} {
		if len(values) > MaxValues {
			return ErrInvalid
		}
		seen := map[string]bool{}
		for _, s := range values {
			if !textOK(s, 256) || seen[s] {
				return ErrInvalid
			}
			seen[s] = true
		}
	}
	if len(p.AppName) > MaxValues || len(p.DataSource) > MaxValues {
		return ErrInvalid
	}
	seenInt := map[int]bool{}
	for _, n := range p.AppName {
		if n < 0 || n > 2147483647 || seenInt[n] {
			return ErrInvalid
		}
		seenInt[n] = true
	}
	seenSource := map[DataSource]bool{}
	for _, d := range p.DataSource {
		if !textOK(d.Server, 256) || d.AppType < 0 || d.AppType > 2147483647 || seenSource[d] {
			return ErrInvalid
		}
		seenSource[d] = true
	}
	for _, s := range p.IPList {
		ip, err := netip.ParseAddr(s)
		if err != nil || ip.Zone() != "" {
			return ErrInvalid
		}
	}
	if len(p.Time) != 0 {
		if len(p.Time) != 2 || !timestamp.MatchString(p.Time[0]) || !timestamp.MatchString(p.Time[1]) {
			return ErrInvalid
		}
		a, e1 := time.Parse(time.RFC3339Nano, p.Time[0])
		b, e2 := time.Parse(time.RFC3339Nano, p.Time[1])
		if e1 != nil || e2 != nil || !a.Before(b) {
			return ErrInvalid
		}
	}
	return nil
}
func clone(t Template) Template {
	p := t.Params
	p.AppName = append([]int{}, p.AppName...)
	p.RulesID = append([]string{}, p.RulesID...)
	p.Time = append([]string{}, p.Time...)
	p.DC = append([]string{}, p.DC...)
	p.DataSource = append([]DataSource{}, p.DataSource...)
	p.UserList = append([]string{}, p.UserList...)
	p.IPList = append([]string{}, p.IPList...)
	p.Types = append([]string{}, p.Types...)
	t.Params = p
	return t
}

type entry struct {
	value            Template
	builtin, deleted bool
}

// Store is process-local only. Its zero value is usable. IDs and names remain
// reserved after deletion; no purge, TTL, external I/O or authorization exists.
type Store struct {
	mu      sync.Mutex
	entries map[string]entry
}

func New(defaults []Template) (*Store, error) {
	s := &Store{}
	for _, t := range defaults {
		if err := s.add(t, true); err != nil {
			return nil, err
		}
	}
	return s, nil
}
func (s *Store) add(t Template, builtin bool) error {
	if !textOK(t.ID, 128) || !textOK(t.Name, 50) {
		return ErrInvalid
	}
	if err := Validate(t.Params); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.entries[t.ID]; ok {
		return ErrExists
	}
	for _, e := range s.entries {
		if e.value.Name == t.Name {
			return ErrExists
		}
	}
	if len(s.entries) >= MaxTemplates {
		return ErrCapacity
	}
	if s.entries == nil {
		s.entries = map[string]entry{}
	}
	s.entries[t.ID] = entry{value: clone(t), builtin: builtin}
	return nil
}
func (s *Store) Create(t Template) error { return s.add(t, false) }
func (s *Store) Delete(id string) error  { return s.setDeleted(id, true) }
func (s *Store) Restore(id string) error { return s.setDeleted(id, false) }
func (s *Store) setDeleted(id string, deleted bool) error {
	if !textOK(id, 128) {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[id]
	if !ok {
		return ErrNotFound
	}
	if e.builtin {
		return ErrBuiltin
	}
	e.deleted = deleted
	s.entries[id] = e
	return nil
}

// List searches names by exact, case-sensitive substring and returns stable
// name/ID order. Deleted values remain recoverable but are never listed.
func (s *Store) List(search string) (Lists, error) {
	out := Lists{Default: []Template{}, Customize: []Template{}}
	if search != "" && !textOK(search, 50) {
		return Lists{}, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.entries {
		if e.deleted || !strings.Contains(e.value.Name, search) {
			continue
		}
		if e.builtin {
			out.Default = append(out.Default, clone(e.value))
		} else {
			out.Customize = append(out.Customize, clone(e.value))
		}
	}
	less := func(v []Template) {
		sort.Slice(v, func(i, j int) bool {
			if v[i].Name == v[j].Name {
				return v[i].ID < v[j].ID
			}
			return v[i].Name < v[j].Name
		})
	}
	less(out.Default)
	less(out.Customize)
	return out, nil
}

// Decode rejects unknown or duplicate keys, null, invalid UTF-8, unpaired
// surrogate escapes, excess depth, trailing values and oversized input.
// Field names are case-sensitive; dst must point to a struct.
func Decode(data []byte, dst any) error {
	if len(data) > MaxJSONBytes || !utf8.Valid(data) || !validEscapes(data) {
		return ErrInvalid
	}
	typ := reflect.TypeOf(dst)
	if typ == nil || typ.Kind() != reflect.Pointer || reflect.ValueOf(dst).IsNil() || typ.Elem().Kind() != reflect.Struct {
		return ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if err := walk(d, typ.Elem(), 0); err != nil {
		return fmt.Errorf("%w: JSON structure", ErrInvalid)
	}
	if _, err := d.Token(); err != io.EOF {
		return ErrInvalid
	}
	// Decode into a temporary value so every failure leaves the caller untouched.
	temp := reflect.New(typ.Elem())
	if err := json.Unmarshal(data, temp.Interface()); err != nil {
		return ErrInvalid
	}
	reflect.ValueOf(dst).Elem().Set(temp.Elem())
	return nil
}
func walk(d *json.Decoder, t reflect.Type, depth int) error {
	if depth > 16 {
		return ErrInvalid
	}
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	tok, err := d.Token()
	if err != nil || tok == nil {
		return ErrInvalid
	}
	switch t.Kind() {
	case reflect.Struct:
		if tok != json.Delim('{') {
			return ErrInvalid
		}
		fields := map[string]reflect.Type{}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			name := strings.Split(f.Tag.Get("json"), ",")[0]
			if name != "" && name != "-" {
				fields[name] = f.Type
			}
		}
		seen := map[string]bool{}
		for d.More() {
			key, e := d.Token()
			if e != nil {
				return ErrInvalid
			}
			k, ok := key.(string)
			ft, exists := fields[k]
			if !ok || !exists || seen[k] {
				return ErrInvalid
			}
			seen[k] = true
			if e = walk(d, ft, depth+1); e != nil {
				return e
			}
		}
		end, e := d.Token()
		if e != nil || end != json.Delim('}') {
			return ErrInvalid
		}
	case reflect.Slice:
		if tok != json.Delim('[') {
			return ErrInvalid
		}
		count := 0
		for d.More() {
			count++
			if count > MaxTemplates {
				return ErrInvalid
			}
			if e := walk(d, t.Elem(), depth+1); e != nil {
				return e
			}
		}
		end, e := d.Token()
		if e != nil || end != json.Delim(']') {
			return ErrInvalid
		}
	case reflect.String:
		if _, ok := tok.(string); !ok {
			return ErrInvalid
		}
	case reflect.Int:
		n, ok := tok.(json.Number)
		if !ok {
			return ErrInvalid
		}
		if _, e := strconv.ParseInt(string(n), 10, 32); e != nil {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}
func validEscapes(b []byte) bool {
	// JSON syntax itself is checked by Decoder; this scanner only adds Unicode
	// checks that encoding/json otherwise replaces silently with U+FFFD.
	for i := 0; i < len(b); i++ {
		if b[i] != '\\' {
			continue
		}
		i++
		if i >= len(b) {
			return false
		}
		if b[i] != 'u' {
			continue
		}
		if i+4 >= len(b) {
			return false
		}
		n, e := strconv.ParseUint(string(b[i+1:i+5]), 16, 16)
		if e != nil {
			return false
		}
		i += 4
		if n >= 0xdc00 && n <= 0xdfff {
			return false
		}
		if n >= 0xd800 && n <= 0xdbff {
			if i+6 >= len(b) || b[i+1] != '\\' || b[i+2] != 'u' {
				return false
			}
			low, e := strconv.ParseUint(string(b[i+3:i+7]), 16, 16)
			if e != nil || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		}
	}
	return true
}
