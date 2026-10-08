// Package operationallogs records a bounded, typed, best-effort application
// journal. It does not collect files, request data, or historical stdout.
package operationallogs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"github.com/yunpiao/adtr/internal/schemaversion"
	"regexp"
	"time"
)

const (
	SchemaVersion      = schemaversion.Current
	OwnerTenantID      = "default"
	MaxWindow          = 24 * time.Hour
	QueueCapacity      = 128
	MaxRecorderReports = 100
)

type Module string

const (
	API    Module = "api"
	Worker Module = "worker"
)

type EventCode string

const (
	ServiceStartRequested EventCode = "service_start_requested"
	ServiceStopped        EventCode = "service_stopped"
	ServiceFailed         EventCode = "service_failed"
	QueueCycle            EventCode = "queue_cycle"
	RecoveryCycle         EventCode = "recovery_cycle"
	SchedulerCycle        EventCode = "scheduler_cycle"
	QueueProgress         EventCode = "queue_progress"
)

type Outcome string

const (
	Attempted Outcome = "attempted"
	Completed Outcome = "completed"
	Failed    Outcome = "failed"
	Progress  Outcome = "progress"
)

type Severity string

const (
	Info          Severity = "info"
	ErrorSeverity Severity = "error"
)

type Reason string

const (
	NoReason     Reason = ""
	ServeFailed  Reason = "serve_failed"
	WorkerFailed Reason = "worker_failed"
	CycleFailed  Reason = "cycle_failed"
)

type Event struct {
	SchemaVersion int       `json:"schemaVersion"`
	EventID       string    `json:"eventId"`
	ProcessID     string    `json:"processId"`
	Module        Module    `json:"module"`
	Code          EventCode `json:"code"`
	Outcome       Outcome   `json:"outcome"`
	Severity      Severity  `json:"severity"`
	Reason        Reason    `json:"reason"`
	ObservedAt    time.Time `json:"observedAt"`
	RecordedAt    time.Time `json:"recordedAt"`
}
type Principal struct {
	TenantID string
	ActorID  int64
}
type Selection struct {
	StartTm    string   `json:"startTm"`
	EndTm      string   `json:"endTm"`
	SystemType []string `json:"systemType"`
}
type Filter struct {
	PageIdx  int `json:"pageIdx"`
	PageSize int `json:"pageSize"`
	Selection
}
type Page struct {
	PageIdx   int `json:"pageIdx"`
	PageSize  int `json:"pageSize"`
	Total     int `json:"total"`
	TotalPage int `json:"totalPage"`
}
type Row struct {
	Event Event  `json:"event"`
	Log   string `json:"log"`
}
type List struct {
	Page      Page      `json:"page"`
	List      []Row     `json:"List"`
	Exhausted bool      `json:"exhausted"`
	Selection Selection `json:"selection"`
	Coverage  Coverage  `json:"coverage"`
}
type Coverage struct {
	Mode             string     `json:"mode"`
	GapsPossible     bool       `json:"gapsPossible"`
	JournalCreatedAt time.Time  `json:"journalCreatedAt"`
	FirstRecordedAt  *time.Time `json:"firstRecordedAt"`
}
type Source struct {
	Module          Module     `json:"module"`
	Registered      bool       `json:"registered"`
	FirstRecordedAt *time.Time `json:"firstRecordedAt"`
	LastRecordedAt  *time.Time `json:"lastRecordedAt"`
	RecordedCount   int64      `json:"recordedCount"`
}

// These are incomplete cumulative observations, not complete delivery totals or
// process health. A report is persisted only alongside an acknowledged event,
// and its counters are sampled before that event's acknowledgement.
type RecorderObservation struct {
	ProcessID         string    `json:"processId"`
	Module            Module    `json:"module"`
	StartedAt         time.Time `json:"startedAt"`
	ObservedAt        time.Time `json:"observedAt"`
	Accepted          int64     `json:"accepted"`
	Acknowledged      int64     `json:"acknowledged"`
	Rejected          int64     `json:"rejected"`
	QueueFull         int64     `json:"queueFull"`
	WriteFailures     int64     `json:"writeFailures"`
	Unacknowledged    int64     `json:"unacknowledged"`
	Abandoned         int64     `json:"abandoned"`
	AcceptanceStopped bool      `json:"acceptanceStopped"`
}
type RecorderReport struct {
	RecorderObservation
	ReportedAt time.Time `json:"reportedAt"`
	Evidence   string    `json:"evidence"`
}
type Sources struct {
	Modules          []Source         `json:"modules"`
	Coverage         Coverage         `json:"coverage"`
	Reports          []RecorderReport `json:"reports"`
	ReportsTruncated bool             `json:"reportsTruncated"`
}
type Error struct {
	Status int
	Code   string
}

func (e *Error) Error() string              { return e.Code }
func problem(status int, code string) error { return &Error{Status: status, Code: code} }

// AppendError separates a transaction known not to have committed from one
// whose commit response was lost. Unknown implementations are conservatively
// treated as uncertain by Recorder.
type AppendError struct {
	Err       error
	Uncertain bool
}

func (e *AppendError) Error() string { return e.Err.Error() }
func (e *AppendError) Unwrap() error { return e.Err }

type EventStore interface {
	Append(context.Context, Event, RecorderObservation) (Event, error)
}

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func validUUID(s string) bool { return uuidPattern.MatchString(s) }
func newUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", problem(503, "random_unavailable")
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	s := hex.EncodeToString(b[:])
	return s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:], nil
}
func eventSeverity(module Module, code EventCode, outcome Outcome, reason Reason) (Severity, bool) {
	if module != API && module != Worker {
		return "", false
	}
	valid := false
	switch code {
	case ServiceStartRequested:
		valid = outcome == Attempted && reason == NoReason
	case ServiceStopped:
		valid = outcome == Completed && reason == NoReason
	case ServiceFailed:
		valid = outcome == Failed && ((module == API && reason == ServeFailed) || (module == Worker && reason == WorkerFailed))
	case QueueCycle, RecoveryCycle, SchedulerCycle:
		valid = module == Worker && ((outcome == Completed && reason == NoReason) || (outcome == Failed && reason == CycleFailed))
	case QueueProgress:
		valid = module == Worker && outcome == Progress && reason == NoReason
	}
	if !valid {
		return "", false
	}
	if outcome == Failed {
		return ErrorSeverity, true
	}
	return Info, true
}
func validateEvent(e Event, requireRecorded bool) error {
	sev, ok := eventSeverity(e.Module, e.Code, e.Outcome, e.Reason)
	if !ok || e.SchemaVersion != 1 || !validUUID(e.EventID) || !validUUID(e.ProcessID) || e.Severity != sev || e.ObservedAt.IsZero() || e.ObservedAt.Unix() < 0 || e.ObservedAt.Nanosecond()%1000 != 0 {
		return problem(400, "invalid_event")
	}
	if requireRecorded {
		if e.RecordedAt.IsZero() || e.RecordedAt.Unix() < 0 || e.RecordedAt.Nanosecond()%1000 != 0 {
			return problem(400, "invalid_event")
		}
	} else if !e.RecordedAt.IsZero() {
		return problem(400, "invalid_event")
	}
	return nil
}
func validPrincipal(p Principal) bool { return p.TenantID == OwnerTenantID && p.ActorID > 0 }
func terminalAppendError(err error) bool {
	var e *Error
	return errors.As(err, &e) && (e.Code == "invalid_event" || e.Code == "invalid_observation" || e.Code == "event_conflict" || e.Code == "process_conflict")
}
