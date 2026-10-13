package domains

import (
	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/directoryassets"
	"github.com/yunpiao/adtr/internal/ldapconnection"
)

type GroupMembersV4Input UserAssetV2Input

type GroupMembersV4 struct {
	DictionaryVersion int                            `json:"dictionaryVersion"`
	Selection         UserAssetsV2Selection          `json:"selection"`
	ObservationID     string                         `json:"observationId"`
	Source            ldapconnection.DirectorySource `json:"source"`
	Object            directoryassets.PublicObjectV4 `json:"object"`
}

func validateGroupMembersV4Response(ctx context.Context, out any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	raw, err := json.Marshal(out)
	defer clear(raw)
	if err != nil || len(raw)+1 > DirectoryV4PageMaxBytes {
		return problem(422, "directory_limit_exceeded")
	}
	return ctx.Err()
}
func (s *Store) GroupMembersV4Tx(ctx context.Context, tx pgx.Tx, tenant string, allowed []string, in GroupMembersV4Input) (GroupMembersV4, error) {
	if err := ValidateUserAssetV2Input(UserAssetV2Input(in)); err != nil {
		return GroupMembersV4{}, err
	}
	selection, err := s.userAssetsV2SelectionTx(ctx, tx, tenant, allowed, in.DomainID, in.ExpectedRevision, in.ExpectedCredentialRevision)
	if err != nil {
		return GroupMembersV4{}, err
	}
	id, observation, err := s.loadDirectoryV4ObservationTx(ctx, tx, tenant, in.DomainID, in.ObservationID)
	defer observation.Discard()
	if err != nil {
		return GroupMembersV4{}, err
	}
	for _, stored := range observation.Objects {
		if err := ctx.Err(); err != nil {
			return GroupMembersV4{}, err
		}
		if stored.Base.Kind != directoryassets.Group || stored.Base.GUID != in.ObjectGUID {
			continue
		}
		object, err := directoryassets.ProjectV4(stored)
		if err != nil {
			return GroupMembersV4{}, problem(409, "directory_observation_unavailable")
		}
		out := GroupMembersV4{DictionaryVersion: 4, Selection: selection, ObservationID: id, Source: observation.Source, Object: object}
		if err := validateGroupMembersV4Response(ctx, out); err != nil {
			return GroupMembersV4{}, err
		}
		return out, nil
	}
	if err := ctx.Err(); err != nil {
		return GroupMembersV4{}, err
	}
	return GroupMembersV4{}, problem(404, "not_found")
}
