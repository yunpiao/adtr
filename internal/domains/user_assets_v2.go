package domains

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/yunpiao/adtr/internal/directoryassets"
	"github.com/yunpiao/adtr/internal/ldapconnection"
)

const UserAssetsV2MaxBytes = DirectoryV2PageMaxBytes

// UserAssetsV2Filter contains raw search text, not a SQL/LDAP expression or a
// display escape language. Source pins are required even for an initial read.
type UserAssetsV2Filter struct {
	DomainID, ExpectedRevision, ExpectedCredentialRevision string
	ObservationID, Search                                  string
	PageIdx, PageSize                                      int
}

type UserAssetV2Input struct {
	DomainID, ExpectedRevision, ExpectedCredentialRevision string
	ObservationID, ObjectGUID                              string
}

// UserAssetsV2Selection deliberately excludes diagnostic/account metadata.
type UserAssetsV2Selection struct {
	DomainID           string `json:"domainId"`
	Revision           string `json:"revision"`
	CredentialRevision string `json:"credentialRevision"`
}

type UserAssetsV2List struct {
	DictionaryVersion int                              `json:"dictionaryVersion"`
	Selection         UserAssetsV2Selection            `json:"selection"`
	Available         bool                             `json:"available"`
	ObservationID     string                           `json:"observationId,omitempty"`
	Source            *ldapconnection.DirectorySource  `json:"source,omitempty"`
	List              []directoryassets.PublicObjectV2 `json:"list"`
	Page              Page                             `json:"page"`
}

type UserAssetV2Detail struct {
	DictionaryVersion int                            `json:"dictionaryVersion"`
	Selection         UserAssetsV2Selection          `json:"selection"`
	ObservationID     string                         `json:"observationId"`
	Source            ldapconnection.DirectorySource `json:"source"`
	Object            directoryassets.PublicObjectV2 `json:"object"`
}

func validateUserAssetsV2Selection(domainID, revision, credentialRevision string) error {
	if !ValidID(domainID) {
		return problem(400, "invalid_input")
	}
	if _, err := Revision(revision); err != nil {
		return err
	}
	_, err := Revision(credentialRevision)
	return err
}

func ValidateUserAssetsV2Filter(f UserAssetsV2Filter) error {
	if err := validateUserAssetsV2Selection(f.DomainID, f.ExpectedRevision, f.ExpectedCredentialRevision); err != nil {
		return err
	}
	if f.ObservationID != "" && !ValidID(f.ObservationID) || f.PageIdx < 1 || f.PageIdx > 10000 || f.PageSize < 10 || f.PageSize > 50 || f.PageSize%10 != 0 || f.PageIdx > 1 && f.ObservationID == "" {
		return problem(400, "invalid_input")
	}
	if !utf8.ValidString(f.Search) || len(f.Search) > 200 {
		return problem(400, "invalid_input")
	}
	units := 0
	for _, r := range f.Search {
		units++
		if r > 0xffff {
			units++
		}
		if units > 50 {
			return problem(400, "invalid_input")
		}
	}
	return nil
}

func ValidateUserAssetV2Input(in UserAssetV2Input) error {
	if err := validateUserAssetsV2Selection(in.DomainID, in.ExpectedRevision, in.ExpectedCredentialRevision); err != nil {
		return err
	}
	if !ValidID(in.ObservationID) || !userAssetV2GUID(in.ObjectGUID) {
		return problem(400, "invalid_input")
	}
	return nil
}

// Match the codec's lower-case canonical GUID text without imposing UUID
// version/variant restrictions that AD's arbitrary 16-byte GUID does not have.
func userAssetV2GUID(v string) bool {
	if len(v) != 36 {
		return false
	}
	for i := range len(v) {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if v[i] != '-' {
				return false
			}
		} else if !(v[i] >= '0' && v[i] <= '9' || v[i] >= 'a' && v[i] <= 'f') {
			return false
		}
	}
	return true
}

// lowerSearch is computed once from validated raw input. Fields are compared
// independently, preserving whitespace, NUL, punctuation and Unicode form.
func userAssetsV2Match(o directoryassets.PublicObjectV2, lowerSearch string) bool {
	if lowerSearch == "" || strings.Contains(strings.ToLower(o.DN), lowerSearch) {
		return true
	}
	for _, value := range []*string{o.SAMAccountName, o.ObjectSID, o.Mail} {
		if value != nil && strings.Contains(strings.ToLower(*value), lowerSearch) {
			return true
		}
	}
	return false
}

// The observation is fully decoded/validated by the shared loader before this
// scan. Filtering precedes count and paging; no older evidence is considered.
func userAssetsV2Page(ctx context.Context, selection UserAssetsV2Selection, id string, o ldapconnection.DirectoryV2Observation, f UserAssetsV2Filter) (UserAssetsV2List, error) {
	rows := make([]directoryassets.PublicObjectV2, 0)
	search := strings.ToLower(f.Search)
	for _, stored := range o.Objects {
		if err := ctx.Err(); err != nil {
			return UserAssetsV2List{}, err
		}
		if stored.Base.Kind != directoryassets.User {
			continue
		}
		object, err := directoryassets.ProjectV2(stored)
		if err != nil {
			return UserAssetsV2List{}, problem(409, "directory_observation_unavailable")
		}
		if userAssetsV2Match(object, search) {
			rows = append(rows, object)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].GUID < rows[j].GUID })
	out := UserAssetsV2List{DictionaryVersion: 2, Selection: selection, Available: true, ObservationID: id, Source: &o.Source, List: []directoryassets.PublicObjectV2{}, Page: Page{Index: f.PageIdx, Size: f.PageSize, Total: len(rows)}}
	if len(rows) > 0 {
		out.Page.Pages = (len(rows) + f.PageSize - 1) / f.PageSize
	}
	start := (f.PageIdx - 1) * f.PageSize
	if start < len(rows) {
		out.List = append(out.List, rows[start:min(start+f.PageSize, len(rows))]...)
	}
	if err := validateUserAssetsV2Response(ctx, out); err != nil {
		return UserAssetsV2List{}, err
	}
	return out, nil
}

// The cap includes the entire success envelope, source and selection, plus the
// HTTP encoder's newline. No partial result can reach the handler on failure.
func validateUserAssetsV2Response(ctx context.Context, out any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	raw, err := json.Marshal(out)
	defer clear(raw)
	if err != nil || len(raw)+1 > UserAssetsV2MaxBytes {
		return problem(422, "directory_limit_exceeded")
	}
	return ctx.Err()
}
