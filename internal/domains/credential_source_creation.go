package domains

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/tasks"
)

func unconfiguredFingerprint(p tasks.Principal, in Input) string {
	raw, _ := json.Marshal(struct {
		Purpose, Tenant           string
		Actor                     int64
		Domain, DC, IP, Port, Key string
	}{"domain-create-unconfigured-v1", p.TenantID, p.ActorID, in.Domain, in.DCHostName, in.LDAPAddr, in.Port, in.IdempotencyKey})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// CreateUnconfiguredTx uses the original creator-bound receipt namespace and
// capacity lock. It requires the same enrollment authority as CreateTx, no key.
func (s *Store) CreateUnconfiguredTx(ctx context.Context, tx pgx.Tx, p tasks.Principal, in Input, now time.Time) (Mutation, error) {
	if err := ValidateInput("/create-unconfigured", &in); err != nil {
		return Mutation{}, err
	}
	fingerprint := unconfiguredFingerprint(p, in)
	var old, operation string
	err := tx.QueryRow(ctx, `SELECT fingerprint,creation_operation FROM adtr.domain_creation_receipts WHERE tenant_id=$1 AND actor_id=$2 AND idempotency_key=$3`, p.TenantID, p.ActorID, in.IdempotencyKey).Scan(&old, &operation)
	if err == nil {
		if operation != "unconfigured" || subtle.ConstantTimeCompare([]byte(old), []byte(fingerprint)) != 1 {
			return Mutation{}, problem(409, "idempotency_conflict")
		}
		receipt, err := s.ReceiptTx(ctx, tx, p, in.IdempotencyKey)
		return Mutation{Result: "SUCCESS", DomainID: receipt.DomainID, Revision: receipt.Revision, RequiresResourceAssignment: true, Replayed: true}, err
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Mutation{}, err
	}
	max, count, err := EnrollmentEligible(ctx, tx, p.TenantID, now)
	if err != nil {
		return Mutation{}, err
	}
	if count >= max {
		return Mutation{}, problem(409, "domain_capacity_exceeded")
	}
	var exists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT FROM adtr.domain_connections WHERE tenant_id=$1 AND canonical_domain=$2 AND deleted_at IS NULL)`, p.TenantID, in.Domain).Scan(&exists); err != nil {
		return Mutation{}, err
	}
	if exists {
		return Mutation{}, problem(409, "domain_conflict")
	}
	id, err := randomID()
	if err != nil {
		return Mutation{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO adtr.resource_domains(tenant_id,id,name,active) VALUES($1,$2,$3,true)`, p.TenantID, id, in.Domain); err != nil {
		return Mutation{}, err
	}
	mode := "starttls"
	if in.Port == "636" {
		mode = "ldaps"
	}
	if _, err = tx.Exec(ctx, `INSERT INTO adtr.domain_connections(tenant_id,domain_id,canonical_domain,dc_hostname,dial_ip,port,transport_mode,credential_mode) VALUES($1,$2,$3,$4,$5,$6,$7,'unconfigured')`, p.TenantID, id, in.Domain, in.DCHostName, in.LDAPAddr, in.Port, mode); err != nil {
		return Mutation{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO adtr.domain_creation_receipts(tenant_id,actor_id,idempotency_key,fingerprint,domain_id,connection_revision,creation_operation) VALUES($1,$2,$3,$4,$5,1,'unconfigured')`, p.TenantID, p.ActorID, in.IdempotencyKey, fingerprint, id); err != nil {
		return Mutation{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO adtr.domain_audit(tenant_id,actor_id,domain_id,action,old_revision,new_revision,credential_revision,result,credential_source) VALUES($1,$2,$3,'domain_create',0,1,1,'saved','unconfigured')`, p.TenantID, p.ActorID, id); err != nil {
		return Mutation{}, err
	}
	return Mutation{Result: "SUCCESS", DomainID: id, Revision: "1", RequiresResourceAssignment: true}, nil
}
