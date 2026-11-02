package simulationrulecatalog

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/detectionrules"
	"github.com/yunpiao/adtr/internal/simulationdetection"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Keep this boundary aligned with the verified existing mapped() v1; do not
// extend the old mapper to make catalog fields appear available.
func unsupportedCopyFields(rule detectionrules.Rule) []string {
	seen := map[string]bool{}
	for _, c := range rule.Conditions {
		switch c.Field {
		case "event.category", "event.outcome", "actor.name", "target.name", "source.ip":
		default:
			seen[c.Field] = true
		}
	}
	out := []string{}
	for field := range seen {
		out = append(out, field)
	}
	sort.Strings(out)
	return out
}
func mapDetectionError(e error) error {
	switch {
	case errors.Is(e, simulationdetection.ErrInvalid):
		return ErrInvalid
	case errors.Is(e, simulationdetection.ErrDenied):
		return ErrDenied
	case errors.Is(e, simulationdetection.ErrNotFound):
		return ErrNotFound
	case errors.Is(e, simulationdetection.ErrConflict):
		return ErrConflict
	case errors.Is(e, simulationdetection.ErrLimit):
		return ErrLimit
	}
	return e
}
func validName(s string) bool {
	if len(s) == 0 || len(s) > 128 || !utf8.ValidString(s) || strings.TrimSpace(s) != s {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// CopyTemplateTx creates an independent NEW template. Catalog controls never
// cascade into existing copies/tasks. The caller must explain and acknowledge it.
func CopyTemplateTx(ctx context.Context, tx pgx.Tx, s Scope, in CopyInput, audit AuditFunc) (Receipt, error) {
	empty := Receipt{}
	if e := authorize(tx, s, true); e != nil {
		return empty, e
	}
	if !s.DetectionRead || !s.DetectionManage {
		return empty, ErrDenied
	}
	if audit == nil || !identifier.MatchString(in.ID) || !identifier.MatchString(in.OperationID) || !identifier.MatchString(in.TemplateID) || !validName(in.Name) || in.Acknowledgement != CopyAcknowledgement || in.ExpectedRevision == 0 || in.PublishedVersion == 0 || len(in.PublishedDigest) != 64 {
		return empty, ErrInvalid
	}
	if e := lock(ctx, tx, s); e != nil {
		return empty, e
	}
	intent := Intent{DomainID: s.Domain, ID: in.ID, Action: "copy-template", OperationID: in.OperationID, ExpectedRevision: in.ExpectedRevision, PublishedVersion: in.PublishedVersion, PublishedDigest: in.PublishedDigest, TemplateID: in.TemplateID, Name: in.Name, Acknowledgement: in.Acknowledgement}
	h := digest(intent)
	prior, ph, e := receipt(ctx, tx, s, in.OperationID)
	if e == nil {
		if ph != h || prior.Action != "copy-template" {
			return empty, ErrConflict
		}
		return prior, nil
	}
	if !errors.Is(e, ErrNotFound) {
		return empty, e
	}
	record, e := DetailTx(ctx, tx, s, in.ID)
	if e != nil {
		return empty, e
	}
	if record.Revision != in.ExpectedRevision {
		return empty, ErrConflict
	}
	entry := record.Snapshot.Entries[0]
	if entry.Archived || entry.ActiveVersion == 0 || entry.ActiveVersion != in.PublishedVersion {
		return empty, ErrTransition
	}
	var source *detectionrules.Rule
	for _, p := range entry.Published {
		if p.Rule.Version == entry.ActiveVersion {
			if p.Digest != in.PublishedDigest {
				return empty, ErrConflict
			}
			r := p.Rule
			source = &r
			break
		}
	}
	if source == nil {
		return empty, ErrInvalid
	}
	if len(unsupportedCopyFields(*source)) != 0 {
		return empty, ErrUnsupportedCopy
	}
	// Private copy only: the published body remains Enabled=false with its original
	// digest. Target versions use the EXISTING template version convention.
	rule := *source
	rule.Enabled = true
	rule.Version = 1
	expected := simulationdetection.Template{ID: in.TemplateID, Name: in.Name, Version: 1, Rules: []detectionrules.Rule{rule}, Simulation: true}
	expected.Hash = digest(expected)
	origin := Origin{RuleID: in.ID, PublishedVersion: source.Version, PublishedDigest: in.PublishedDigest, CatalogRevision: record.Revision, TemplateID: expected.ID, TemplateVersion: expected.Version, TemplateHash: expected.Hash, Acknowledgement: in.Acknowledgement}
	result := Receipt{Intent: intent, RequestHash: h, Simulation: true, OperationID: in.OperationID, Action: "copy-template", ActorID: s.ActorID, At: time.Now().UTC(), Template: &expected, Origin: &origin}
	if _, e = marshalBounded(expected, MaxRecordBytes); e != nil {
		return empty, e
	}
	if _, e = marshalBounded(result, MaxResponseBytes); e != nil {
		return empty, e
	}
	ob, e := marshalBounded(origin, 4096)
	if e != nil {
		return empty, e
	}
	ds := simulationdetection.Scope{Tenant: s.Tenant, Domain: s.Domain, ActorID: s.ActorID, Read: s.DetectionRead, Manage: s.DetectionManage}
	// Match the existing module lock before checking NEWness, preserving the only
	// cross-module order catalog→detection and preventing a concurrent target create.
	_, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "simulation-detection:"+digest([]string{s.Tenant, s.Domain}))
	if e != nil {
		return empty, e
	}
	_, e = simulationdetection.DetailTx(ctx, tx, ds, in.TemplateID)
	if e == nil {
		return empty, ErrConflict
	}
	if !errors.Is(e, simulationdetection.ErrNotFound) {
		return empty, mapDetectionError(e)
	}
	target, e := simulationdetection.MutateTx(ctx, tx, ds, simulationdetection.Mutation{ID: in.TemplateID, Name: in.Name, OperationID: "catalog-copy-" + digest([]any{s.ActorID, in.OperationID}), Rules: expected.Rules}, func(ctx context.Context, tx pgx.Tx, _ simulationdetection.Audit) error { return audit(ctx, tx, result) })
	if e != nil {
		return empty, mapDetectionError(e)
	}
	if target.Hash != expected.Hash {
		return empty, ErrInvalid
	}
	_, e = tx.Exec(ctx, `INSERT INTO adtr.simulation_rule_catalog_origins VALUES($1,$2,$3,$4,$5,$6,$7)`, s.Tenant, s.Domain, target.ID, target.Version, in.ID, in.PublishedVersion, ob)
	if e != nil {
		return empty, e
	}
	// The target callback above already audited the complete source+target receipt.
	e = saveReceipt(ctx, tx, s, h, result, func(context.Context, pgx.Tx, Receipt) error { return nil })
	if e != nil {
		return empty, e
	}
	return result, nil
}
