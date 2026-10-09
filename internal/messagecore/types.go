// Package messagecore implements bounded, immutable message query snapshots and
// identity-bound, pure read-state transitions. It performs no authorization,
// persistence, I/O, or clock reads.
package messagecore

import "time"

// ScopeKind distinguishes platform messages from messages belonging to a domain.
type ScopeKind string

const (
	ScopePlatform ScopeKind = "platform"
	ScopeDomain   ScopeKind = "domain"
)

// Scope is an already-authorized input contract, not proof of permission.
type Scope struct {
	TenantID        string
	UserID          string
	DomainIDs       []string
	IncludePlatform bool
}

// Message contains only the caller's sanitized projection. Both timestamps are
// required and independent; IngestedAt need not follow OccurredAt.
type Message struct {
	ID          string
	TenantID    string
	ScopeKind   ScopeKind
	DomainID    string
	Type        string
	SubType     string
	Title       string
	Description string
	OccurredAt  time.Time
	IngestedAt  time.Time
}

// TypeSubType is a fully qualified subtype. Names are opaque and case-sensitive.
type TypeSubType struct {
	Type    string
	SubType string
}

// Catalog explicitly enumerates supported type/subtype pairs. It must be
// nonempty, with nonempty names and no repeated pairs. A subtype name may occur
// under multiple types; the complete pair is always its identity.
type Catalog []TypeSubType

type Sort string

const (
	SortAscending  Sort = "asc"
	SortDescending Sort = "desc"
)

type ReadFilter string

const (
	ReadAll    ReadFilter = "all"
	ReadUnread ReadFilter = "unread"
	ReadRead   ReadFilter = "read"
)

// Query is frozen by NewResult. Zero time endpoints mean both are omitted;
// omitting exactly one endpoint is invalid. Empty Types means all catalog types;
// nonempty SubTypes further restricts that selection to exact pairs.
type Query struct {
	Types      []string
	SubTypes   []TypeSubType
	Keyword    string
	Start      time.Time
	End        time.Time
	ReadFilter ReadFilter
	Sort       Sort
	Offset     int
	Limit      int
}

// Limits are required, explicit resource guards, not product capacity promises.
// MaxInputBytes uses the deterministic accounting documented in the contract.
type Limits struct {
	MaxPageSize      int
	MaxInputMessages int
	MaxInputBytes    int64
	MaxMarkTargets   int
}

// ReadState belongs to exactly one tenant/user. FirstReadAt contains the first
// recorded read instant by message ID, including IDs outside a given result.
type ReadState struct {
	TenantID    string
	UserID      string
	FirstReadAt map[string]time.Time
}

// Request supplies all inputs, including the caller's explicit confirmation
// instant. The default range is [ConfirmedAt-168h, ConfirmedAt), in UTC.
type Request struct {
	Scope       Scope
	Messages    []Message
	Catalog     Catalog
	ReadState   ReadState
	Query       Query
	ConfirmedAt time.Time
	Limits      Limits
}

// Entry is a message plus its query-time read snapshot. Unread entries have a
// zero FirstReadAt; Read and FirstReadAt are never recomputed during pagination.
type Entry struct {
	Message     Message
	Read        bool
	FirstReadAt time.Time
}

// Counts ignores only Query.ReadFilter, and always satisfies All=Unread+Read.
type Counts struct {
	All    int
	Unread int
	Read   int
}

// Page.Total is the read-filtered count before pagination, not len(Items).
type Page struct {
	Items  []Entry
	Total  int
	Counts Counts
	Offset int
	Limit  int
}

// Location uses a zero-based Index and one-based Page. A miss is explicitly
// Found=false, Index=-1, Page=0.
type Location struct {
	Found bool
	Index int
	Page  int
}

// Progress describes only one successful pure transition, not committed work.
type Progress struct {
	Target      int
	Processed   int
	NewlyRead   int
	AlreadyRead int
	Failed      int
	Remaining   int
}

type MarkResult struct {
	State    ReadState
	Progress Progress
}

// Code is safe to expose without leaking submitted identifiers or message text.
type Code string

const (
	InvalidScope      Code = "invalid_scope"
	InvalidMessage    Code = "invalid_message"
	InvalidFilter     Code = "invalid_filter"
	InvalidPagination Code = "invalid_pagination"
	InvalidCatalog    Code = "invalid_catalog"
	InvalidPolicy     Code = "invalid_policy"
	BudgetExceeded    Code = "budget_exceeded"
	InvalidReadState  Code = "invalid_read_state"
	InvalidTarget     Code = "invalid_target"
	TargetNotInResult Code = "target_not_in_result"
	InvalidResult     Code = "invalid_result"
)

// Error contains a stable code and a static field name only. It deliberately
// does not retain or format any caller-supplied value.
type Error struct {
	Code  Code
	Field string
}

func (e *Error) Error() string { return "messagecore: " + string(e.Code) + ": " + e.Field }

func invalid(code Code, field string) error { return &Error{Code: code, Field: field} }

func utc(t time.Time) time.Time { return t.Round(0).UTC() }

func cloneReadState(s ReadState) ReadState {
	r := ReadState{TenantID: s.TenantID, UserID: s.UserID, FirstReadAt: make(map[string]time.Time, len(s.FirstReadAt))}
	for id, at := range s.FirstReadAt {
		r.FirstReadAt[id] = utc(at)
	}
	return r
}

func cloneScope(s Scope) Scope {
	s.DomainIDs = append(make([]string, 0, len(s.DomainIDs)), s.DomainIDs...)
	return s
}

func cloneQuery(q Query) Query {
	q.Types = append(make([]string, 0, len(q.Types)), q.Types...)
	q.SubTypes = append(make([]TypeSubType, 0, len(q.SubTypes)), q.SubTypes...)
	return q
}
