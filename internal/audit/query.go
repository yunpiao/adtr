package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/tasks"
)

// Every caller gates the exact shared application schema version before identity locks.
func requireSchema(ctx context.Context, tx pgx.Tx) error {
	var version int
	var held bool
	err := tx.QueryRow(ctx, `SELECT version,EXISTS(SELECT FROM pg_locks WHERE locktype='advisory' AND pid=pg_backend_pid() AND granted AND classid=0::oid AND objid::bigint=734192801 AND objsubid=1 AND mode IN ('ShareLock','ExclusiveLock')) FROM adtr.schema_version WHERE singleton=true`).Scan(&version, &held)
	if err != nil {
		return err
	}
	if version != SchemaVersion {
		return tasks.ErrSchemaIncompatible
	}
	if !held {
		return tasks.ErrSchemaGateRequired
	}
	return nil
}
func scopedQuery(p Principal, f Filter) (string, []any) {
	args := []any{p.TenantID, p.RoleID}
	where := `e.tenant_id=$1 AND (e.domain_id IS NULL OR
 (e.source='task' AND e.domain_id='platform' AND e.task_kind IN ('infrastructure.health','audit.export','system.logs_bundle')) OR
 (e.domain_id<>'platform' AND EXISTS(SELECT FROM adtr.resource_tenant_config c
 JOIN adtr.resource_domains d ON d.tenant_id=c.tenant_id
 JOIN adtr.resource_group_members m ON m.tenant_id=d.tenant_id AND m.domain_id=d.id
 JOIN adtr.resource_role_groups g ON g.tenant_id=m.tenant_id AND g.group_id=m.group_id
 WHERE c.tenant_id=$1 AND c.expire_time>extract(epoch FROM clock_timestamp()) AND c.max_ad_count>0
 AND (SELECT count(*) FROM adtr.resource_domains counted WHERE counted.tenant_id=c.tenant_id AND counted.active)<=c.max_ad_count
 AND d.id=e.domain_id AND d.active AND g.role_id=$2)))`
	add := func(expr string, v any) { args = append(args, v); where += " AND " + fmt.Sprintf(expr, len(args)) }
	if f.Visibility == "visible" {
		where += " AND NOT COALESCE(v.hidden,false)"
	} else if f.Visibility == "hidden" {
		where += " AND COALESCE(v.hidden,false)"
	}
	if f.StartTm != "" {
		add("e.occurred_at >= $%d::timestamptz", f.StartTm)
	}
	if f.EndTm != "" {
		add("e.occurred_at < $%d::timestamptz", f.EndTm)
	}
	if f.Keyword != "" {
		args = append(args, f.Keyword)
		n := len(args)
		where += fmt.Sprintf(" AND (strpos(lower(COALESCE(e.login_user,'')),lower($%d))>0 OR strpos(lower(COALESCE(e.source_ip,'')),lower($%d))>0)", n, n)
	}
	if len(f.FilterEvent) > 0 {
		add("e.event=ANY($%d::text[])", f.FilterEvent)
	}
	if len(f.LogTypeList) > 0 {
		add("e.log_type=ANY($%d::integer[])", f.LogTypeList)
	}
	return `SELECT e.*,COALESCE(v.hidden,false) hidden,COALESCE(v.version,0) visibility_version FROM adtr.audit_source e LEFT JOIN adtr.audit_visibility v ON v.tenant_id=e.tenant_id AND v.source=e.source AND v.source_id=e.source_id WHERE ` + where, args
}
func order(f Filter) string {
	d := " DESC"
	if f.CreateSort == 1 {
		d = " ASC"
	}
	return "occurred_at" + d + ",source COLLATE \"C\"" + d + ",source_id" + d
}

const rowJSON = `jsonb_build_object('ID',source||'.'||source_id::text,'loginUser',login_user,'sourceIp',source_ip,'event',event,'eventArgs',event_args::text,'eventResult',event_result,'CreateTm',occurred_at,'logType',log_type,'logTypeName',CASE log_type WHEN 8 THEN '报告与审计' WHEN 9 THEN '系统设置' ELSE '未知' END,'userId',user_id,'source',source,'domainId',domain_id,'availability',jsonb_build_object('loginUser',login_user IS NOT NULL,'sourceIp',source_ip IS NOT NULL,'path',audit_path IS NOT NULL,'requestId',audit_request_id IS NOT NULL,'eventResult',result_available),'deleted',hidden,'deletable',adtr.audit_event_deletable(source,event),'visibilityVersion',visibility_version)`

func ListTx(ctx context.Context, tx pgx.Tx, p Principal, f Filter) (List, error) {
	out := List{List: []Row{}}
	if err := requireSchema(ctx, tx); err != nil {
		return out, err
	}
	f, err := NormalizeFilter(f)
	if err != nil {
		return out, err
	}
	sql, args := scopedQuery(p, f)
	size := f.PageSize
	if size == -1 {
		size = 1001
	}
	args = append(args, size, (f.PageIdx-1)*size)
	var raw []byte
	var total int
	err = tx.QueryRow(ctx, `WITH selected AS MATERIALIZED (`+sql+`), page AS (SELECT occurred_at,source,source_id,`+rowJSON+` row_data FROM selected ORDER BY `+order(f)+fmt.Sprintf(` LIMIT $%d OFFSET $%d) SELECT (SELECT count(*) FROM selected),COALESCE((SELECT jsonb_agg(row_data ORDER BY `+order(f)+`) FROM page),'[]'::jsonb)`, len(args)-1, len(args)), args...).Scan(&total, &raw)
	if err != nil {
		return out, err
	}
	if f.PageSize == -1 && total > 1000 {
		return out, problem(422, "result_too_large")
	}
	if err = json.Unmarshal(raw, &out.List); err != nil {
		return out, err
	}
	for i := range out.List {
		out.List[i].CreateTm = out.List[i].CreateTm.UTC()
	}
	pages := (total + size - 1) / size
	out.Page = Page{f.PageIdx, f.PageSize, total, pages}
	out.Exhausted = f.PageIdx >= pages
	return out, nil
}

