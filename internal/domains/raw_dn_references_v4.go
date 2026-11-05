package domains

import (
	"bytes"
	"context"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/directoryassets"
	"github.com/yunpiao/adtr/internal/ldapconnection"
)

type RawDNReferencesV4Input struct {
	UserAssetV2Input
	PageIdx, PageSize int
}

type RawDNReferencesV4Coverage struct {
	GroupCount                  int `json:"groupCount"`
	MemberReturnedGroupCount    int `json:"memberReturnedGroupCount"`
	MemberNotReturnedGroupCount int `json:"memberNotReturnedGroupCount"`
}

type RawDNReferencesV4 struct {
	DictionaryVersion int                              `json:"dictionaryVersion"`
	Selection         UserAssetsV2Selection            `json:"selection"`
	ObservationID     string                           `json:"observationId"`
	Source            ldapconnection.DirectorySource   `json:"source"`
	Object            directoryassets.PublicObjectV4   `json:"object"`
	List              []directoryassets.PublicObjectV4 `json:"list"`
	Page              Page                             `json:"page"`
	Coverage          RawDNReferencesV4Coverage        `json:"coverage"`
}

func ValidateRawDNReferencesV4Input(in RawDNReferencesV4Input) error {
	if err := ValidateUserAssetV2Input(in.UserAssetV2Input); err != nil {
		return err
	}
	if in.PageIdx < 1 || in.PageIdx > 10000 || (in.PageSize != 25 && in.PageSize != 50 && in.PageSize != 100) {
		return problem(400, "invalid_input")
	}
	return nil
}

// The caller must supply a complete observation validated by the immutable
// loader. This is a byte-exact text lookup, never a resolved membership graph.
func rawDNReferencesV4Page(ctx context.Context, selection UserAssetsV2Selection, id string, o ldapconnection.DirectoryV4Observation, in RawDNReferencesV4Input) (RawDNReferencesV4, error) {
	if err := ValidateRawDNReferencesV4Input(in); err != nil {
		return RawDNReferencesV4{}, err
	}
	target := -1
	for i := range o.Objects {
		if err := ctx.Err(); err != nil {
			return RawDNReferencesV4{}, err
		}
		if o.Objects[i].Base.GUID == in.ObjectGUID {
			target = i
		}
	}
	if target < 0 {
		return RawDNReferencesV4{}, problem(404, "not_found")
	}
	object, err := directoryassets.ProjectV4(o.Objects[target])
	if err != nil {
		return RawDNReferencesV4{}, problem(409, "directory_observation_unavailable")
	}
	out := RawDNReferencesV4{DictionaryVersion: 4, Selection: selection, ObservationID: id, Source: o.Source, Object: object, List: []directoryassets.PublicObjectV4{}, Page: Page{Index: in.PageIdx, Size: in.PageSize}}
	dn := []byte(o.Objects[target].Base.DN)
	rows := make([]int, 0)
	for i, stored := range o.Objects {
		if err := ctx.Err(); err != nil {
			return RawDNReferencesV4{}, err
		}
		if stored.Base.Kind != directoryassets.Group {
			continue
		}
		out.Coverage.GroupCount++
		if stored.Membership.MemberBytes == nil {
			out.Coverage.MemberNotReturnedGroupCount++
			continue
		}
		out.Coverage.MemberReturnedGroupCount++
		for _, member := range stored.Membership.MemberBytes {
			if err := ctx.Err(); err != nil {
				return RawDNReferencesV4{}, err
			}
			if bytes.Equal(member, dn) {
				rows = append(rows, i)
				break
			}
		}
	}
	sort.Slice(rows, func(i, j int) bool { return o.Objects[rows[i]].Base.GUID < o.Objects[rows[j]].Base.GUID })
	out.Page.Total = len(rows)
	out.Page.Pages = (len(rows) + in.PageSize - 1) / in.PageSize
	start := (in.PageIdx - 1) * in.PageSize
	if start < len(rows) {
		for _, index := range rows[start:min(start+in.PageSize, len(rows))] {
			if err := ctx.Err(); err != nil {
				return RawDNReferencesV4{}, err
			}
			row, err := directoryassets.ProjectV4(o.Objects[index])
			if err != nil {
				return RawDNReferencesV4{}, problem(409, "directory_observation_unavailable")
			}
			out.List = append(out.List, row)
		}
	}
	if err := validateGroupMembersV4Response(ctx, out); err != nil {
		return RawDNReferencesV4{}, err
	}
	return out, nil
}

func (s *Store) RawDNReferencesV4Tx(ctx context.Context, tx pgx.Tx, tenant string, allowed []string, in RawDNReferencesV4Input) (RawDNReferencesV4, error) {
	if err := ValidateRawDNReferencesV4Input(in); err != nil {
		return RawDNReferencesV4{}, err
	}
	selection, err := s.userAssetsV2SelectionTx(ctx, tx, tenant, allowed, in.DomainID, in.ExpectedRevision, in.ExpectedCredentialRevision)
	if err != nil {
		return RawDNReferencesV4{}, err
	}
	id, observation, err := s.loadDirectoryV4ObservationTx(ctx, tx, tenant, in.DomainID, in.ObservationID)
	defer observation.Discard()
	if err != nil {
		return RawDNReferencesV4{}, err
	}
	return rawDNReferencesV4Page(ctx, selection, id, observation, in)
}
