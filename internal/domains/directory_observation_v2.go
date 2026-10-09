package domains

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/ldapconnection"
	"github.com/yunpiao/adtr/internal/tasks"
)

// Only the lossless stored envelope crosses the SQL guard. Raw supplemental
// text, including NUL, is base64 inside that envelope, never public JSONB text.
// The caller holds current authority inside the original executor's fence.
func publishDirectoryObservationV2Tx(ctx context.Context, tx pgx.Tx, t tasks.Task, p directoryPinnedPayload, observation ldapconnection.DirectoryV2Observation) error {
	pins, err := directoryUseTaskPayloadForProfile(directoryTaskV2, t)
	if err != nil || pins != p {
		return errDirectoryUseEvidence
	}
	raw, err := encodeDirectoryObservationV2(observation)
	defer clear(raw)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(raw)
	_, err = tx.Exec(ctx, `INSERT INTO adtr.domain_directory_observations(tenant_id,domain_id,task_id,actor_id,opener_owner,opener_fencing_token,opener_attempt,object_count,body,sha256,dictionary_version)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,2)`, t.TenantID, t.DomainID, t.ID, t.ActorID, t.LeaseOwner, t.FencingToken, t.Attempt, len(observation.Objects), raw, hex.EncodeToString(digest[:]))
	if err != nil {
		return err
	}
	revision, _ := Revision(p.ConnectionRevision)
	generation, _ := Revision(p.ConnectionCredentialGeneration)
	return audit(ctx, tx, tasks.Principal{TenantID: t.TenantID, ActorID: t.ActorID}, t.DomainID, "domain_directory_v2_result", revision, revision, generation, t.ID, "success")
}