type Type struct {
	LogType       int      `json:"logType"`
	LogTypeName   string   `json:"logTypeName"`
	EventNameList []string `json:"eventNameList"`
}
type Types struct {
	Events []string `json:"events"`
	List   []Type   `json:"List"`
}

func TypesTx(ctx context.Context, tx pgx.Tx, p Principal) (Types, error) {
	out := Types{Events: []string{}, List: []Type{}}
	if err := requireSchema(ctx, tx); err != nil {
		return out, err
	}
	f, _ := NormalizeFilter(Filter{})
	sql, args := scopedQuery(p, f)
	rows, err := tx.Query(ctx, `WITH selected AS (`+sql+`) SELECT DISTINCT log_type,event FROM selected ORDER BY log_type,event LIMIT 1001`, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	grouped := map[int][]string{}
	seen := map[string]bool{}
	count := 0
	for rows.Next() {
		var n int
		var event string
		if err = rows.Scan(&n, &event); err != nil {
			return out, err
		}
		count++
		if count > 1000 {
			return out, problem(422, "metadata_too_large")
		}
		grouped[n] = append(grouped[n], event)
		if !seen[event] {
			out.Events = append(out.Events, event)
			seen[event] = true
		}
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	sort.Strings(out.Events)
	for n := 1; n <= 9; n++ {
		events := grouped[n]
		if events == nil {
			events = []string{}
		}
		out.List = append(out.List, Type{n, TypeName(n), events})
	}
	return out, nil
}

type VisibilityResult struct {
	Result             string `json:"result"`
	Changed            int    `json:"changed"`
	VisibilityRevision int64  `json:"visibilityRevision"`
}

func ChangeVisibilityTx(ctx context.Context, tx pgx.Tx, p Principal, ids []string, reason string, hidden bool) (VisibilityResult, error) {
	var out VisibilityResult
	if err := requireSchema(ctx, tx); err != nil {
		return out, err
	}
	if err := ValidateTargets(ids, reason); err != nil {
		return out, err
	}
	f, _ := NormalizeFilter(Filter{Visibility: "all"})
	sql, args := scopedQuery(p, f)
	args = append(args, ids)
	rows, err := tx.Query(ctx, `WITH selected AS (`+sql+fmt.Sprintf(`) SELECT source,source_id,hidden,adtr.audit_event_deletable(source,event) FROM selected WHERE source||'.'||source_id::text=ANY($%d::text[]) ORDER BY source,source_id`, len(args)), args...)
	if err != nil {
		return out, err
	}
	type target struct {
		source    string
		id        int64
		hidden    bool
		deletable bool
	}
	targets := []target{}
	for rows.Next() {
		var t target
		if err = rows.Scan(&t.source, &t.id, &t.hidden, &t.deletable); err != nil {
			rows.Close()
			return out, err
		}
		targets = append(targets, t)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if len(targets) != len(ids) {
		return out, problem(404, "not_found")
	}
	for _, t := range targets {
		if !t.deletable {
			return out, problem(409, "protected_audit_event")
		}
	}
	changed := 0
	for _, t := range targets {
		if t.hidden != hidden {
			changed++
		}
	}
	if changed == 0 {
		return out, problem(409, "visibility_unchanged")
	}
	var revision int64
	err = tx.QueryRow(ctx, `INSERT INTO adtr.audit_visibility_revisions(tenant_id,revision) VALUES($1,1) ON CONFLICT(tenant_id) DO UPDATE SET revision=adtr.audit_visibility_revisions.revision+1 RETURNING revision`, p.TenantID).Scan(&revision)
	if err != nil {
		return out, err
	}
	action := "audit_restore"
	if hidden {
		action = "audit_hide"
	}
	for _, t := range targets {
		if t.hidden == hidden {
			continue
		}
		var version int64
		err = tx.QueryRow(ctx, `INSERT INTO adtr.audit_visibility(tenant_id,source,source_id,hidden,version) VALUES($1,$2,$3,$4,1) ON CONFLICT(tenant_id,source,source_id) DO UPDATE SET hidden=EXCLUDED.hidden,version=adtr.audit_visibility.version+1 RETURNING version`, p.TenantID, t.source, t.id, hidden).Scan(&version)
		if err != nil {
			return out, err
		}
		_, err = tx.Exec(ctx, `INSERT INTO adtr.audit_events(tenant_id,actor_id,action,target_id,reason,visibility_version) VALUES($1,$2,$3,$4,$5,$6)`, p.TenantID, p.ActorID, action, sourceID(t.source, t.id), strings.TrimSpace(reason), version)
		if err != nil {
			return out, err
		}
	}
	return VisibilityResult{"success", changed, revision}, nil
}
func RecordExportTx(ctx context.Context, tx pgx.Tx, p Principal, taskID string) error {
	_, err := tx.Exec(ctx, `INSERT INTO adtr.audit_events(tenant_id,actor_id,action,target_id) VALUES($1,$2,'audit_export',$3)`, p.TenantID, p.ActorID, taskID)
	return err
}
