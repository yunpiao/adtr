package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/messagecore"
	"github.com/yunpiao/adtr/internal/messages"
)

// Personal read state is protected by read authority, never task mutation proof.
var messageRoutes = map[string]string{"/query": "POST", "/page": "GET", "/locate": "GET", "/read": "POST", "/read-operation": "GET"}

func messagePathAllowed(method, path string, grants map[string]AccessAuth) bool {
	suffix, ok := strings.CutPrefix(path, "/api/messages")
	return ok && messageRoutes[suffix] == method && grants["tasks"].Readable
}

type messageInput struct {
	Types       []string               `json:"types"`
	SubTypes    []messageSubType       `json:"subTypes"`
	Keyword     string                 `json:"keyword"`
	Start       time.Time              `json:"start"`
	End         time.Time              `json:"end"`
	ReadFilter  messagecore.ReadFilter `json:"readFilter"`
	Sort        messagecore.Sort       `json:"sort"`
	Offset      int                    `json:"offset"`
	Limit       int                    `json:"limit"`
	ResultID    string                 `json:"resultId"`
	MessageID   string                 `json:"messageId"`
	OperationID string                 `json:"operationId"`
}
type messageSubType struct {
	Type    string `json:"type"`
	SubType string `json:"subType"`
}

func (in messageInput) query() messagecore.Query {
	pairs := make([]messagecore.TypeSubType, 0, len(in.SubTypes))
	for _, p := range in.SubTypes {
		pairs = append(pairs, messagecore.TypeSubType{Type: p.Type, SubType: p.SubType})
	}
	return messagecore.Query{Types: in.Types, SubTypes: pairs, Keyword: in.Keyword, Start: in.Start, End: in.End, ReadFilter: in.ReadFilter, Sort: in.Sort, Offset: in.Offset, Limit: in.Limit}
}
func decodeMessageInput(w http.ResponseWriter, r *http.Request, path string) (messageInput, error) {
	var in messageInput
	bad := func() (messageInput, error) { return messageInput{}, fail(400, "invalid_input") }
	if r.URL.RawQuery != "" {
		return bad()
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return bad()
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 32768))
	if err != nil || !utf8.Valid(raw) {
		return bad()
	}
	allowed := "resultId messageId operationId"
	if path == "/query" {
		allowed = "types subTypes keyword start end readFilter sort offset limit"
	}
	fields, err := resourceExactObject(raw, allowed)
	if err != nil {
		return bad()
	}
	if rawPairs, ok := fields["subTypes"]; ok {
		var pairs []json.RawMessage
		if json.Unmarshal(rawPairs, &pairs) != nil {
			return bad()
		}
		for _, pair := range pairs {
			obj, e := resourceExactObject(pair, "type subType")
			if e != nil || len(obj) != 2 || obj["type"] == nil || obj["subType"] == nil || string(obj["type"]) == "null" || string(obj["subType"]) == "null" {
				return bad()
			}
		}
	}
	for _, v := range fields {
		if string(v) == "null" {
			return bad()
		}
	}
	required := []string{"resultId", "messageId", "operationId"}
	if path == "/query" {
		required = []string{"readFilter", "sort", "offset", "limit"}
	}
	for _, k := range required {
		if _, ok := fields[k]; !ok {
			return bad()
		}
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&in) != nil || d.Decode(&struct{}{}) != io.EOF || resourceUniqueJSON(json.NewDecoder(bytes.NewReader(raw))) != nil {
		return bad()
	}
	if path == "/read" {
		if !messageIdentifier(in.ResultID) || !messageIdentifier(in.MessageID) || !messageIdentifier(in.OperationID) {
			return bad()
		}
	}
	return in, nil
}
func messageIdentifier(s string) bool {
	if len(s) == 0 || len(s) > 128 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}
