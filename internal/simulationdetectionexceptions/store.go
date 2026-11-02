package simulationdetectionexceptions

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/detectionexceptions"
	"github.com/yunpiao/adtr/internal/detectionrules"
	"github.com/yunpiao/adtr/internal/tasks"
	"regexp"
)

var identifier = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)

func digest(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func authorize(tx pgx.Tx, s Scope, write bool) error {
	if tx == nil || !identifier.MatchString(s.Tenant) || !identifier.MatchString(s.Domain) || s.ActorID <= 0 || !s.Read || (write && !s.Manage) {
		return ErrDenied
	}
	return nil
}
func coreScope(s Scope) detectionrules.Scope {
	return detectionrules.Scope{Tenant: s.Tenant, Domain: s.Domain}
}
func access(s Scope) detectionexceptions.Access {
	return detectionexceptions.Access{Scope: coreScope(s), Read: s.Read, Write: s.Manage}
}
func lock(ctx context.Context, tx pgx.Tx, s Scope) error {
	_, e := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "simulation-exceptions:"+digest([]string{s.Tenant, s.Domain}))
	return e
}
func mapCore(e error) error {
	switch {
	case errors.Is(e, detectionexceptions.ErrConflict):
		return ErrConflict
	case errors.Is(e, detectionexceptions.ErrNotFound):
		return ErrNotFound
	case errors.Is(e, detectionexceptions.ErrLimit), errors.Is(e, detectionrules.ErrLimit):
		return ErrLimit
	case errors.Is(e, detectionexceptions.ErrInvalid), errors.Is(e, detectionexceptions.ErrScope), errors.Is(e, detectionrules.ErrInvalid), errors.Is(e, detectionrules.ErrScope):
		return ErrInvalid
	case errors.Is(e, detectionexceptions.ErrPermission):
		return ErrDenied
	}
	return e
}
func marshalBounded(v any, limit int) ([]byte, error) {
	b, e := json.Marshal(v)
	if e != nil {
		return nil, ErrInvalid
	}
	size := len(b)
	quoted, escaped := false, false
	for _, c := range b {
		if quoted {
			if escaped {
				escaped = false
				continue
			}
			if c == '\\' {
				escaped = true
			} else if c == '"' {
				quoted = false
			}
			continue
		}
		if c == '"' {
			quoted = true
		} else if c == ':' || c == ',' {
			size++
		}
	}
	if size > limit {
		return nil, ErrLimit
	}
	return b, nil
}
func decode(b []byte, limit int, v any) error {
	if len(b) > limit {
		return ErrLimit
	}
	if json.Unmarshal(b, v) != nil {
		return ErrInvalid
	}
	return nil
}
func empty(s Scope) Record {
	snap, _ := detectionexceptions.New(coreScope(s))
	r := Record{Contract: Contract, Simulation: true, Snapshot: snap, SourceTasks: map[string]string{}, Action: "initial"}
	r.Hash = digest(r)
	return r
}
func validateRecord(s Scope, r Record) error {
	h := r.Hash
	r.Hash = ""
	if h != digest(r) || r.Contract != Contract || !r.Simulation || r.Snapshot.Revision > MaxRevisions || len(r.Snapshot.Entries) > MaxEntries || r.SourceTasks == nil {
		return ErrInvalid
	}
	if _, e := detectionexceptions.List(access(s), r.Snapshot); e != nil {
		return mapCore(e)
	}
	n := 0
	for _, v := range r.Snapshot.Entries {
		if !v.Global {
			n++
			if !identifier.MatchString(r.SourceTasks[v.ID]) {
				return ErrInvalid
			}
		}
		if e := conditions(v.Conditions); e != nil {
			return e
		}
	}
	if len(r.SourceTasks) != n {
		return ErrInvalid
	}
	return nil
}
func SnapshotTx(ctx context.Context, tx pgx.Tx, s Scope) (Record, error) {
	if e := authorize(tx, s, false); e != nil {
		return Record{}, e
	}
	var rev uint64
	e := tx.QueryRow(ctx, `SELECT revision FROM adtr.simulation_exception_heads WHERE tenant_id=$1 AND domain_id=$2`, s.Tenant, s.Domain).Scan(&rev)
	if errors.Is(e, pgx.ErrNoRows) {
		return empty(s), nil
	}
	if e != nil {
		return Record{}, e
	}
	return HistoryTx(ctx, tx, s, rev)
}
func HistoryTx(ctx context.Context, tx pgx.Tx, s Scope, revision uint64) (Record, error) {
	var r Record
	if e := authorize(tx, s, false); e != nil {
		return r, e
	}
	if revision < 1 || revision > MaxRevisions {
		return r, ErrInvalid
	}
	var b []byte
	var h string
	e := tx.QueryRow(ctx, `SELECT body,hash FROM adtr.simulation_exception_revisions WHERE tenant_id=$1 AND domain_id=$2 AND revision=$3`, s.Tenant, s.Domain, revision).Scan(&b, &h)
	if errors.Is(e, pgx.ErrNoRows) {
		return r, ErrNotFound
	}
	if e != nil {
		return r, e
	}
	if e = decode(b, MaxRecordBytes, &r); e != nil {
		return r, e
	}
	if r.Hash != h || r.Snapshot.Revision != revision {
		return r, ErrInvalid
	}
	return r, validateRecord(s, r)
}

