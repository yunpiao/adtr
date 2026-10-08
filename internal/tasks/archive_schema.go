package tasks

// ArchiveCoreSchema keeps archival separate from execution and durable history.
// The migrator installs it for existing queues; fresh isolated queue fixtures
// receive the same columns and guards through Schema.
const ArchiveCoreSchema = `
ALTER TABLE adtr.tasks ADD COLUMN IF NOT EXISTS terminal_at timestamptz;
DROP TRIGGER IF EXISTS tasks_terminal_time ON adtr.tasks;
-- Only unambiguous recorded terminal evidence can timestamp an old task.
WITH evidence AS (
 SELECT task_id,min(occurred_at) first_terminal,min(state) state
 FROM adtr.task_events
 WHERE state IN ('succeeded','failed','partial_failed','dead_letter','cancelled')
 AND action IN ('finished','cancel_requested','authorization_rejected','attempts_exhausted','lease_recovered','kind_rejected')
 GROUP BY task_id HAVING count(DISTINCT state)=1
)
UPDATE adtr.tasks t SET terminal_at=e.first_terminal FROM evidence e
 WHERE t.task_id=e.task_id AND t.state=e.state AND t.terminal_at IS NULL;
CREATE OR REPLACE FUNCTION adtr.assign_task_terminal_time() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' THEN
  IF NEW.terminal_at IS NOT NULL THEN RAISE EXCEPTION 'terminal time is server owned'; END IF;
  IF NEW.state IN ('succeeded','failed','partial_failed','dead_letter','cancelled') THEN NEW.terminal_at:=clock_timestamp(); END IF;
  RETURN NEW;
 END IF;
 IF NEW.terminal_at IS DISTINCT FROM OLD.terminal_at THEN RAISE EXCEPTION 'terminal time is immutable'; END IF;
 IF OLD.state IN ('succeeded','failed','partial_failed','dead_letter','cancelled') AND NEW.state NOT IN ('succeeded','failed','partial_failed','dead_letter','cancelled') THEN RAISE EXCEPTION 'terminal task cannot restart'; END IF;
 IF OLD.state NOT IN ('succeeded','failed','partial_failed','dead_letter','cancelled') AND NEW.state IN ('succeeded','failed','partial_failed','dead_letter','cancelled') THEN NEW.terminal_at:=clock_timestamp(); END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER tasks_terminal_time BEFORE INSERT OR UPDATE ON adtr.tasks FOR EACH ROW EXECUTE FUNCTION adtr.assign_task_terminal_time();
CREATE UNIQUE INDEX IF NOT EXISTS tasks_tenant_identity ON adtr.tasks(tenant_id,task_id);
CREATE TABLE IF NOT EXISTS adtr.task_visibility (
 task_id text PRIMARY KEY,tenant_id text NOT NULL,archived boolean NOT NULL DEFAULT false,
 visibility_version bigint NOT NULL DEFAULT 0 CHECK(visibility_version>=0),
 archived_at timestamptz,archived_by bigint,reason text NOT NULL DEFAULT '' CHECK(length(reason)<=500),
 FOREIGN KEY(tenant_id,task_id) REFERENCES adtr.tasks(tenant_id,task_id),
 CHECK(NOT archived OR (archived_at IS NOT NULL AND archived_by IS NOT NULL))
);
CREATE INDEX IF NOT EXISTS task_visibility_tenant_state ON adtr.task_visibility(tenant_id,archived,task_id);
`
