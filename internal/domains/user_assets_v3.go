package domains

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/directoryassets"
	"github.com/yunpiao/adtr/internal/ldapconnection"
)

// Version-specific projection; v2 DTOs and codec acceptance remain unchanged.
type UserAssetsV3List struct {
	DictionaryVersion int                              `json:"dictionaryVersion"`
	Selection         UserAssetsV2Selection            `json:"selection"`
	Available         bool                             `json:"available"`
	ObservationID     string                           `json:"observationId,omitempty"`
	Source            *ldapconnection.DirectorySource  `json:"source,omitempty"`
	List              []directoryassets.PublicObjectV3 `json:"list"`
	Page              Page                             `json:"page"`
}

func (s *Store) UserAssetsV3ListTx(ctx context.Context, tx pgx.Tx, tenant string, allowed []string, f UserAssetsV2Filter) (UserAssetsV3List, error) {
	if err := ValidateUserAssetsV2Filter(f); err != nil {
		return UserAssetsV3List{}, err
	}
	selection, err := s.userAssetsV2SelectionTx(ctx, tx, tenant, allowed, f.DomainID, f.ExpectedRevision, f.ExpectedCredentialRevision)
	if err != nil {
		return UserAssetsV3List{}, err
	}
	id, observation, err := s.loadDirectoryV3ObservationTx(ctx, tx, tenant, f.DomainID, f.ObservationID)
	defer observation.Discard()
	if err != nil {
		return UserAssetsV3List{}, err
	}
	if id == "" {
		out := UserAssetsV3List{DictionaryVersion: 3, Selection: selection, List: []directoryassets.PublicObjectV3{}, Page: Page{Index: f.PageIdx, Size: f.PageSize}}
		if err := validateUserAssetV3Response(ctx, out); err != nil {
			return UserAssetsV3List{}, err
		}
		return out, nil
	}
	return userAssetsV3Page(ctx, selection, id, observation, f)
}

func userAssetsV3Page(ctx context.Context, selection UserAssetsV2Selection, id string, o ldapconnection.DirectoryV3Observation, f UserAssetsV2Filter) (UserAssetsV3List, error) {
	rows, err := userAssetsV3Rows(ctx, o, f)
	if err != nil {
		return UserAssetsV3List{}, err
	}
	out := UserAssetsV3List{DictionaryVersion: 3, Selection: selection, Available: true, ObservationID: id, Source: &o.Source, List: []directoryassets.PublicObjectV3{}, Page: Page{Index: f.PageIdx, Size: f.PageSize, Total: len(rows)}}
	if len(rows) > 0 {
		out.Page.Pages = (len(rows) + f.PageSize - 1) / f.PageSize
	}
	start := (f.PageIdx - 1) * f.PageSize
	if start < len(rows) {
		out.List = append(out.List, rows[start:min(start+f.PageSize, len(rows))]...)
	}
	if err := validateUserAssetV3Response(ctx, out); err != nil {
		return UserAssetsV3List{}, err
	}
	return out, nil
}

// One predicate and ordering implementation for pages and whole-observation exports.
// The caller has already validated every stored object, including excluded ones.
func userAssetsV3Rows(ctx context.Context, o ldapconnection.DirectoryV3Observation, f UserAssetsV2Filter) ([]directoryassets.PublicObjectV3, error) {
	rows := make([]directoryassets.PublicObjectV3, 0)
	search := strings.ToLower(f.Search)
	rangeStart, rangeEnd, err := userAssetsV2TimeRange(f)
	if err != nil {
		return nil, err
	}
	for _, stored := range o.Objects {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if stored.Base.Kind != directoryassets.User {
			continue
		}
		object, err := directoryassets.ProjectV3(stored)
		if err != nil {
			return nil, problem(409, "directory_observation_unavailable")
		}
		if !userAssetsV2Match(object.PublicObjectV2, search) || f.SAMAccountName != "" && (object.SAMAccountName == nil || *object.SAMAccountName != f.SAMAccountName) {
			continue
		}
		if f.StartTm != "" {
			if object.WhenCreated == nil {
				continue
			}
			created, err := time.Parse(time.RFC3339, *object.WhenCreated)
			if err != nil {
				return nil, problem(409, "directory_observation_unavailable")
			}
			if created.Before(rangeStart) || !created.Before(rangeEnd) {
				continue
			}
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
	return rows, nil
}