// Export contains explicit domain-shared configuration only, never source events or task results.
func ExportTx(ctx context.Context, tx pgx.Tx, s Scope) (Record, error) {
	r, e := SnapshotTx(ctx, tx, s)
	if e == nil {
		_, e = detectionexceptions.Export(access(s), r.Snapshot)
	}
	return r, e
}
func receipt(ctx context.Context, tx pgx.Tx, s Scope, op string) (Receipt, error) {
	var r Receipt
	var b []byte
	var h string
	e := tx.QueryRow(ctx, `SELECT body,request_hash FROM adtr.simulation_exception_receipts WHERE tenant_id=$1 AND domain_id=$2 AND actor_id=$3 AND operation_id=$4`, s.Tenant, s.Domain, s.ActorID, op).Scan(&b, &h)
	if errors.Is(e, pgx.ErrNoRows) {
		return r, ErrNotFound
	}
	if e != nil {
		return r, e
	}
	if e = decode(b, MaxResponseBytes, &r); e != nil {
		return r, e
	}
	if !r.Simulation || r.Contract != Contract || r.ActorID != s.ActorID || r.OperationID != op || r.Intent.OperationID != op || r.Intent.DomainID != s.Domain || r.Intent.Action != r.Action || r.RequestHash != h || digest(r.Intent) != h {
		return r, ErrInvalid
	}
	return r, nil
}
func ReceiptTx(ctx context.Context, tx pgx.Tx, s Scope, engine *tasks.Engine, op string) (Receipt, error) {
	if e := authorize(tx, s, false); e != nil {
		return Receipt{}, e
	}
	if !identifier.MatchString(op) {
		return Receipt{}, ErrInvalid
	}
	r, e := receipt(ctx, tx, s, op)
	if e != nil {
		return r, e
	}
	if r.Evaluation != nil {
		ev, e := EvaluationTx(ctx, tx, s, engine, r.Evaluation.ID)
		if e != nil {
			return Receipt{}, e
		}
		if digest(ev) != digest(*r.Evaluation) {
			return Receipt{}, ErrInvalid
		}
	}
	return r, nil
}
func saveReceipt(ctx context.Context, tx pgx.Tx, s Scope, r Receipt, audit AuditFunc) error {
	b, e := marshalBounded(r, MaxResponseBytes)
	if e != nil {
		return e
	}
	_, e = tx.Exec(ctx, `INSERT INTO adtr.simulation_exception_receipts VALUES($1,$2,$3,$4,$5,$6)`, s.Tenant, s.Domain, s.ActorID, r.OperationID, r.RequestHash, b)
	if e != nil {
		return e
	}
	return audit(ctx, tx, r)
}
func persist(ctx context.Context, tx pgx.Tx, s Scope, r Record) error {
	b, e := marshalBounded(r, MaxRecordBytes)
	if e != nil {
		return e
	}
	if r.Snapshot.Revision == 1 {
		_, e = tx.Exec(ctx, `INSERT INTO adtr.simulation_exception_heads VALUES($1,$2,1)`, s.Tenant, s.Domain)
	} else {
		_, e = tx.Exec(ctx, `UPDATE adtr.simulation_exception_heads SET revision=$3 WHERE tenant_id=$1 AND domain_id=$2 AND revision=$3-1`, s.Tenant, s.Domain, r.Snapshot.Revision)
	}
	if e != nil {
		return e
	}
	_, e = tx.Exec(ctx, `INSERT INTO adtr.simulation_exception_revisions VALUES($1,$2,$3,$4,$5)`, s.Tenant, s.Domain, r.Snapshot.Revision, r.Hash, b)
	return e
}
