package domains

import "time"

// Choice deliberately contains only configured connection metadata and the
// current matching historical observation. It is never an execution grant.
type Choice struct {
	DomainID             string         `json:"domainId"`
	Domain               string         `json:"domain"`
	Revision             string         `json:"revision"`
	CredentialRevision   string         `json:"credentialRevision"`
	DCHostName           string         `json:"dcHostName"`
	LDAPAddr             string         `json:"ldapAddr"`
	Port                 string         `json:"port"`
	Mode                 string         `json:"mode"`
	CredentialConfigured bool           `json:"credentialConfigured"`
	ConnectionState      string         `json:"connectionState"`
	Source               string         `json:"source"`
	LastTest             *SelectionTest `json:"lastTest"`
}

type SelectionTest struct {
	ObservedAt time.Time `json:"observedAt"`
	DCHostName string    `json:"dcHostName"`
	Code       string    `json:"code"`
}

type SelectionList struct {
	Page      Page     `json:"page"`
	List      []Choice `json:"List"`
	Exhausted bool     `json:"exhausted"`
}

type SelectionResolution struct {
	Selection Choice    `json:"selection"`
	CheckedAt time.Time `json:"checkedAt"`
}

type SelectionFilter struct {
	PageIdx, PageSize int
	Keyword           string
	ObservationState  string
}

type SelectionInput struct {
	DomainID                   string
	ExpectedRevision           string
	ExpectedCredentialRevision string
}
