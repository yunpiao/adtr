//go:build integration

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var errProtocol = errors.New("control_invalid")
var ticketPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)
var identifierPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)

type armRecord struct {
	Version        int    `json:"version"`
	Ticket         string `json:"ticket"`
	TenantID       string `json:"tenantId"`
	ActorID        string `json:"actorId"`
	DomainID       string `json:"domainId"`
	AccountID      string `json:"accountId"`
	IdempotencyKey string `json:"idempotencyKey"`
}

func (a armRecord) valid() bool {
	n, err := strconv.ParseInt(a.ActorID, 10, 64)
	return a.Version == 1 && ticketPattern.MatchString(a.Ticket) && identifierPattern.MatchString(a.TenantID) &&
		identifierPattern.MatchString(a.DomainID) && a.DomainID != "platform" && identifierPattern.MatchString(a.AccountID) && a.AccountID != "platform" &&
		identifierPattern.MatchString(a.IdempotencyKey) && !strings.HasPrefix(a.IdempotencyKey, "schedule_") && err == nil && n > 0 && strconv.FormatInt(n, 10) == a.ActorID
}

type marker struct {
	Version int    `json:"version"`
	Ticket  string `json:"ticket"`
	Phase   string `json:"phase"`
}

type fixtureArm struct {
	Version   int    `json:"version"`
	Ticket    string `json:"ticket"`
	Operation string `json:"operation"`
}

type status struct {
	Version                int       `json:"version"`
	Ticket                 string    `json:"ticket"`
	Phase                  string    `json:"phase"`
	Sequence               uint64    `json:"sequence"`
	Armed                  bool      `json:"armed"`
	LedgerLocked           bool      `json:"ledgerLocked"`
	LockHeld               bool      `json:"lockHeld"`
	FixtureReached         bool      `json:"fixtureReached"`
	FixtureReleased        bool      `json:"fixtureReleased"`
	FixtureResponseWritten bool      `json:"fixtureResponseWritten"`
	FixtureHandlerClosed   bool      `json:"fixtureHandlerClosed"`
	Released               bool      `json:"released"`
	Quiesced               bool      `json:"quiesced"`
	ErrorCode              string    `json:"errorCode"`
	Snapshot               *snapshot `json:"snapshot"`
}

type snapshot struct {
	TaskID                         string `json:"taskId"`
	TenantID                       string `json:"tenantId"`
	ActorID                        string `json:"actorId"`
	DomainID                       string `json:"domainId"`
	AccountID                      string `json:"accountId"`
	TaskKind                       string `json:"taskKind"`
	TaskState                      string `json:"taskState"`
	UseState                       string `json:"useState"`
	OpenedAtPresent                bool   `json:"openedAtPresent"`
	OpenerPresent                  bool   `json:"openerPresent"`
	OpenerUnchanged                bool   `json:"openerUnchanged"`
	QuiescedAtPresent              bool   `json:"quiescedAtPresent"`
	QuiescenceReason               string `json:"quiescenceReason"`
	DiagnosticCode                 string `json:"diagnosticCode"`
	DiagnosticSuccessful           bool   `json:"diagnosticSuccessful"`
	AccountRevision                string `json:"accountRevision"`
	AccountCredentialRevision      string `json:"accountCredentialRevision"`
	AccountDeleted                 bool   `json:"accountDeleted"`
	AccountCredentialPresent       bool   `json:"accountCredentialPresent"`
	ConnectionRevision             string `json:"connectionRevision"`
	ConnectionCredentialGeneration string `json:"connectionCredentialGeneration"`
	CredentialSource               string `json:"credentialSource"`
	AccountPointerPresent          bool   `json:"accountPointerPresent"`
	AccountPointerMatches          bool   `json:"accountPointerMatches"`
	CustomCredentialCount          int64  `json:"customCredentialCount"`
	BindingDependencyCount         int64  `json:"bindingDependencyCount"`
	ExactBindingDependencyCount    int64  `json:"exactBindingDependencyCount"`
	TaskDependencyCount            int64  `json:"taskDependencyCount"`
	ExactTaskDependencyCount       int64  `json:"exactTaskDependencyCount"`
	TotalDependencyCount           int64  `json:"totalDependencyCount"`
}

func (s snapshot) isQuiesced() bool {
	return s.UseState == "quiesced" && s.OpenedAtPresent && s.OpenerPresent && s.OpenerUnchanged && s.QuiescedAtPresent && s.QuiescenceReason == "executor_returned" && s.ExactTaskDependencyCount == 0
}

// A second token walk rejects duplicates and nulls that encoding/json otherwise
// accepts. Control objects have only scalar members, all explicitly required.
func decodeStrict(data []byte, target any, keys ...string) error {
	if len(data) > 4096 {
		return errProtocol
	}
	d := json.NewDecoder(bytes.NewReader(data))
	t, err := d.Token()
	if err != nil || t != json.Delim('{') {
		return errProtocol
	}
	seen := make(map[string]bool, len(keys))
	allowed := make(map[string]bool, len(keys))
	for _, k := range keys {
		allowed[k] = true
	}
	for d.More() {
		t, err = d.Token()
		k, ok := t.(string)
		if err != nil || !ok || !allowed[k] || seen[k] {
			return errProtocol
		}
		seen[k] = true
		var v json.RawMessage
		if d.Decode(&v) != nil || bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return errProtocol
		}
	}
	if t, err = d.Token(); err != nil || t != json.Delim('}') || len(seen) != len(keys) {
		return errProtocol
	}
	if _, err = d.Token(); !errors.Is(err, io.EOF) {
		return errProtocol
	}
	d = json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if d.Decode(target) != nil {
		return errProtocol
	}
	return nil
}

func readControl(dir, name string) ([]byte, bool, error) {
	p := filepath.Join(dir, name)
	info, err := os.Lstat(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > 4096 {
		return nil, false, errProtocol
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, false, errProtocol
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil || len(b) > 4096 {
		return nil, false, errProtocol
	}
	return b, true, nil
}

func readArm(dir string) (armRecord, bool, error) {
	b, exists, err := readControl(dir, "barrier-arm.json")
	var a armRecord
	if err != nil || !exists {
		return a, exists, err
	}
	if decodeStrict(b, &a, "version", "ticket", "tenantId", "actorId", "domainId", "accountId", "idempotencyKey") != nil || !a.valid() {
		return a, true, errProtocol
	}
	return a, true, nil
}

func readMarker(dir, name, ticket, phase string) (bool, error) {
	b, exists, err := readControl(dir, name)
	if err != nil || !exists {
		return false, err
	}
	var m marker
	if decodeStrict(b, &m, "version", "ticket", "phase") != nil || m.Version != 1 || m.Ticket != ticket || m.Phase != phase {
		return false, errProtocol
	}
	return true, nil
}

func writeAtomic(dir, name string, value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return errProtocol
	}
	f, err := os.CreateTemp(dir, ".barrier-*.tmp")
	if err != nil {
		return errProtocol
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(append(b, '\n')); err != nil {
		f.Close()
		return errProtocol
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return errProtocol
	}
	if err = f.Close(); err != nil {
		return errProtocol
	}
	if err = os.Rename(f.Name(), filepath.Join(dir, name)); err != nil {
		return errProtocol
	}
	return nil
}
