package simulationrulecatalog

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/detectionrules"
	"github.com/yunpiao/adtr/internal/rulecatalog"
	"strconv"
	"time"
)

func validMutation(in Mutation) bool {
	if !identifier.MatchString(in.ID) || !identifier.MatchString(in.OperationID) {
		return false
	}
	switch in.Action {
	case "create":
		return in.ExpectedRevision == 0 && in.Declaration != nil && len(in.Cases) == 0
	case "edit":
		return in.ExpectedRevision > 0 && in.Declaration != nil && len(in.Cases) == 0
	case "test":
		return in.ExpectedRevision > 0 && in.Declaration == nil && len(in.Cases) >= 2 && len(in.Cases) <= MaxCases
	case "publish", "enable", "disable", "archive":
		return in.ExpectedRevision > 0 && in.Declaration == nil && len(in.Cases) == 0
	}
	return false
}
func transition(ctx context.Context, s Scope, in Mutation, old Record, at time.Time) (Record, *Publication, error) {
	r := old
	if old.Revision >= MaxRevisions-2 && in.Action == "disable" && old.ActiveVersion == 0 {
		return r, nil, ErrTransition
	}
	// Reserve the final two revisions for disabling and then archival. Ordinary
	// edits/tests/publish/enable must not consume the ability to stop future copies.
	if old.Revision >= MaxRevisions || (old.Revision >= MaxRevisions-1 && in.Action != "archive") || (old.Revision >= MaxRevisions-2 && in.Action != "disable" && in.Action != "archive") {
		return r, nil, ErrLimit
	}
	var publication *Publication
	var snap rulecatalog.Snapshot
	expected := in.ExpectedRevision
	if in.Action == "create" {
		init, e := rulecatalog.Execute(ctx, rulecatalog.Request{Scope: coreScope(s), Action: "init"})
		if e != nil {
			return r, nil, e
		}
		snap = init.Snapshot
		expected = snap.Revision
	} else {
		snap = old.Snapshot
	}
	req := rulecatalog.Request{Scope: coreScope(s), Snapshot: &snap, Action: in.Action, ID: in.ID, ExpectedRevision: expected}
	if in.Declaration != nil {
		v := uint64(1)
		if in.Action == "edit" {
			entry := snap.Entries[0]
			if entry.Draft != nil {
				v = entry.Draft.Version + 1
			} else {
				v = entry.Published[len(entry.Published)-1].Rule.Version + 1
			}
		}
		req.Rule = &detectionrules.Rule{Contract: detectionrules.ContractVersion, Provenance: detectionrules.Provenance, ID: in.ID, Version: v, Scope: coreScope(s), Enabled: false, Combine: in.Declaration.Combine, Conditions: in.Declaration.Conditions}
	}
	for _, c := range in.Cases {
		req.Cases = append(req.Cases, rulecatalog.TestCase{Event: detectionrules.Event{ID: c.ID, Fields: c.Fields, Scope: coreScope(s), Provenance: detectionrules.Provenance}, WantMatch: c.WantMatch})
	}
	if in.Action == "publish" {
		if !s.Publish || old.LastEditorID == s.ActorID || old.LastEditorID <= 0 || old.TestedBy <= 0 || old.TestRevision == 0 {
			return r, nil, ErrDenied
		}
		req.Action = "publish-simulation"
		req.Reviewer = strconv.FormatInt(s.ActorID, 10)
	}
	out, e := rulecatalog.Execute(ctx, req)
	if e != nil {
		return r, nil, mapCore(e)
	}
	if out.Snapshot.Revision > MaxRevisions {
		return r, nil, ErrLimit
	}
	entry := out.Snapshot.Entries[0]
	r = Record{Summary: Summary{ID: in.ID, Revision: out.Snapshot.Revision, ActiveVersion: entry.ActiveVersion, Archived: entry.Archived, LastEditorID: old.LastEditorID, UpdatedAt: at}, Contract: Contract, Simulation: true, Snapshot: out.Snapshot, ActorID: s.ActorID, Action: in.Action, TestedBy: old.TestedBy, TestRevision: old.TestRevision, CaseResults: old.CaseResults}
	if entry.Draft != nil {
		r.DraftVersion = entry.Draft.Version
	}
	if entry.Test != nil {
		passed := entry.Test.Passed
		r.TestPassed = &passed
	}
	if len(entry.Published) > 0 {
		r.LatestPublishedVersion = entry.Published[len(entry.Published)-1].Rule.Version
	}
	if in.Action == "create" || in.Action == "edit" {
		r.LastEditorID = s.ActorID
		r.TestedBy = 0
		r.TestRevision = 0
		r.CaseResults = nil
	}
	if in.Action == "test" {
		r.TestedBy = s.ActorID
		r.TestRevision = r.Revision
		rule := *entry.Draft
		rule.Enabled = true
		program, err := detectionrules.Compile(coreScope(s), []detectionrules.Rule{rule})
		if err != nil {
			return Record{}, nil, mapCore(err)
		}
		events := make([]detectionrules.Event, len(req.Cases))
		for i, c := range req.Cases {
			events[i] = c.Event
		}
		matches, err := program.Evaluate(ctx, events)
		if err != nil {
			return Record{}, nil, mapCore(err)
		}
		matched := map[string]bool{}
		for _, m := range matches {
			matched[m.EventID] = true
		}
		r.CaseResults = make([]CaseResult, len(req.Cases))
		for i, c := range req.Cases {
			r.CaseResults[i] = CaseResult{ID: c.Event.ID, WantMatch: c.WantMatch, ActualMatch: matched[c.Event.ID]}
		}
	}
	if in.Action == "publish" {
		publication = &Publication{Rule: entry.Published[len(entry.Published)-1], ReviewedBy: s.ActorID, LastEditorID: old.LastEditorID, TestedBy: old.TestedBy, TestRevision: old.TestRevision, PublishedAt: at, CaseResults: old.CaseResults}
		r.TestedBy = 0
		r.TestRevision = 0
		r.CaseResults = nil
	}
	r.CopyUnsupportedFields = []string{}
	for _, p := range entry.Published {
		if p.Rule.Version == entry.ActiveVersion {
			r.CopyUnsupportedFields = unsupportedCopyFields(p.Rule)
			r.CopySupported = !entry.Archived && len(r.CopyUnsupportedFields) == 0
		}
	}
	r.Hash = digest(r)
	return r, publication, nil
}
func MutateTx(ctx context.Context, tx pgx.Tx, s Scope, in Mutation, audit AuditFunc) (Receipt, error) {
	empty := Receipt{}
	if e := authorize(tx, s, in.Action != "publish"); e != nil {
		return empty, e
	}
	if audit == nil || !validMutation(in) {
		return empty, ErrInvalid
	}
	if in.Action == "publish" && !s.Publish {
		return empty, ErrDenied
	}
	if _, e := marshalBounded(in, MaxRecordBytes); e != nil {
		return empty, e
	}
	if e := lock(ctx, tx, s); e != nil {
		return empty, e
	}
	intent := Intent{DomainID: s.Domain, ID: in.ID, Action: in.Action, OperationID: in.OperationID, ExpectedRevision: in.ExpectedRevision, Declaration: in.Declaration, Cases: in.Cases}
	h := digest(intent)
	replay, prior, e := receipt(ctx, tx, s, in.OperationID)
	if e == nil {
		if h != prior || replay.Action != in.Action {
			return empty, ErrConflict
		}
		return replay, nil
	}
	if !errors.Is(e, ErrNotFound) {
		return empty, e
	}
	old, e := DetailTx(ctx, tx, s, in.ID)
	if in.Action == "create" {
		if e == nil {
			return empty, ErrConflict
		}
		if !errors.Is(e, ErrNotFound) {
			return empty, e
		}
		var count int
		if e = tx.QueryRow(ctx, `SELECT count(*) FROM adtr.simulation_rule_catalog_heads WHERE tenant_id=$1 AND domain_id=$2`, s.Tenant, s.Domain).Scan(&count); e != nil {
			return empty, e
		}
		if count >= MaxRules {
			return empty, ErrLimit
		}
	} else {
		if e != nil {
			return empty, e
		}
		if old.Revision != in.ExpectedRevision {
			return empty, ErrConflict
		}
	}
	at := time.Now().UTC()
	record, pub, e := transition(ctx, s, in, old, at)
	if e != nil {
		return empty, e
	}
	result := Receipt{Intent: intent, RequestHash: h, Simulation: true, OperationID: in.OperationID, Action: in.Action, ActorID: s.ActorID, At: at, Rule: &record.Summary}
	// Preflight every persisted/read response envelope BEFORE any business DML.
	body, e := marshalBounded(record, MaxRecordBytes)
	if e != nil {
		return empty, e
	}
	summary, e := marshalBounded(record.Summary, 4096)
	if e != nil {
		return empty, e
	}
	if _, e = marshalBounded(result, MaxResponseBytes); e != nil {
		return empty, e
	}
	var pubBody []byte
	if pub != nil {
		pubBody, e = marshalBounded(pub, MaxRecordBytes)
		if e != nil {
			return empty, e
		}
	}
	// Export carries additional authenticated metadata. Bound the entire potential
	// history before accepting a publication, not only each individual record.
	if pub != nil {
		exported, e := ExportTx(ctx, tx, s, in.ID)
		if e != nil {
			return empty, e
		}
		exported.Publications = append(exported.Publications, *pub)
		if _, e = marshalBounded(exported, MaxResponseBytes); e != nil {
			return empty, e
		}
	}
	if in.Action == "create" {
		_, e = tx.Exec(ctx, `INSERT INTO adtr.simulation_rule_catalog_heads VALUES($1,$2,$3,$4,$5)`, s.Tenant, s.Domain, in.ID, record.Revision, summary)
	} else {
		var tag interface{ RowsAffected() int64 }
		tag, e = tx.Exec(ctx, `UPDATE adtr.simulation_rule_catalog_heads SET revision=$4,summary=$5 WHERE tenant_id=$1 AND domain_id=$2 AND rule_id=$3 AND revision=$6`, s.Tenant, s.Domain, in.ID, record.Revision, summary, in.ExpectedRevision)
		if e == nil && tag.RowsAffected() != 1 {
			e = ErrConflict
		}
	}
	if e != nil {
		return empty, e
	}
	_, e = tx.Exec(ctx, `INSERT INTO adtr.simulation_rule_catalog_revisions VALUES($1,$2,$3,$4,$5,$6)`, s.Tenant, s.Domain, in.ID, record.Revision, record.Hash, body)
	if e != nil {
		return empty, e
	}
	if pub != nil {
		_, e = tx.Exec(ctx, `INSERT INTO adtr.simulation_rule_catalog_publications VALUES($1,$2,$3,$4,$5,$6,$7)`, s.Tenant, s.Domain, in.ID, pub.Rule.Rule.Version, pub.Rule.Digest, pub.TestRevision, pubBody)
		if e != nil {
			return empty, e
		}
	}
	if e = saveReceipt(ctx, tx, s, h, result, audit); e != nil {
		return empty, e
	}
	return result, nil
}
