package domains

import (
	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/directoryassets"
	"github.com/yunpiao/adtr/internal/ldapconnection"
)

type UserAssetV3Detail struct {
	DictionaryVersion int                            `json:"dictionaryVersion"`
	Selection         UserAssetsV2Selection          `json:"selection"`
	ObservationID     string                         `json:"observationId"`
	Source            ldapconnection.DirectorySource `json:"source"`
	Object            directoryassets.PublicObjectV3 `json:"object"`
}

func validateUserAssetV3Response(ctx context.Context, out UserAssetV3Detail) error {
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
func (s *Store) UserAssetV3DetailTx(ctx context.Context, tx pgx.Tx, tenant string, allowed []string, in UserAssetV2Input) (UserAssetV3Detail, error) {
	if err := ValidateUserAssetV2Input(in); err != nil {
		return UserAssetV3Detail{}, err
	}
	selection, err := s.userAssetsV2SelectionTx(ctx, tx, tenant, allowed, in.DomainID, in.ExpectedRevision, in.ExpectedCredentialRevision)
	if err != nil {
		return UserAssetV3Detail{}, err
	}
	id, observation, err := s.loadDirectoryV3ObservationTx(ctx, tx, tenant, in.DomainID, in.ObservationID)
	defer observation.Discard()
	if err != nil {
		return UserAssetV3Detail{}, err
	}
	for _, stored := range observation.Objects {
		if err := ctx.Err(); err != nil {
			return UserAssetV3Detail{}, err
		}
		if stored.Base.Kind != directoryassets.User || stored.Base.GUID != in.ObjectGUID {
			continue
		}
		object, err := directoryassets.ProjectV3(stored)
		if err != nil {
			return UserAssetV3Detail{}, problem(409, "directory_observation_unavailable")
		}
		out := UserAssetV3Detail{DictionaryVersion: 3, Selection: selection, ObservationID: id, Source: observation.Source, Object: object}
		if err := validateUserAssetV3Response(ctx, out); err != nil {
			return UserAssetV3Detail{}, err
		}
		return out, nil
	}
	if err := ctx.Err(); err != nil {
		return UserAssetV3Detail{}, err
	}
	return UserAssetV3Detail{}, problem(404, "not_found")
}
