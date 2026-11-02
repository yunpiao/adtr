package simulationdetectionexceptions

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/detectionexceptions"
	"github.com/yunpiao/adtr/internal/tasks"
	"time"
)

func reserve(s detectionexceptions.Snapshot) uint64 {
	var n uint64
	for _, e := range s.Entries {
		if !e.Archived {
			n++
			if e.Enabled {
				n++
			}
		}
	}
	return n
}
func transition(s Scope, old Record, in Mutation, entries []detectionexceptions.Exception) (Record, error) {
	out := old
	out.SourceTasks = map[string]string{}
	for k, v := range old.SourceTasks {
		out.SourceTasks[k] = v
	}
	a := access(s)
	var e error
	switch in.Action {
	case "create":
		if len(entries) != 1 || in.Declaration == nil || in.ID != "" || len(in.Entries) != 0 {
			return out, ErrInvalid
		}
		out.Snapshot, e = detectionexceptions.Add(a, old.Snapshot, in.ExpectedRevision, entries[0])
		if !entries[0].Global {
			out.SourceTasks[entries[0].ID] = in.Declaration.SourceTaskID
		}
	case "edit":
		if len(entries) != 1 || in.Declaration == nil || in.ID != "" || len(in.Entries) != 0 {
			return out, ErrInvalid
		}
		out.Snapshot, e = detectionexceptions.Edit(a, old.Snapshot, in.ExpectedRevision, entries[0])
		delete(out.SourceTasks, entries[0].ID)
		if !entries[0].Global {
			out.SourceTasks[entries[0].ID] = in.Declaration.SourceTaskID
		}
	case "import":
		if in.Declaration != nil || in.ID != "" || len(in.Entries) != len(entries) {
			return out, ErrInvalid
		}
		out.Snapshot, e = detectionexceptions.Import(a, old.Snapshot, in.ExpectedRevision, entries)
		for i, v := range entries {
			if !v.Global {
				out.SourceTasks[v.ID] = in.Entries[i].SourceTaskID
			}
		}
	case "enable", "disable", "archive":
		if in.Declaration != nil || len(in.Entries) != 0 || !identifier.MatchString(in.ID) {
			return out, ErrInvalid
		}
		if in.Action == "archive" {
			out.Snapshot, e = detectionexceptions.Archive(a, old.Snapshot, in.ExpectedRevision, in.ID)
		} else {
			found := false
			for _, v := range old.Snapshot.Entries {
				if v.ID == in.ID {
					found = true
					if v.Enabled == (in.Action == "enable") {
						return out, ErrConflict
					}
					v.Enabled = in.Action == "enable"
					out.Snapshot, e = detectionexceptions.Edit(a, old.Snapshot, in.ExpectedRevision, v)
					break
				}
			}
			if !found {
				return out, ErrNotFound
			}
		}
	default:
		return out, ErrInvalid
	}
	if e != nil {
		return out, mapCore(e)
	}
	if len(out.Snapshot.Entries) > MaxEntries || out.Snapshot.Revision > MaxRevisions {
		return out, ErrLimit
	}
	// Reserve one archive and, when enabled, one disable revision per current entry.
	// Safe controls consume only reserved slots; ordinary edits cannot spend them.
	if in.Action != "disable" && in.Action != "archive" && out.Snapshot.Revision+reserve(out.Snapshot) > MaxRevisions {
		return out, ErrLimit
	}
	return out, nil
}
func MutateTx(ctx context.Context, tx pgx.Tx, s Scope, engine *tasks.Engine, in Mutation, audit AuditFunc) (Receipt, error) {
	var receiptOut Receipt
	if e := authorize(tx, s, true); e != nil {
		return receiptOut, e
	}
	if audit == nil || !identifier.MatchString(in.OperationID) || in.ExpectedRevision < 1 || in.ExpectedRevision > MaxRevisions {
		return receiptOut, ErrInvalid
	}
	intent := Intent{DomainID: s.Domain, Action: in.Action, OperationID: in.OperationID, ExpectedRevision: in.ExpectedRevision, ID: in.ID, Declaration: in.Declaration, Entries: in.Entries}
	if _, e := marshalBounded(intent, MaxRecordBytes); e != nil {
		return receiptOut, e
	}
	if e := lock(ctx, tx, s); e != nil {
		return receiptOut, e
	}
	h := digest(intent)
	existing, e := receipt(ctx, tx, s, in.OperationID)
	if e == nil {
		if existing.RequestHash != h {
			return receiptOut, ErrConflict
		}
		return ReceiptTx(ctx, tx, s, engine, in.OperationID)
	}
	if !errors.Is(e, ErrNotFound) {
		return receiptOut, e
	}
	old, e := SnapshotTx(ctx, tx, s)
	if e != nil {
		return receiptOut, e
	}
	if old.Snapshot.Revision != in.ExpectedRevision {
		return receiptOut, ErrConflict
	}
	entries := []detectionexceptions.Exception{}
	if in.Declaration != nil {
		v, e := declaration(ctx, tx, s, engine, *in.Declaration)
		if e != nil {
			return receiptOut, e
		}
		entries = append(entries, v)
	}
	if len(in.Entries) > MaxEntries {
		return receiptOut, ErrLimit
	}
	for _, d := range in.Entries {
		v, e := declaration(ctx, tx, s, engine, d)
		if e != nil {
			return receiptOut, e
		}
		entries = append(entries, v)
	}
	next, e := transition(s, old, in, entries)
	if e != nil {
		return receiptOut, e
	}
	now := time.Now().UTC()
	next.ActorID = s.ActorID
	next.Action = in.Action
	next.At = now
	next.Hash = ""
	next.Hash = digest(next)
	receiptOut = Receipt{Contract: Contract, Simulation: true, OperationID: in.OperationID, Action: in.Action, ActorID: s.ActorID, At: now, Intent: intent, RequestHash: h, Revision: next.Snapshot.Revision, SnapshotHash: next.Hash}
	if _, e = marshalBounded(next, MaxRecordBytes); e != nil {
		return Receipt{}, e
	}
	if _, e = marshalBounded(receiptOut, MaxResponseBytes); e != nil {
		return Receipt{}, e
	}
	// Persist the initial revision only after all validation and output preflight.
	if old.ActorID == 0 {
		var exists bool
		if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM adtr.simulation_exception_heads WHERE tenant_id=$1 AND domain_id=$2)`, s.Tenant, s.Domain).Scan(&exists); e != nil {
			return Receipt{}, e
		}
		if !exists {
			if e = persist(ctx, tx, s, old); e != nil {
				return Receipt{}, e
			}
		}
	}
	if e = persist(ctx, tx, s, next); e != nil {
		return Receipt{}, e
	}
	if e = saveReceipt(ctx, tx, s, receiptOut, audit); e != nil {
		return Receipt{}, e
	}
	return receiptOut, nil
}
