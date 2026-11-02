package auth

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/dbadmission"
	"github.com/yunpiao/adtr/internal/detectionrules"
	"github.com/yunpiao/adtr/internal/schemaversion"
	catalog "github.com/yunpiao/adtr/internal/simulationrulecatalog"
	"github.com/yunpiao/adtr/internal/tasks"
	"io"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const simulationRuleCatalogPath = "/api/simulation/rule-catalog"

var simulationRuleCatalogRouteOrder = []string{"/list", "/detail", "/export", "/receipt", "/create", "/edit", "/test", "/publish", "/enable", "/disable", "/archive", "/create-template"}
var simulationRuleCatalogRoutes = map[string]taskRoute{
	"/list":            {"GET", "domainId query offset limit", false},
	"/detail":          {"GET", "domainId id", false},
	"/export":          {"GET", "domainId id", false},
	"/receipt":         {"GET", "domainId operationId", false},
	"/create":          {"POST", "domainId id expectedRevision operationId declaration", true},
	"/edit":            {"POST", "domainId id expectedRevision operationId declaration", true},
	"/test":            {"POST", "domainId id expectedRevision operationId cases", true},
	"/publish":         {"POST", "domainId id expectedRevision operationId", true},
	"/enable":          {"POST", "domainId id expectedRevision operationId", true},
	"/disable":         {"POST", "domainId id expectedRevision operationId", true},
	"/archive":         {"POST", "domainId id expectedRevision operationId", true},
	"/create-template": {"POST", "domainId id expectedRevision operationId publishedVersion publishedDigest templateId name acknowledgement", true},
}

func simulationRuleCatalogPathAllowed(method, path string, grants map[string]AccessAuth) bool {
	suffix, ok := strings.CutPrefix(path, simulationRuleCatalogPath)
	rt, known := simulationRuleCatalogRoutes[suffix]
	g := grants["simulation_rule_catalog"]
	if !ok || !known || method != rt.method || !g.Readable {
		return false
	}
	if suffix == "/publish" {
		p := grants["simulation_rule_catalog_publish"]
		return p.Readable && p.Writeable
	}
	if rt.write && !g.Writeable {
		return false
	}
	if suffix == "/create-template" {
		d := grants["simulation_detection"]
		return d.Readable && d.Writeable
	}
	return true
}

type simulationRuleCatalogInput struct {
	DomainID         string               `json:"domainId"`
	ID               string               `json:"id"`
	Query            string               `json:"query"`
	Offset           int                  `json:"offset"`
	Limit            int                  `json:"limit"`
	OperationID      string               `json:"operationId"`
	ExpectedRevision uint64               `json:"expectedRevision"`
	Declaration      *catalog.Declaration `json:"declaration"`
	Cases            []catalog.Fixture    `json:"cases"`
	PublishedVersion uint64               `json:"publishedVersion"`
	PublishedDigest  string               `json:"publishedDigest"`
	TemplateID       string               `json:"templateId"`
	Name             string               `json:"name"`
	Acknowledgement  string               `json:"acknowledgement"`
}