func parseMessageURL(r *http.Request, path string) (messageInput, error) {
	var in messageInput
	bad := func() (messageInput, error) { return messageInput{}, fail(400, "invalid_input") }
	if r.ContentLength != 0 || len(r.TransferEncoding) != 0 || len(r.URL.RawQuery) > 1024 {
		return bad()
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return bad()
	}
	keys := "resultId offset limit"
	if path == "/locate" {
		keys = "resultId messageId limit"
	}
	if path == "/read-operation" {
		keys = "operationId"
	}
	if len(q) != len(strings.Fields(keys)) {
		return bad()
	}
	for _, k := range strings.Fields(keys) {
		v, ok := q[k]
		if !ok || len(v) != 1 || v[0] == "" {
			return bad()
		}
		switch k {
		case "offset", "limit":
			n, e := strconv.Atoi(v[0])
			if e != nil || strconv.Itoa(n) != v[0] || n < 0 || k == "limit" && (n < 1 || n > 200) {
				return bad()
			}
			if k == "offset" {
				in.Offset = n
			} else {
				in.Limit = n
			}
		default:
			if !messageIdentifier(v[0]) {
				return bad()
			}
		}
	}
	in.ResultID, in.MessageID, in.OperationID = q.Get("resultId"), q.Get("messageId"), q.Get("operationId")
	return in, nil
}

func (s *Service) MessagesHandler(store *messages.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		path, ok := strings.CutPrefix(r.URL.Path, "/api/messages")
		method, known := messageRoutes[path]
		if !ok || !known {
			write(w, 404, map[string]string{"error": "not_found"})
			return
		}
		if r.Method != method {
			w.Header().Set("Allow", method)
			write(w, 405, map[string]string{"error": "method_not_allowed"})
			return
		}
		var in messageInput
		var err error
		if method == "POST" {
			if r.Header.Get("Origin") != s.origin {
				write(w, 403, map[string]string{"error": "forbidden"})
				return
			}
			in, err = decodeMessageInput(w, r, path)
		} else {
			in, err = parseMessageURL(r, path)
		}
		if err != nil {
			write(w, 400, map[string]string{"error": "invalid_input"})
			return
		}
		if s.database == nil || store == nil {
			write(w, 503, map[string]string{"error": "messages_unavailable"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		value, err := s.handleMessage(ctx, r.WithContext(ctx), store, path, in)
		if err != nil {
			status, code := messageHTTPError(err)
			write(w, status, map[string]string{"error": code})
			return
		}
		write(w, 200, value)
	})
}
func messageHTTPError(err error) (int, string) {
	var f failure
	var core *messagecore.Error
	var e *messages.Error
	if errors.As(err, &f) {
		return f.status, f.code
	}
	if errors.As(err, &core) {
		return 400, string(core.Code)
	}
	if errors.As(err, &e) {
		return e.Status, e.Code
	}
	return 503, "messages_unavailable"
}
func (s *Service) handleMessage(ctx context.Context, r *http.Request, store *messages.Store, path string, in messageInput) (any, error) {
	conn, err := pgx.ConnectConfig(ctx, s.database.Copy())
	if err != nil {
		return nil, err
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err = store.CheckSchemaTx(ctx, tx); err != nil {
		return nil, err
	}
	actor, _, grants, err := s.authenticateAccess(ctx, tx, r)
	if err != nil {
		return nil, err
	}
	if !messagePathAllowed(r.Method, r.URL.Path, grants) {
		return nil, fail(403, "forbidden")
	}
	cookie, _ := r.Cookie(s.cookieName())
	id := messages.Identity{TenantID: actor.tenant, UserID: actor.ID, SessionHash: digest(cookie.Value)}
	if err = tx.QueryRow(ctx, "SELECT authorization_version FROM adtr.users WHERE tenant_id=$1 AND id=$2", actor.tenant, actor.ID).Scan(&id.AuthorizationVersion); err != nil {
		return nil, err
	}
	now := s.now().UTC()
	var value any
	switch path {
	case "/query":
		value, err = store.QueryTx(ctx, tx, id, in.query(), now)
	case "/page":
		value, err = store.PageTx(ctx, tx, id, in.ResultID, in.Offset, in.Limit, now)
	case "/locate":
		value, err = store.LocateTx(ctx, tx, id, in.ResultID, in.MessageID, in.Limit, now)
	case "/read":
		value, err = store.MarkReadTx(ctx, tx, id, in.ResultID, in.MessageID, in.OperationID, now)
	case "/read-operation":
		value, err = store.ReadOperationTx(ctx, tx, id, in.OperationID)
	}
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return value, nil
}
