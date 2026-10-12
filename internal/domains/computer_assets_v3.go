package domains

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/directoryassets"
	"github.com/yunpiao/adtr/internal/ldapconnection"
)

// Computer query deliberately excludes the user-only exact-SAM extension.
type ComputerAssetsV3Filter struct {
	DomainID, ExpectedRevision, ExpectedCredentialRevision string
	ObservationID, Search, StartTm, EndTm, CreatTm         string
	PageIdx, PageSize                                      int
}

func computerAssetsV3BaseFilter(f ComputerAssetsV3Filter) UserAssetsV2Filter {
	return UserAssetsV2Filter{DomainID: f.DomainID, ExpectedRevision: f.ExpectedRevision,
		ExpectedCredentialRevision: f.ExpectedCredentialRevision, ObservationID: f.ObservationID,
		Search: f.Search, StartTm: f.StartTm, EndTm: f.EndTm, CreatTm: f.CreatTm, PageIdx: f.PageIdx, PageSize: f.PageSize}
}
func ValidateComputerAssetsV3Filter(f ComputerAssetsV3Filter) error {
	return ValidateUserAssetsV2Filter(computerAssetsV3BaseFilter(f))
}

// Closed v3 projection; no inferred device identity or source-system aliases.
type ComputerAssetsV3List struct {
	DictionaryVersion     int                              `json:"dictionaryVersion"`
	Selection             UserAssetsV2Selection            `json:"selection"`
	ObservedComputerCount *int                             `json:"observedComputerCount"`
	Available             bool                             `json:"available"`
	ObservationID         string                           `json:"observationId,omitempty"`
	Source                *ldapconnection.DirectorySource  `json:"source,omitempty"`
	List                  []directoryassets.PublicObjectV3 `json:"list"`
	Page                  Page                             `json:"page"`
}

func (s *Store) ComputerAssetsV3ListTx(ctx context.Context, tx pgx.Tx, tenant string, allowed []string, f ComputerAssetsV3Filter) (ComputerAssetsV3List, error) {
	if err := ValidateComputerAssetsV3Filter(f); err != nil {
		return ComputerAssetsV3List{}, err
	}
	selection, err := s.userAssetsV2SelectionTx(ctx, tx, tenant, allowed, f.DomainID, f.ExpectedRevision, f.ExpectedCredentialRevision)
	if err != nil {
		return ComputerAssetsV3List{}, err
	}
	id, observation, err := s.loadDirectoryV3ObservationTx(ctx, tx, tenant, f.DomainID, f.ObservationID)
	defer observation.Discard()
	if err != nil {
		return ComputerAssetsV3List{}, err
	}
	if id == "" {
		out := ComputerAssetsV3List{DictionaryVersion: 3, Selection: selection, List: []directoryassets.PublicObjectV3{}, Page: Page{Index: f.PageIdx, Size: f.PageSize}}
		if err := validateComputerAssetsV3Response(ctx, out); err != nil {
			return ComputerAssetsV3List{}, err
		}
		return out, nil
	}
	return computerAssetsV3Page(ctx, selection, id, observation, f)
}

func computerAssetsV3Page(ctx context.Context, selection UserAssetsV2Selection, id string, o ldapconnection.DirectoryV3Observation, f ComputerAssetsV3Filter) (ComputerAssetsV3List, error) {
	rows := make([]directoryassets.PublicObjectV3, 0)
	observedComputerCount := 0
	search := strings.ToLower(f.Search)
	rangeStart, rangeEnd, err := userAssetsV2TimeRange(computerAssetsV3BaseFilter(f))
	if err != nil {
		return ComputerAssetsV3List{}, err
	}
	for _, stored := range o.Objects {
		if err := ctx.Err(); err != nil {
			return ComputerAssetsV3List{}, err
		}
		if stored.Base.Kind != directoryassets.Computer {
			continue
		}
		observedComputerCount++
		object, err := directoryassets.ProjectV3(stored)
		if err != nil {
			return ComputerAssetsV3List{}, problem(409, "directory_observation_unavailable")
		}
		if !computerAssetsV3Match(object, search) {
			continue
		}
		if f.StartTm != "" {
			if object.WhenCreated == nil {
				continue
			}
			created, err := time.Parse(time.RFC3339, *object.WhenCreated)
			if err != nil {
				return ComputerAssetsV3List{}, problem(409, "directory_observation_unavailable")
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
	out := ComputerAssetsV3List{DictionaryVersion: 3, Selection: selection, Available: true, ObservedComputerCount: &observedComputerCount, ObservationID: id, Source: &o.Source, List: []directoryassets.PublicObjectV3{}, Page: Page{Index: f.PageIdx, Size: f.PageSize, Total: len(rows)}}
	if len(rows) > 0 {
		out.Page.Pages = (len(rows) + f.PageSize - 1) / f.PageSize
	}
	start := (f.PageIdx - 1) * f.PageSize
	if start < len(rows) {
		out.List = append(out.List, rows[start:min(start+f.PageSize, len(rows))]...)
	}
	if err := validateComputerAssetsV3Response(ctx, out); err != nil {
		return ComputerAssetsV3List{}, err
	}
	return out, nil
}

// Search only the declared raw facts; SPN, mail and inferred addresses do not match.
func computerAssetsV3Match(o directoryassets.PublicObjectV3, q string) bool {
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
func validateComputerAssetsV3Response(ctx context.Context, out ComputerAssetsV3List) error {
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
