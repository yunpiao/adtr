// Package assetassessment evaluates explicitly synthetic asset evidence using a
// versioned, provisional contract. It performs no IO, authorization or AD reads.
package assetassessment

import (
	"errors"
	"strings"
	"time"
)

const (
	// Version identifies assumptions, not a production policy or an AD schema.
	Version          = "simulated-asset-v1"
	SyntheticSource  = "synthetic"
	FreshnessWindow  = 24 * time.Hour
	InactivityWindow = 30 * 24 * time.Hour
)

var ErrInvalidInput = errors.New("invalid simulated asset assessment input")

type Kind string

const (
	User     Kind = "user"
	Computer Kind = "computer"
	Group    Kind = "group"
)

type LoginState string

const (
	LoginUnknown  LoginState = "unknown"
	LoginObserved LoginState = "observed"
	LoginNever    LoginState = "never"
)

// AccountEvidence deliberately requires independently observed flags. A nil
// flag means unavailable, not false. Never is an explicit source assertion;
// a missing timestamp alone cannot establish that an account never logged in.
type AccountEvidence struct {
	Disabled  *bool      `json:"disabled"`
	Locked    *bool      `json:"locked"`
	Login     LoginState `json:"login"`
	LastLogin *time.Time `json:"lastLogin"`
}

type GroupEvidence struct {
	SecurityEnabled *bool `json:"securityEnabled"`
	// Scope is unknown, global, domain_local or universal; empty means unknown.
	Scope string `json:"scope"`
}

// Input is one immutable observation, not a request for live asset status.
// TenantID/DomainID/ObjectID bind provenance only; callers must authorize scope
// before reading any evidence. No authorization can be inferred from this API.
type Input struct {
	Version       string           `json:"version"`
	Source        string           `json:"source"`
	TenantID      string           `json:"tenantId"`
	DomainID      string           `json:"domainId"`
	ObjectID      string           `json:"objectId"`
	ObservationID string           `json:"observationId"`
	Kind          Kind             `json:"kind"`
	ObservedAt    time.Time        `json:"observedAt"`
	AsOf          time.Time        `json:"asOf"`
	Account       *AccountEvidence `json:"account"`
	Group         *GroupEvidence   `json:"group"`
}

// Finding has a stable machine-readable reason for unknown/stale values.
// Value is never omitted and unknown never means safe, healthy or false.
type Finding struct {
	Value  string `json:"value"`
	Reason string `json:"reason"`
}

type Result struct {
	Version       string    `json:"version"`
	Source        string    `json:"source"`
	TenantID      string    `json:"tenantId"`
	DomainID      string    `json:"domainId"`
	ObjectID      string    `json:"objectId"`
	ObservationID string    `json:"observationId"`
	Kind          Kind      `json:"kind"`
	ObservedAt    time.Time `json:"observedAt"`
	AsOf          time.Time `json:"asOf"`
	AccountStatus Finding   `json:"accountStatus"`
	Activity      Finding   `json:"activity"`
	GroupType     Finding   `json:"groupType"`
	GroupScope    Finding   `json:"groupScope"`
}

// Evaluate is deterministic and does not mutate input. All timestamps are
// returned in UTC. Invalid/inconsistent evidence fails atomically with no result.
func Evaluate(in Input) (Result, error) {
	if !valid(in) {
		return Result{}, ErrInvalidInput
	}
	r := Result{Version: Version, Source: SyntheticSource, TenantID: in.TenantID,
		DomainID: in.DomainID, ObjectID: in.ObjectID, ObservationID: in.ObservationID,
		Kind: in.Kind, ObservedAt: in.ObservedAt.UTC(), AsOf: in.AsOf.UTC(),
		AccountStatus: Finding{"not_applicable", "asset_kind"}, Activity: Finding{"not_applicable", "asset_kind"},
		GroupType: Finding{"not_applicable", "asset_kind"}, GroupScope: Finding{"not_applicable", "asset_kind"}}
	unknown := Finding{"unknown", "missing_evidence"}
	stale := in.AsOf.Sub(in.ObservedAt) > FreshnessWindow
	if stale {
		unknown.Reason = "stale_observation"
	}
	if in.Kind == Group {
		r.GroupType, r.GroupScope = unknown, unknown
		if stale || in.Group == nil {
			return r, nil
		}
		if in.Group.SecurityEnabled != nil {
			r.GroupType = Finding{"distribution", "explicit_security_flag"}
			if *in.Group.SecurityEnabled {
				r.GroupType.Value = "security"
			}
		}
		if in.Group.Scope != "" && in.Group.Scope != "unknown" {
			r.GroupScope = Finding{in.Group.Scope, "explicit_scope"}
		}
		return r, nil
	}
	r.AccountStatus, r.Activity = unknown, unknown
	if stale || in.Account == nil {
		return r, nil
	}
	a := in.Account
	// Disabled wins only when explicitly known. A locked observation with an
	// unknown disabled flag remains unknown because precedence is unresolved.
	if a.Disabled != nil && *a.Disabled {
		r.AccountStatus = Finding{"disabled", "explicit_disabled_flag"}
	} else if a.Disabled != nil && a.Locked != nil {
		r.AccountStatus = Finding{"enabled", "explicit_flags_clear"}
		if *a.Locked {
			r.AccountStatus = Finding{"locked", "explicit_locked_flag"}
		}
	}
	switch a.Login {
	case LoginNever:
		r.Activity = Finding{"never_observed", "explicit_never_assertion"}
	case LoginObserved:
		r.Activity = Finding{"recent", "last_login_within_window"}
		if in.AsOf.Sub(*a.LastLogin) >= InactivityWindow {
			r.Activity = Finding{"inactive", "last_login_at_or_before_boundary"}
		}
	}
	return r, nil
}

func valid(in Input) bool {
	if in.Version != Version || in.Source != SyntheticSource || !identifier(in.TenantID) || !identifier(in.DomainID) || !identifier(in.ObjectID) || !identifier(in.ObservationID) {
		return false
	}
	if in.ObservedAt.IsZero() || in.AsOf.IsZero() || in.ObservedAt.After(in.AsOf) {
		return false
	}
	switch in.Kind {
	case User, Computer:
		if in.Group != nil {
			return false
		}
	case Group:
		if in.Account != nil {
			return false
		}
	default:
		return false
	}
	if a := in.Account; a != nil {
		switch a.Login {
		case "", LoginUnknown, LoginNever:
			if a.LastLogin != nil {
				return false
			}
		case LoginObserved:
			if a.LastLogin == nil || a.LastLogin.IsZero() || a.LastLogin.After(in.ObservedAt) {
				return false
			}
		default:
			return false
		}
	}
	if g := in.Group; g != nil {
		switch g.Scope {
		case "", "unknown", "global", "domain_local", "universal":
		default:
			return false
		}
	}
	return true
}

func identifier(s string) bool {
	if len(s) == 0 || len(s) > 256 || strings.TrimSpace(s) != s {
		return false
	}
	for _, r := range s {
		if r < 32 || r == 127 {
			return false
		}
	}
	return true
}
