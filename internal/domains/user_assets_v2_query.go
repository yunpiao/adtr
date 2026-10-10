package domains

import (
	"context"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/directoryassets"
)

// Both user-asset reads require the caller's exact schema gate, live reader
// authorization and schema/tenant/actor locks for this same transaction. The
// current source selection is checked before any observation lookup so a lost
// scope cannot reveal a revision conflict or object existence.
func (s *Store) userAssetsV2SelectionTx(ctx context.Context, tx pgx.Tx, tenant string, allowed []string, domainID, revision, credentialRevision string) (UserAssetsV2Selection, error) {
	if !slices.Contains(allowed, domainID) {
		return UserAssetsV2Selection{}, problem(404, "not_found")
	}
	choice, err := s.SelectionResolveTx(ctx, tx, tenant, allowed, SelectionInput{DomainID: domainID, ExpectedRevision: revision, ExpectedCredentialRevision: credentialRevision})
	if err != nil {
		return UserAssetsV2Selection{}, err
	}
	return UserAssetsV2Selection{DomainID: choice.DomainID, Revision: choice.Revision, CredentialRevision: choice.CredentialRevision}, nil
}

func (s *Store) UserAssetsV2ListTx(ctx context.Context, tx pgx.Tx, tenant string, allowed []string, f UserAssetsV2Filter) (UserAssetsV2List, error) {
	if err := ValidateUserAssetsV2Filter(f); err != nil {
		return UserAssetsV2List{}, err
	}
	selection, err := s.userAssetsV2SelectionTx(ctx, tx, tenant, allowed, f.DomainID, f.ExpectedRevision, f.ExpectedCredentialRevision)
	if err != nil {
		return UserAssetsV2List{}, err
	}
	id, observation, err := s.loadDirectoryV2ObservationTx(ctx, tx, tenant, f.DomainID, f.ObservationID)
	defer observation.Discard()
	if err != nil {
		return UserAssetsV2List{}, err
	}
	if id == "" {
		out := UserAssetsV2List{DictionaryVersion: 2, Selection: selection, List: []directoryassets.PublicObjectV2{}, Page: Page{Index: f.PageIdx, Size: f.PageSize}}
		if err := validateUserAssetsV2Response(ctx, out); err != nil {
			return UserAssetsV2List{}, err
		}
		return out, nil
	}
	return userAssetsV2Page(ctx, selection, id, observation, f)
}

func (s *Store) UserAssetV2DetailTx(ctx context.Context, tx pgx.Tx, tenant string, allowed []string, in UserAssetV2Input) (UserAssetV2Detail, error) {
	if err := ValidateUserAssetV2Input(in); err != nil {
		return UserAssetV2Detail{}, err
	}
	selection, err := s.userAssetsV2SelectionTx(ctx, tx, tenant, allowed, in.DomainID, in.ExpectedRevision, in.ExpectedCredentialRevision)
	if err != nil {
		return UserAssetV2Detail{}, err
	}
	id, observation, err := s.loadDirectoryV2ObservationTx(ctx, tx, tenant, in.DomainID, in.ObservationID)
	defer observation.Discard()
	if err != nil {
		return UserAssetV2Detail{}, err
	}
	for _, stored := range observation.Objects {
		if err := ctx.Err(); err != nil {
			return UserAssetV2Detail{}, err
		}
		if stored.Base.Kind != directoryassets.User || stored.Base.GUID != in.ObjectGUID {
			continue
		}
		object, err := directoryassets.ProjectV2(stored)
		if err != nil {
			return UserAssetV2Detail{}, problem(409, "directory_observation_unavailable")
		}
		out := UserAssetV2Detail{DictionaryVersion: 2, Selection: selection, ObservationID: id, Source: observation.Source, Object: object}
		if err := validateUserAssetsV2Response(ctx, out); err != nil {
			return UserAssetV2Detail{}, err
		}
		return out, nil
	}
	if err := ctx.Err(); err != nil {
		return UserAssetV2Detail{}, err
	}
	return UserAssetV2Detail{}, problem(404, "not_found")
}