func decodeSimulationRuleCatalogInput(w http.ResponseWriter, r *http.Request, path string) (simulationRuleCatalogInput, error) {
	var in simulationRuleCatalogInput
	bad := func() (simulationRuleCatalogInput, error) { return in, fail(400, "invalid_input") }
	rt, ok := simulationRuleCatalogRoutes[path]
	if !ok || r.Method != rt.method {
		return bad()
	}
	var raw []byte
	if rt.write {
		media, _, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if e != nil || media != "application/json" || r.URL.RawQuery != "" {
			return bad()
		}
		raw, e = io.ReadAll(http.MaxBytesReader(w, r.Body, 65536))
		if e != nil || !utf8.Valid(raw) {
			return bad()
		}
	} else {
		if r.ContentLength != 0 || len(r.TransferEncoding) != 0 || len(r.URL.RawQuery) > 4096 {
			return bad()
		}
		q, e := url.ParseQuery(r.URL.RawQuery)
		if e != nil {
			return bad()
		}
		obj := map[string]any{}
		for k, v := range q {
			if len(v) != 1 || !utf8.ValidString(v[0]) {
				return bad()
			}
			if k == "offset" || k == "limit" {
				n, e := strconv.Atoi(v[0])
				if e != nil || strconv.Itoa(n) != v[0] {
					return bad()
				}
				obj[k] = n
			} else {
				obj[k] = v[0]
			}
		}
		raw, _ = json.Marshal(obj)
	}
	fields, e := simulationLabelsExact(raw, rt.fields)
	if e != nil {
		return bad()
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&in) != nil || d.Decode(&struct{}{}) != io.EOF || resourceUniqueJSON(json.NewDecoder(bytes.NewReader(raw))) != nil {
		return bad()
	}
	if !messageIdentifier(in.DomainID) || in.DomainID == "platform" {
		return bad()
	}
	for _, v := range []struct{ k, v string }{{"id", in.ID}, {"operationId", in.OperationID}, {"templateId", in.TemplateID}} {
		if _, ok := fields[v.k]; ok && !messageIdentifier(v.v) {
			return bad()
		}
	}
	if path == "/list" && (in.Offset < 0 || in.Offset > 10000 || in.Limit < 1 || in.Limit > catalog.MaxPage || !simulationLabelsText(in.Query, 128, true)) {
		return bad()
	}
	if rt.write && (in.ExpectedRevision > catalog.MaxRevisions || (path == "/create" && in.ExpectedRevision != 0) || (path != "/create" && in.ExpectedRevision == 0)) {
		return bad()
	}
	if spec, ok := fields["declaration"]; ok {
		f, e := simulationLabelsExact(spec, "combine conditions")
		if e != nil || in.Declaration == nil || !slices.Contains([]string{"all", "any"}, in.Declaration.Combine) {
			return bad()
		}
		var cond []json.RawMessage
		if json.Unmarshal(f["conditions"], &cond) != nil || len(cond) < 1 || len(cond) > detectionrules.MaxConditions {
			return bad()
		}
		for i, c := range cond {
			cf, e := simulationLabelsExact(c, "field operator values")
			if e != nil || in.Declaration.Conditions[i].Values == nil {
				return bad()
			}
			var values []json.RawMessage
			if json.Unmarshal(cf["values"], &values) != nil {
				return bad()
			}
			for _, v := range values {
				if string(v) == "null" {
					return bad()
				}
			}
		}
	}
	if fixtures, ok := fields["cases"]; ok {
		var cs []json.RawMessage
		if json.Unmarshal(fixtures, &cs) != nil || len(cs) < 2 || len(cs) > catalog.MaxCases {
			return bad()
		}
		for i, c := range cs {
			cf, e := simulationLabelsExact(c, "id fields wantMatch")
			if e != nil || !messageIdentifier(in.Cases[i].ID) || in.Cases[i].Fields == nil || len(in.Cases[i].Fields) > 32 {
				return bad()
			}
			var values map[string]json.RawMessage
			if json.Unmarshal(cf["fields"], &values) != nil {
				return bad()
			}
			for _, v := range values {
				if string(v) == "null" {
					return bad()
				}
			}
			for k, v := range in.Cases[i].Fields {
				if !simulationLabelsText(k, 128, false) || !simulationLabelsText(v, 512, true) {
					return bad()
				}
			}
		}
	}
	if path == "/create-template" {
		h, e := hex.DecodeString(in.PublishedDigest)
		if e != nil || len(h) != 32 || strings.ToLower(in.PublishedDigest) != in.PublishedDigest || in.PublishedVersion < 1 || in.PublishedVersion > catalog.MaxRevisions || !simulationLabelsText(in.Name, 128, false) || in.Acknowledgement != catalog.CopyAcknowledgement {
			return bad()
		}
	}
	return in, nil
}
func (s *Service) SimulationRuleCatalogHandler(enabled, detectionEnabled bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		path, ok := strings.CutPrefix(r.URL.Path, simulationRuleCatalogPath)
		rt, known := simulationRuleCatalogRoutes[path]
		if !ok || !known {
			write(w, 404, map[string]string{"error": "not_found"})
			return
		}
		if !enabled || (path == "/create-template" && !detectionEnabled) {
			write(w, 404, map[string]string{"error": "simulation_disabled"})
			return
		}
		if r.Method != rt.method {
			w.Header().Set("Allow", rt.method)
			write(w, 405, map[string]string{"error": "method_not_allowed"})
			return
		}
		if rt.write && (s.origin == "" || r.Header.Get("Origin") != s.origin) {
			write(w, 403, map[string]string{"error": "forbidden"})
			return
		}
		in, e := decodeSimulationRuleCatalogInput(w, r, path)
		if e != nil {
			write(w, 400, map[string]string{"error": "invalid_input"})
			return
		}
		if s.database == nil {
			write(w, 503, map[string]string{"error": "simulation_unavailable"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		ctx = context.WithValue(ctx, auditContextKey{}, newAuditContext(r))
		out, e := s.handleSimulationRuleCatalog(ctx, r.WithContext(ctx), path, in, detectionEnabled)
		if e != nil {
			status, code := simulationRuleCatalogHTTPError(e)
			write(w, status, map[string]string{"error": code})
			return
		}
		w.Header().Set("X-ADTR-User-ID", strconv.FormatInt(out.actorID, 10))
		if path == "/export" {
			w.Header().Set("Content-Disposition", `attachment; filename="simulation-rule-publications.json"`)
		}
		write(w, 200, out.value)
	})
}
func simulationRuleCatalogHTTPError(e error) (int, string) {
	switch {
	case errors.Is(e, tasks.ErrSchemaIncompatible):
		return 503, "schema_incompatible"
	case errors.Is(e, catalog.ErrDenied):
		return 403, "forbidden"
	case errors.Is(e, catalog.ErrNotFound):
		return 404, "not_found"
	case errors.Is(e, catalog.ErrConflict):
		return 409, "revision_conflict"
	case errors.Is(e, catalog.ErrTransition):
		return 409, "invalid_transition"
	case errors.Is(e, catalog.ErrInvalid):
		return 400, "invalid_input"
	case errors.Is(e, catalog.ErrUnsupportedCopy):
		return 422, "unsupported_copy_fields"
	case errors.Is(e, catalog.ErrLimit):
		return 422, "simulation_limit_exceeded"
	}
	return simulationDetectionHTTPError(e)
}
func (s *Service) handleSimulationRuleCatalog(ctx context.Context, r *http.Request, path string, in simulationRuleCatalogInput, detectionEnabled bool) (*credentialUseResult, error) {
	conn, e := dbadmission.ConnectConfig(ctx, s.database.Copy())
	if e != nil {
		return nil, e
	}
	defer conn.Close(ctx)
	tx, e := conn.Begin(ctx)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(ctx)
	if e = tasks.CheckSchemaTx(ctx, tx, schemaversion.Current); e != nil {
		return nil, e
	}
	actor, role, grants, e := s.authenticateAccess(ctx, tx, r)
	if e != nil {
		return nil, e
	}
	if !simulationRuleCatalogPathAllowed(r.Method, r.URL.Path, grants) {
		return nil, fail(403, "forbidden")
	}
	allowed, e := s.effectiveResourceIDs(ctx, tx, actor, role)
	if e != nil {
		return nil, e
	}
	if !slices.Contains(allowed, in.DomainID) {
		return nil, fail(404, "not_found")
	}
	scope := simulationRuleCatalogScope(actor, in.DomainID, grants, detectionEnabled)
	audit := func(ctx context.Context, tx pgx.Tx, receipt catalog.Receipt) error {
		return s.audit(ctx, tx, actor, "simulation_rule_catalog_"+receipt.Action, actor.ID)
	}
	var value any
	switch path {
	case "/list":
		value, e = catalog.ListTx(ctx, tx, scope, in.Query, in.Offset, in.Limit)
	case "/detail":
		value, e = catalog.DetailTx(ctx, tx, scope, in.ID)
	case "/export":
		value, e = catalog.ExportTx(ctx, tx, scope, in.ID)
	case "/receipt":
		value, e = catalog.ReceiptTx(ctx, tx, scope, in.OperationID)
		if errors.Is(e, catalog.ErrNotFound) {
			return nil, fail(404, "operation_not_found")
		}
	case "/create-template":
		value, e = catalog.CopyTemplateTx(ctx, tx, scope, catalog.CopyInput{ID: in.ID, OperationID: in.OperationID, TemplateID: in.TemplateID, Name: in.Name, Acknowledgement: in.Acknowledgement, ExpectedRevision: in.ExpectedRevision, PublishedVersion: in.PublishedVersion, PublishedDigest: in.PublishedDigest}, audit)
	default:
		value, e = catalog.MutateTx(ctx, tx, scope, catalog.Mutation{ID: in.ID, Action: strings.TrimPrefix(path, "/"), OperationID: in.OperationID, ExpectedRevision: in.ExpectedRevision, Declaration: in.Declaration, Cases: in.Cases}, audit)
	}
	if e != nil {
		return nil, e
	}
	if e = tx.Commit(ctx); e != nil {
		return nil, e
	}
	return &credentialUseResult{value: value, actorID: actor.ID}, nil
}

// Receipt recovery reauthorizes manual-template contents under the live feature
// flag as well as the live grant. Ordinary catalog receipts remain readable.
func simulationRuleCatalogScope(actor User, domain string, grants map[string]AccessAuth, detectionEnabled bool) catalog.Scope {
	g, p, d := grants["simulation_rule_catalog"], grants["simulation_rule_catalog_publish"], grants["simulation_detection"]
	return catalog.Scope{Tenant: actor.tenant, Domain: domain, ActorID: actor.ID, Read: g.Readable, Manage: g.Writeable, Publish: p.Readable && p.Writeable, DetectionRead: d.Readable && detectionEnabled, DetectionManage: d.Writeable && detectionEnabled}
}
