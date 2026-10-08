//go:build integration

package store

// Frozen F49 projection before the domain audit producer in migration 9.
const auditVersionEightView = `
CREATE OR REPLACE FUNCTION adtr.audit_event_deletable(source text,event text) RETURNS boolean LANGUAGE SQL IMMUTABLE AS $$
 SELECT source<>'audit' AND event NOT IN ('schedule.create','schedule.enable','schedule.pause','task.archive','task.restore');
$$;
CREATE OR REPLACE VIEW adtr.audit_source AS
SELECT 'auth'::text source,a.id source_id,a.tenant_id,a.actor_id user_id,a.audit_username login_user,a.audit_ip source_ip,
 CASE WHEN split_part(a.action,':',1) IN ('role_create','role_update','role_delete','permissions_update','schedule.create','schedule.enable','schedule.pause','task.archive','task.restore') THEN split_part(a.action,':',1) ELSE a.action END event,
 jsonb_strip_nulls(jsonb_build_object('targetId',a.target_id,'roleId',CASE WHEN split_part(a.action,':',1) IN ('role_create','role_update','role_delete','permissions_update') AND position(':' in a.action)>0 THEN substr(a.action,position(':' in a.action)+1) END,'scheduleUUID',CASE WHEN split_part(a.action,':',1) IN ('schedule.create','schedule.enable','schedule.pause') AND position(':' in a.action)>0 THEN split_part(a.action,':',2) END,'operationUUID',CASE WHEN split_part(a.action,':',1) IN ('task.archive','task.restore') AND position(':' in a.action)>0 THEN split_part(a.action,':',2) END,'path',a.audit_path,'requestId',a.audit_request_id)) event_args,
 CASE WHEN right(a.action,7)='_denied' THEN 'FAIL' WHEN (a.action IN ('login','logout','password_change','password_reset','mfa_enroll_begin','mfa_enable','mfa_disable','bootstrap','user_create','user_delete','user_update','user_role_assign','resource_group_create','resource_group_update','resource_group_delete','resource_roles_replace','resource_tenant_update','system_storage_alarm') OR (split_part(a.action,':',1) IN ('role_create','role_update','role_delete','permissions_update','schedule.create','schedule.enable','schedule.pause','task.archive','task.restore') AND position(':' in a.action)>0)) THEN 'SUCCESS' ELSE 'NONE' END event_result,
 (right(a.action,7)='_denied' OR (a.action IN ('login','logout','password_change','password_reset','mfa_enroll_begin','mfa_enable','mfa_disable','bootstrap','user_create','user_delete','user_update','user_role_assign','resource_group_create','resource_group_update','resource_group_delete','resource_roles_replace','resource_tenant_update','system_storage_alarm') OR (split_part(a.action,':',1) IN ('role_create','role_update','role_delete','permissions_update','schedule.create','schedule.enable','schedule.pause','task.archive','task.restore') AND position(':' in a.action)>0))) result_available,
 a.occurred_at,CASE WHEN split_part(a.action,':',1) IN ('task.archive','task.restore') THEN 8 ELSE 9 END log_type,NULL::text domain_id,NULL::text task_kind,a.audit_path,a.audit_request_id FROM adtr.auth_audit a
UNION ALL
SELECT 'resource',a.id,a.tenant_id,a.actor_id,a.audit_username,a.audit_ip,a.action,
 jsonb_strip_nulls(jsonb_build_object('targetId',a.target_id,'path',a.audit_path,'requestId',a.audit_request_id)),
 CASE WHEN a.action IN ('resource_group_create','resource_group_update','resource_group_delete','resource_roles_replace','resource_tenant_update','system_storage_alarm') THEN 'SUCCESS' ELSE 'NONE' END,
 a.action IN ('resource_group_create','resource_group_update','resource_group_delete','resource_roles_replace','resource_tenant_update','system_storage_alarm'),a.occurred_at,9,NULL,NULL,a.audit_path,a.audit_request_id FROM adtr.resource_audit a
UNION ALL
SELECT 'task',a.id,a.tenant_id,a.actor_id,a.audit_username,a.audit_ip,a.action,
 jsonb_strip_nulls(jsonb_build_object('taskUUID',a.task_id,'state',a.state,'attempt',a.attempt,'path',a.audit_path,'requestId',a.audit_request_id)),
 CASE WHEN a.action IN ('submitted','recovered','kind_rejected','retry_queued','leased','authorization_rejected','attempts_exhausted','started','authorization_stop_requested','progress','completed-with-cancel-race','finished','lease_recovered','cancel_requested') AND a.state='succeeded' THEN 'SUCCESS' WHEN a.action IN ('submitted','recovered','kind_rejected','retry_queued','leased','authorization_rejected','attempts_exhausted','started','authorization_stop_requested','progress','completed-with-cancel-race','finished','lease_recovered','cancel_requested') AND a.state IN ('failed','partial_failed','dead_letter') THEN 'FAIL' ELSE 'NONE' END,
 a.action IN ('submitted','recovered','kind_rejected','retry_queued','leased','authorization_rejected','attempts_exhausted','started','authorization_stop_requested','progress','completed-with-cancel-race','finished','lease_recovered','cancel_requested') AND a.state IN ('queued','running','retry_wait','cancel_requested','succeeded','failed','partial_failed','dead_letter','cancelled'),a.occurred_at,CASE WHEN t.kind='audit.export' THEN 8 ELSE 9 END,a.domain_id,t.kind,a.audit_path,a.audit_request_id FROM adtr.task_events a JOIN adtr.tasks t ON t.task_id=a.task_id AND t.tenant_id=a.tenant_id
UNION ALL
SELECT 'audit',a.id,a.tenant_id,a.actor_id,a.audit_username,a.audit_ip,a.action,
 jsonb_strip_nulls(jsonb_build_object('targetId',a.target_id,'reason',NULLIF(a.reason,''),'visibilityVersion',a.visibility_version,'path',a.audit_path,'requestId',a.audit_request_id)),
 'SUCCESS',true,a.occurred_at,8,NULL,NULL,a.audit_path,a.audit_request_id FROM adtr.audit_events a;
`
