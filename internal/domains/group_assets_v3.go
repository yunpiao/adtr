package domains

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/directoryassets"
	"github.com/yunpiao/adtr/internal/ldapconnection"
)

// Group query excludes exact-SAM and creation-range extensions; only raw search and stable sorting are supported.
type GroupAssetsV3Filter struct {
	DomainID, ExpectedRevision, ExpectedCredentialRevision string
	ObservationID, Search, CreatTm                         string
	PageIdx, PageSize                                      int
}

func groupAssetsV3BaseFilter(f GroupAssetsV3Filter) UserAssetsV2Filter {
	return UserAssetsV2Filter{DomainID: f.DomainID, ExpectedRevision: f.ExpectedRevision,
		ExpectedCredentialRevision: f.ExpectedCredentialRevision, ObservationID: f.ObservationID,
		Search: f.Search, CreatTm: f.CreatTm, PageIdx: f.PageIdx, PageSize: f.PageSize}
}
func ValidateGroupAssetsV3Filter(f GroupAssetsV3Filter) error {
	if err := ValidateUserAssetsV2Filter(groupAssetsV3BaseFilter(f)); err != nil {
		return err
	}
	units := 0
	for _, r := range f.Search {
		units++
		if r > 0xffff {
			units++
		}
	}
	if units > 20 || len(f.Search) > 80 {
		return problem(400, "invalid_input")
	}
	return nil
}

// Closed v3 projection; no inferred group classification or source-system aliases.
type GroupAssetsV3List struct {
	DictionaryVersion  int                              `json:"dictionaryVersion"`
	Selection          UserAssetsV2Selection            `json:"selection"`
	ObservedGroupCount *int                             `json:"observedGroupCount"`
	Available          bool                             `json:"available"`
	ObservationID      string                           `json:"observationId,omitempty"`
	Source             *ldapconnection.DirectorySource  `json:"source,omitempty"`
	List               []directoryassets.PublicObjectV3 `json:"list"`
	Page               Page                             `json:"page"`
}

func (s *Store) GroupAssetsV3ListTx(ctx context.Context, tx pgx.Tx, tenant string, allowed []string, f GroupAssetsV3Filter) (GroupAssetsV3List, error) {
	if err := ValidateGroupAssetsV3Filter(f); err != nil {
		return GroupAssetsV3List{}, err
	}
	selection, err := s.userAssetsV2SelectionTx(ctx, tx, tenant, allowed, f.DomainID, f.ExpectedRevision, f.ExpectedCredentialRevision)
	if err != nil {
		return GroupAssetsV3List{}, err
	}
	id, observation, err := s.loadDirectoryV3ObservationTx(ctx, tx, tenant, f.DomainID, f.ObservationID)
	defer observation.Discard()
	if err != nil {
		return GroupAssetsV3List{}, err
	}
	if id == "" {
		out := GroupAssetsV3List{DictionaryVersion: 3, Selection: selection, List: []directoryassets.PublicObjectV3{}, Page: Page{Index: f.PageIdx, Size: f.PageSize}}
		if err := validateGroupAssetsV3Response(ctx, out); err != nil {
			return GroupAssetsV3List{}, err
		}
		return out, nil
	}
	return groupAssetsV3Page(ctx, selection, id, observation, f)
}

func groupAssetsV3Page(ctx context.Context, selection UserAssetsV2Selection, id string, o ldapconnection.DirectoryV3Observation, f GroupAssetsV3Filter) (GroupAssetsV3List, error) {
	rows := make([]directoryassets.PublicObjectV3, 0)
	observedGroupCount := 0
	search := strings.ToLower(f.Search)
	for _, stored := range o.Objects {
		if err := ctx.Err(); err != nil {
			return GroupAssetsV3List{}, err
		}
		if stored.Base.Kind != directoryassets.Group {
			continue
		}
		observedGroupCount++
		object, err := directoryassets.ProjectV3(stored)
		if err != nil {
			return GroupAssetsV3List{}, problem(409, "directory_observation_unavailable")
		}
		if !groupAssetsV3Match(object, search) {
			continue
		}
		rows = append(rows, object)
	}
	sort.Slice(rows, func(i, j int) bool {
		if f.CreatTm != "" {
			a, b := rows[i].WhenCreated, rows[j].WhenCreated
			if a == nil && b != nil {
				return false
			}
			if a != nil && b == nil {
				return true
			}
			// The v3 supplemental projection retains fixed v2 guarantees: fixed-width UTC whole seconds; text order is
			// chronological, including year 0001 (which is not a missing value).
			if a != nil && b != nil && *a != *b {
				if f.CreatTm == "desc" {
					return *a > *b
				}
				return *a < *b
			}
		}
		return rows[i].GUID < rows[j].GUID
	})
	out := GroupAssetsV3List{DictionaryVersion: 3, Selection: selection, Available: true, ObservedGroupCount: &observedGroupCount, ObservationID: id, Source: &o.Source, List: []directoryassets.PublicObjectV3{}, Page: Page{Index: f.PageIdx, Size: f.PageSize, Total: len(rows)}}
	if len(rows) > 0 {
		out.Page.Pages = (len(rows) + f.PageSize - 1) / f.PageSize
	}
	start := (f.PageIdx - 1) * f.PageSize
	if start < len(rows) {
		out.List = append(out.List, rows[start:min(start+f.PageSize, len(rows))]...)
	}
	if err := validateGroupAssetsV3Response(ctx, out); err != nil {
		return GroupAssetsV3List{}, err
	}
	return out, nil
}

// Search only the declared raw facts; SPN, mail and inferred addresses do not match.
func groupAssetsV3Match(o directoryassets.PublicObjectV3, q string) bool {
	if q == "" || strings.Contains(strings.ToLower(o.DN), q) {
		return true
	}
	for _, v := range []*string{o.SAMAccountName, o.ObjectSID} {
		if v != nil && strings.Contains(strings.ToLower(*v), q) {
			return true
		}
	}
	return false
}
func validateGroupAssetsV3Response(ctx context.Context, out GroupAssetsV3List) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	raw, err := json.Marshal(out)
	defer clear(raw)
	if err != nil || len(raw)+1 > DirectoryV3PageMaxBytes {
		return problem(422, "directory_limit_exceeded")
	}
	return ctx.Err()
}
