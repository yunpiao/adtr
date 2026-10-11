"""Crash/restart durability for an isolated, explicitly synthetic bulk fixture.

This tests the real Worker process and PostgreSQL container. It is separate from
Go's real health producer + HTTP acceptance tests: direct SQL here seeds storage,
not a claimed end-to-end user submission. It uses no user or AD credentials.
"""
import subprocess
import time


def verify_bulk_crash_recovery(compose, pause=time.sleep):
    def sql(statement):
        return compose("exec", "-T", "db", "psql", "-X", "-v", "ON_ERROR_STOP=1",
                       "-U", "adtr_dev", "-d", "adtr_dev", "-At", "-c", statement,
                       capture=True).stdout.strip()

    def wait_value(statement, expected, attempts=80):
        for _ in range(attempts):
            if sql(statement) == expected:
                return
            pause(0.25)
        raise AssertionError("bulk lifecycle condition did not reach expected value")

    compose("stop", "worker")
    sql("""
    INSERT INTO adtr.users(tenant_id,username,password_hash,role,must_change)
    VALUES('bulk-lifecycle','bulk-lifecycle','not-an-authentication-credential','platform_admin',false);
    INSERT INTO adtr.tasks(task_id,tenant_id,domain_id,kind,payload_version,payload,payload_hash,
      actor_id,authorization_version,idempotency_key,state,max_attempts,result_version)
    SELECT 'bulk-lifecycle-source-'||g,u.tenant_id,'platform','infrastructure.health',1,'{}','synthetic-fixture',
      u.id,u.authorization_version::text,'bulk-lifecycle-source-'||g,'succeeded',1,1
    FROM adtr.users u CROSS JOIN generate_series(1,51) g WHERE u.username='bulk-lifecycle';
    INSERT INTO adtr.task_events(task_id,tenant_id,domain_id,actor_id,action,state,attempt,result_version,fencing_token,authorization_version)
    SELECT task_id,tenant_id,domain_id,actor_id,'finished','succeeded',1,1,1,authorization_version
    FROM adtr.tasks WHERE tenant_id='bulk-lifecycle';
    INSERT INTO adtr.messages(message_id,tenant_id,recipient_user_id,source_task_id,source_event_id,source_result_version,message_type,sub_type,title,description,occurred_at)
    SELECT e.task_id,e.tenant_id,e.actor_id,e.task_id,e.id,1,'task_result','succeeded','Synthetic lifecycle fixture','No production source assertion',e.occurred_at
    FROM adtr.task_events e WHERE e.tenant_id='bulk-lifecycle';
    INSERT INTO adtr.message_bulk_operations(tenant_id,user_id,operation_id,result_id,selection,request_hash,authorization_version,accepted_at,updated_at,state,target)
    SELECT tenant_id,id,'bulk-lifecycle-operation','synthetic-no-snapshot','all',repeat('a',64),authorization_version,clock_timestamp(),clock_timestamp(),'queued',51
    FROM adtr.users WHERE username='bulk-lifecycle';
    INSERT INTO adtr.message_bulk_targets(tenant_id,user_id,operation_id,ordinal,message_id)
    SELECT u.tenant_id,u.id,'bulk-lifecycle-operation',g-1,'bulk-lifecycle-source-'||g
    FROM adtr.users u CROSS JOIN generate_series(1,51) g WHERE u.username='bulk-lifecycle';
    CREATE FUNCTION adtr.synthetic_bulk_lifecycle_pause() RETURNS trigger LANGUAGE plpgsql AS $$
    BEGIN IF NEW.operation_id='bulk-lifecycle-operation' AND NEW.ordinal=50 THEN PERFORM pg_sleep(20); END IF; RETURN NEW; END; $$;
    CREATE TRIGGER synthetic_bulk_lifecycle_pause BEFORE INSERT ON adtr.message_bulk_receipts FOR EACH ROW EXECUTE FUNCTION adtr.synthetic_bulk_lifecycle_pause();
    """)
    compose("start", "worker")
    progress_sql = "SELECT state||':'||processed||':'||(target-processed) FROM adtr.message_bulk_operations WHERE operation_id='bulk-lifecycle-operation'"
    wait_value(progress_sql, "running:50:1")
    committed = sql("SELECT md5(string_agg(ordinal||':'||first_read_at::text||':'||committed_at::text||':'||newly_read::text,',' ORDER BY ordinal)) FROM adtr.message_bulk_receipts WHERE operation_id='bulk-lifecycle-operation'")
    accepted = sql("SELECT accepted_at::text||':'||request_hash||':'||target FROM adtr.message_bulk_operations WHERE operation_id='bulk-lifecycle-operation'")
    # Capture committed evidence BEFORE observing the next in-flight batch.
    # Only accept a recently started sleep, leaving headroom before the fixed
    # production 5-second statement deadline, then crash without extra SQL calls.
    wait_value("SELECT EXISTS(SELECT FROM pg_stat_activity WHERE datname=current_database() AND wait_event='PgSleep' AND query LIKE 'INSERT INTO adtr.message_bulk_receipts%' AND query_start>clock_timestamp()-interval '2 seconds')", "t")
    compose("kill", "--signal", "SIGKILL", "db", "worker")
    compose("start", "db")
    for _ in range(80):
        try:
            ready = compose("exec", "-T", "db", "pg_isready", "-U", "adtr_dev", "-d", "adtr_dev", capture=True)
            if "accepting connections" in ready.stdout:
                break
        except subprocess.CalledProcessError:
            pass
        pause(0.25)
    else:
        raise AssertionError("crashed PostgreSQL did not restart")
    if sql(progress_sql) != "running:50:1":
        raise AssertionError("crash lost committed progress or preserved a partial batch")
    if sql("SELECT count(*) FROM adtr.message_reads WHERE tenant_id='bulk-lifecycle'") != "50":
        raise AssertionError("crash did not roll back the uncommitted read")
    if sql("SELECT accepted_at::text||':'||request_hash||':'||target FROM adtr.message_bulk_operations WHERE operation_id='bulk-lifecycle-operation'") != accepted:
        raise AssertionError("crash changed original acceptance")
    sql("DROP TRIGGER synthetic_bulk_lifecycle_pause ON adtr.message_bulk_receipts")
    compose("start", "worker")
    wait_value(progress_sql, "completed:51:0")
    if sql("SELECT md5(string_agg(ordinal||':'||first_read_at::text||':'||committed_at::text||':'||newly_read::text,',' ORDER BY ordinal)) FROM adtr.message_bulk_receipts WHERE operation_id='bulk-lifecycle-operation' AND ordinal<50") != committed:
        raise AssertionError("restarted Worker rewrote committed first times or receipts")
    wait_value("SELECT count(*) FROM adtr.message_reads WHERE tenant_id='bulk-lifecycle'", "51")
    print("Bulk lifecycle: actual PostgreSQL/Worker SIGKILL, partial-batch rollback, original receipts and persisted restart progress PASS")
