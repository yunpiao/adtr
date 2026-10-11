package messages

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/messagecore"
	"regexp"
	"strconv"
	"time"
)

var identifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)
var sessionDigest = regexp.MustCompile(`^[0-9a-f]{64}$`)

func identityOK(id Identity) bool {
	return identifier.MatchString(id.TenantID) && id.UserID > 0 && id.AuthorizationVersion > 0 && sessionDigest.MatchString(id.SessionHash)
}

// requireIdentity checks the trusted adapter contract. The HTTP adapter owns
// live authentication/grant checks and retains tenant/actor locks to commit.
func (s *Store) requireIdentity(ctx context.Context, tx pgx.Tx, id Identity) error {
	if err := s.requireSchemaTx(ctx, tx); err != nil {
		return err
	}
	if !identityOK(id) {
		return problem(403, "forbidden")
	}
	return nil
}
func randomID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

type snapshotData struct {
	Request messagecore.Request
	TaskIDs map[string]string
}
type loaded struct {
	id      string
	expires time.Time
	data    snapshotData
	core    *messagecore.Result
}

func (v loaded) page(offset, limit int) (Snapshot, error) {
	p, err := v.core.Page(offset, limit)
	if err != nil {
		return Snapshot{}, coreError(err)
	}
	out := Snapshot{ResultID: v.id, ConfirmedAt: v.core.ConfirmedAt(), ExpiresAt: v.expires.UTC(), Items: []Entry{}, Total: p.Total, Counts: Counts{p.Counts.All, p.Counts.Unread, p.Counts.Read}, Offset: p.Offset, Limit: p.Limit}
	for _, e := range p.Items {
		m := e.Message
		item := Entry{ID: m.ID, TaskID: v.data.TaskIDs[m.ID], Type: m.Type, SubType: m.SubType, Title: m.Title, Description: m.Description, ScopeKind: m.ScopeKind, DomainID: m.DomainID, OccurredAt: m.OccurredAt, IngestedAt: m.IngestedAt, Read: e.Read}
		if e.Read {
			at := e.FirstReadAt
			item.FirstReadAt = &at
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}

func (s *Store) QueryTx(ctx context.Context, tx pgx.Tx, id Identity, q messagecore.Query, now time.Time) (Snapshot, error) {
	if err := s.requireIdentity(ctx, tx, id); err != nil {
		return Snapshot{}, err
	}
	now = now.UTC().Truncate(time.Microsecond)
	req := messagecore.Request{Scope: messagecore.Scope{TenantID: id.TenantID, UserID: strconv.FormatInt(id.UserID, 10), IncludePlatform: true}, Catalog: catalog(), Query: q, ReadState: messagecore.ReadState{TenantID: id.TenantID, UserID: strconv.FormatInt(id.UserID, 10), FirstReadAt: map[string]time.Time{}}, ConfirmedAt: now.UTC(), Limits: limits()}
	empty, err := messagecore.NewResult(req)
	if err != nil {
		return Snapshot{}, coreError(err)
	}
	req.Query = empty.Query()
	// Serialize only snapshot admission. No code acquires an actor lock after
	// this global quota lock; maintenance takes no actor/tenant locks.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(734192850::bigint)`); err != nil {
		return Snapshot{}, err
	}
	var userCount, tenantCount, globalCount int
	if err = tx.QueryRow(ctx, `SELECT
 (SELECT count(*) FROM (SELECT 1 FROM adtr.message_results WHERE tenant_id=$1 AND user_id=$2 AND expires_at>$3 LIMIT 8) a),
 (SELECT count(*) FROM (SELECT 1 FROM adtr.message_results WHERE tenant_id=$1 AND expires_at>$3 LIMIT 128) b),
 (SELECT count(*) FROM (SELECT 1 FROM adtr.message_results WHERE expires_at>$3 LIMIT 1024) c)`, id.TenantID, id.UserID, now).Scan(&userCount, &tenantCount, &globalCount); err != nil {
		return Snapshot{}, err
	}
	if userCount >= MaxUserSnapshots || tenantCount >= MaxTenantSnapshots || globalCount >= MaxGlobalSnapshots {
		return Snapshot{}, problem(429, "snapshot_quota_exceeded")
	}
	// The indexed recipient/time candidate budget precedes arbitrary keyword and
	// read filters. Narrow the time range when its candidate set is too large.
	rows, err := tx.Query(ctx, `SELECT m.message_id,m.source_task_id,m.message_type,m.sub_type,m.title,m.description,m.occurred_at,m.ingested_at,r.first_read_at
 FROM adtr.messages m LEFT JOIN adtr.message_reads r ON r.tenant_id=m.tenant_id AND r.user_id=m.recipient_user_id AND r.message_id=m.message_id
 WHERE m.tenant_id=$1 AND m.recipient_user_id=$2 AND m.occurred_at>=$3 AND m.occurred_at<$4
 ORDER BY m.occurred_at,m.message_id COLLATE "C" LIMIT 1001`, id.TenantID, id.UserID, ceilMicrosecond(req.Query.Start), ceilMicrosecond(req.Query.End))
	if err != nil {
		return Snapshot{}, err
	}
	data := snapshotData{Request: req, TaskIDs: map[string]string{}}
	for rows.Next() {
		var m messagecore.Message
		var task string
		var first *time.Time
		if err = rows.Scan(&m.ID, &task, &m.Type, &m.SubType, &m.Title, &m.Description, &m.OccurredAt, &m.IngestedAt, &first); err != nil {
			rows.Close()
			return Snapshot{}, err
		}
		m.TenantID = id.TenantID
		m.ScopeKind = messagecore.ScopePlatform
		data.Request.Messages = append(data.Request.Messages, m)
		data.TaskIDs[m.ID] = task
		if first != nil {
			data.Request.ReadState.FirstReadAt[m.ID] = first.UTC()
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return Snapshot{}, err
	}
	if len(data.Request.Messages) > MaxCandidates {
		return Snapshot{}, problem(422, "query_scan_budget_exceeded")
	}
	result, err := messagecore.NewResult(data.Request)
	if err != nil {
		return Snapshot{}, coreError(err)
	}
	// Persist only the actual base; unrelated candidate rows cannot affect page
	// replay and do not need to occupy fixed-result storage.
	basis := result.Base()
	data.Request.Messages = make([]messagecore.Message, 0, len(basis))
	data.Request.ReadState.FirstReadAt = map[string]time.Time{}
	actualTasks := map[string]string{}
	for _, e := range basis {
		data.Request.Messages = append(data.Request.Messages, e.Message)
		actualTasks[e.Message.ID] = data.TaskIDs[e.Message.ID]
		if e.Read {
			data.Request.ReadState.FirstReadAt[e.Message.ID] = e.FirstReadAt
		}
	}
	data.TaskIDs = actualTasks
	raw, err := json.Marshal(data)
	if err != nil {
		return Snapshot{}, err
	}
	if len(raw) > 4*1024*1024 {
		return Snapshot{}, problem(422, "budget_exceeded")
	}
	resultID, err := randomID()
	if err != nil {
		return Snapshot{}, err
	}
	expires := now.UTC().Add(SnapshotTTL)
	if _, err = tx.Exec(ctx, `INSERT INTO adtr.message_results(result_id,tenant_id,user_id,session_hash,authorization_version,projection_version,confirmed_at,expires_at,payload) VALUES($1,$2,$3,$4,$5,1,$6,$7,$8)`, resultID, id.TenantID, id.UserID, id.SessionHash, id.AuthorizationVersion, now, expires, raw); err != nil {
		return Snapshot{}, err
	}
	return (loaded{id: resultID, expires: expires, data: data, core: result}).page(req.Query.Offset, req.Query.Limit)
}

func (s *Store) loadTx(ctx context.Context, tx pgx.Tx, id Identity, resultID string, now time.Time) (loaded, error) {
	if !identifier.MatchString(resultID) {
		return loaded{}, problem(400, "invalid_result")
	}
	var session string
	var epoch int64
	var version int
	var confirmed, expires time.Time
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT session_hash,authorization_version,projection_version,confirmed_at,expires_at,payload FROM adtr.message_results WHERE tenant_id=$1 AND user_id=$2 AND result_id=$3 FOR SHARE`, id.TenantID, id.UserID, resultID).Scan(&session, &epoch, &version, &confirmed, &expires, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return loaded{}, problem(404, "not_found")
	}
	if err != nil {
		return loaded{}, err
	}
	if session != id.SessionHash || epoch != id.AuthorizationVersion || version != 1 {
		return loaded{}, problem(410, "result_invalidated")
	}
	if now.IsZero() || !expires.After(now) {
		return loaded{}, problem(410, "result_expired")
	}
	if len(raw) > 4*1024*1024 {
		return loaded{}, problem(500, "invalid_stored_result")
	}
	var data snapshotData
	if err = json.Unmarshal(raw, &data); err != nil {
		return loaded{}, problem(500, "invalid_stored_result")
	}
	if data.Request.Scope.TenantID != id.TenantID || data.Request.Scope.UserID != strconv.FormatInt(id.UserID, 10) || !data.Request.ConfirmedAt.Equal(confirmed) || data.Request.Limits != limits() {
		return loaded{}, problem(500, "invalid_stored_result")
	}
	result, err := messagecore.NewResult(data.Request)
	if err != nil {
		return loaded{}, problem(500, "invalid_stored_result")
	}
	return loaded{id: resultID, expires: expires, data: data, core: result}, nil
}
func (s *Store) PageTx(ctx context.Context, tx pgx.Tx, id Identity, resultID string, offset, limit int, now time.Time) (Snapshot, error) {
	if err := s.requireIdentity(ctx, tx, id); err != nil {
		return Snapshot{}, err
	}
	v, err := s.loadTx(ctx, tx, id, resultID, now)
	if err != nil {
		return Snapshot{}, err
	}
	return v.page(offset, limit)
}
func (s *Store) LocateTx(ctx context.Context, tx pgx.Tx, id Identity, resultID, messageID string, pageSize int, now time.Time) (Location, error) {
	if err := s.requireIdentity(ctx, tx, id); err != nil {
		return Location{}, err
	}
	v, err := s.loadTx(ctx, tx, id, resultID, now)
	if err != nil {
		return Location{}, err
	}
	loc, err := v.core.Locate(messageID, pageSize)
	if err != nil {
		return Location{}, coreError(err)
	}
	return Location{ResultID: resultID, Found: loc.Found, Index: loc.Index, Page: loc.Page}, nil
}

// PostgreSQL timestamps are microsecond-granular. Both half-open boundaries
// must round UP: floor(start) admits earlier candidates; floor(end) loses the
// final eligible microsecond. Keep original nanoseconds in the core Request.
// A year-9999 carry is safe here: PostgreSQL supports year 10000, and this
// adjusted value is only a SQL parameter, never serialized as RFC3339 JSON.
func ceilMicrosecond(t time.Time) time.Time {
	t = t.UTC()
	if remainder := t.Nanosecond() % 1000; remainder != 0 {
		return t.Add(time.Duration(1000-remainder) * time.Nanosecond)
	}
	return t
}
