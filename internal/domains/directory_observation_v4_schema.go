package domains

// Schema20 adds bounded raw member storage without changing old canonical
// validators or rewriting observations. Membership text remains base64 so NUL
// and valid control characters never need conversion into PostgreSQL text.
const DirectoryObservationV4Schema = `
ALTER TABLE adtr.domain_directory_observations DROP CONSTRAINT domain_directory_observations_dictionary_version_check;
ALTER TABLE adtr.domain_directory_observations ADD CONSTRAINT domain_directory_observations_dictionary_version_check CHECK(dictionary_version IN (1,2,3,4));
CREATE FUNCTION adtr.domain_directory_observation_canonical_v4(b jsonb) RETURNS text LANGUAGE plpgsql IMMUTABLE STRICT AS $$
DECLARE keys text[]; k text; out text; separator text:='';
BEGIN
 CASE jsonb_typeof(b)
 WHEN 'null' THEN RETURN 'null';
 WHEN 'number' THEN
  IF b::text !~ '^(0|[1-9][0-9]*)$' THEN RAISE EXCEPTION 'noncanonical directory number' USING ERRCODE='23514';END IF;
  RETURN b::text;
 WHEN 'string' THEN
  RETURN replace(replace(replace(replace(replace(to_json(b#>>'{}')::text,'<',chr(92)||'u003c'),'>',chr(92)||'u003e'),'&',chr(92)||'u0026'),chr(8232),chr(92)||'u2028'),chr(8233),chr(92)||'u2029');
 WHEN 'array' THEN
  SELECT '['||COALESCE(string_agg(adtr.domain_directory_observation_canonical_v4(value),',' ORDER BY ordinality),'')||']' INTO out FROM jsonb_array_elements(b) WITH ORDINALITY;
  RETURN out;
 WHEN 'object' THEN
  IF b ? 'dictionaryVersion' THEN keys:=ARRAY['dictionaryVersion','objects','source'];
  ELSIF b ? 'objects' THEN keys:=ARRAY['objects','source'];
  ELSIF b ? 'membership' THEN keys:=ARRAY['base','supplemental','identity','membership'];
  ELSIF b ? 'memberBytes' THEN keys:=ARRAY['memberBytes'];
  ELSIF b ? 'identity' THEN keys:=ARRAY['base','supplemental','identity'];
  ELSIF b ? 'primaryGroupIDBytes' THEN keys:=ARRAY['primaryGroupIDBytes','servicePrincipalNameBytes'];
  ELSIF b ? 'base' THEN keys:=ARRAY['base','supplemental'];
  ELSIF b ? 'objectGUID' THEN keys:=ARRAY['objectGUID','distinguishedName','kind','objectClass','samAccountName','userAccountControl'];
  ELSIF b ? 'objectSidBytes' THEN keys:=ARRAY['objectSidBytes','mailBytes','descriptionBytes','whenCreatedBytes'];
  ELSIF b ? 'server_name' THEN keys:=ARRAY['server_name','dc_host_name','domain','naming_context','started_at','completed_at','elapsed_milliseconds','pages'];
  ELSE RAISE EXCEPTION 'invalid directory object shape' USING ERRCODE='23514';END IF;
  IF NOT b ?& keys OR (SELECT count(*) FROM jsonb_object_keys(b))<>cardinality(keys) THEN RAISE EXCEPTION 'invalid directory object keys' USING ERRCODE='23514';END IF;
  out:='{';
  FOREACH k IN ARRAY keys LOOP
   out:=out||separator||to_json(k)::text||':'||adtr.domain_directory_observation_canonical_v4(b->k);separator:=',';
  END LOOP;
  RETURN out||'}';
 ELSE RAISE EXCEPTION 'invalid directory JSON value' USING ERRCODE='23514';
 END CASE;
END; $$;

CREATE FUNCTION adtr.domain_directory_member_value_v4(b jsonb) RETURNS boolean LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE s text; raw bytea; n integer; pos integer:=0; lead integer; width integer; next integer; i integer;
BEGIN
 IF b IS NULL OR b='null'::jsonb THEN RETURN false;END IF;
 IF jsonb_typeof(b) IS DISTINCT FROM 'string' THEN RETURN false;END IF;
 s:=b#>>'{}';
 IF length(s)=0 OR length(s)>4*((4096+2)/3) OR length(s)%4<>0 OR s !~ '^[A-Za-z0-9+/]+={0,2}$' THEN RETURN false;END IF;
 BEGIN raw:=decode(s,'base64');EXCEPTION WHEN invalid_parameter_value THEN RETURN false;END;
 n:=octet_length(raw);
 IF n NOT BETWEEN 1 AND 4096 OR replace(encode(raw,'base64'),chr(10),'')<>s THEN RETURN false;END IF;
 -- Validate UTF-8 directly in bytea, retaining valid NUL and control bytes.
 WHILE pos<n LOOP
  lead:=get_byte(raw,pos);
  IF lead<128 THEN width:=1;
  ELSIF lead BETWEEN 194 AND 223 THEN width:=2;
  ELSIF lead BETWEEN 224 AND 239 THEN width:=3;
  ELSIF lead BETWEEN 240 AND 244 THEN width:=4;
  ELSE RETURN false;END IF;
  IF pos+width>n THEN RETURN false;END IF;
  IF width>1 THEN
   next:=get_byte(raw,pos+1);
   IF (lead=224 AND next<160) OR (lead=237 AND next>159) OR (lead=240 AND next<144) OR (lead=244 AND next>143) THEN RETURN false;END IF;
   FOR i IN 1..width-1 LOOP IF get_byte(raw,pos+i) NOT BETWEEN 128 AND 191 THEN RETURN false;END IF;END LOOP;
  END IF;
  pos:=pos+width;
 END LOOP;
 RETURN true;
END; $$;

CREATE FUNCTION adtr.domain_directory_membership_v4(b jsonb, kind text) RETURNS boolean LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE vals jsonb; v jsonb; s text; used integer:=0; seen text[]:=ARRAY[]::text[];
BEGIN
 IF b IS NULL OR jsonb_typeof(b) IS DISTINCT FROM 'object' OR NOT b ? 'memberBytes' OR (SELECT count(*) FROM jsonb_object_keys(b))<>1 THEN RETURN false;END IF;
 vals:=b->'memberBytes';IF vals='null'::jsonb THEN RETURN true;END IF;
 IF kind IS DISTINCT FROM 'group' OR jsonb_typeof(vals) IS DISTINCT FROM 'array' THEN RETURN false;END IF;
 IF jsonb_array_length(vals)>256 THEN RETURN false;END IF;
 FOR v IN SELECT value FROM jsonb_array_elements(vals) LOOP
  IF v='null'::jsonb OR NOT adtr.domain_directory_member_value_v4(v) THEN RETURN false;END IF;
  s:=v#>>'{}';IF s=ANY(seen) THEN RETURN false;END IF;seen:=array_append(seen,s);
  used:=used+octet_length(decode(s,'base64'));IF used>65536 THEN RETURN false;END IF;
 END LOOP;
 RETURN true;
END; $$;
CREATE OR REPLACE FUNCTION adtr.domain_directory_observation_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE t adtr.tasks%ROWTYPE; u adtr.domain_directory_task_uses%ROWTYPE; b jsonb; s jsonb; o jsonb; base jsonb; extra jsonb; k text; classes text[]; expected_kind text; started timestamptz; completed timestamptz;
BEGIN
 PERFORM adtr.task_auth_lock(NEW.tenant_id);
 SELECT * INTO t FROM adtr.tasks WHERE task_id=NEW.task_id FOR UPDATE NOWAIT;
 SELECT * INTO u FROM adtr.domain_directory_task_uses WHERE task_id=NEW.task_id FOR UPDATE NOWAIT;
 IF t.task_id IS NULL OR u.task_id IS NULL OR t.state<>'running' OR t.lease_until IS NULL OR t.lease_until<=clock_timestamp()
 OR NEW.dictionary_version NOT IN (1,2,3,4) OR NEW.dictionary_version IS NULL
 OR t.kind IS DISTINCT FROM (CASE NEW.dictionary_version WHEN 1 THEN 'domain.directory_read' WHEN 2 THEN 'domain.directory_read.v2' WHEN 3 THEN 'domain.directory_read.v3' WHEN 4 THEN 'domain.directory_read.v4' END)
 OR t.payload_version<>1 OR u.purpose IS DISTINCT FROM t.kind OR u.dictionary_version IS DISTINCT FROM NEW.dictionary_version
 OR ROW(t.tenant_id,t.domain_id,t.actor_id,t.lease_owner,t.fencing_token,t.attempt) IS DISTINCT FROM ROW(NEW.tenant_id,NEW.domain_id,NEW.actor_id,NEW.opener_owner,NEW.opener_fencing_token,NEW.opener_attempt)
 OR ROW(u.tenant_id,u.domain_id,u.actor_id,u.opener_owner,u.opener_fencing_token,u.opener_attempt) IS DISTINCT FROM ROW(NEW.tenant_id,NEW.domain_id,NEW.actor_id,NEW.opener_owner,NEW.opener_fencing_token,NEW.opener_attempt)
 OR u.state<>'opened' OR NOT adtr.domain_directory_use_pins(u) OR NOT adtr.domain_directory_use_current(u) THEN RAISE EXCEPTION 'directory publication authority changed' USING ERRCODE='42501';END IF;
 IF octet_length(NEW.body) NOT BETWEEN 1 AND 16777216 OR NEW.object_count NOT BETWEEN 0 AND 10000 OR encode(sha256(NEW.body),'hex') IS DISTINCT FROM NEW.sha256 THEN RAISE EXCEPTION 'invalid directory observation digest or bound' USING ERRCODE='23514';END IF;
 b:=convert_from(NEW.body,'UTF8')::jsonb;
 IF jsonb_typeof(b) IS DISTINCT FROM 'object' OR jsonb_typeof(b->'objects') IS DISTINCT FROM 'array' OR jsonb_typeof(b->'source') IS DISTINCT FROM 'object' THEN RAISE EXCEPTION 'invalid directory observation shape' USING ERRCODE='23514';END IF;
 IF (NEW.dictionary_version=1 AND ((SELECT count(*) FROM jsonb_object_keys(b))<>2 OR b ? 'dictionaryVersion'))
 OR (NEW.dictionary_version IN (2,3,4) AND ((SELECT count(*) FROM jsonb_object_keys(b))<>3 OR b->'dictionaryVersion' IS DISTINCT FROM to_jsonb(NEW.dictionary_version)))
 OR jsonb_array_length(b->'objects')<>NEW.object_count THEN RAISE EXCEPTION 'directory observation profile or count mismatch' USING ERRCODE='23514';END IF;
 s:=b->'source';
 FOREACH k IN ARRAY ARRAY['server_name','dc_host_name','domain','naming_context','started_at','completed_at'] LOOP
  IF jsonb_typeof(s->k) IS DISTINCT FROM 'string' THEN RAISE EXCEPTION 'invalid directory source type' USING ERRCODE='23514';END IF;
 END LOOP;
 IF octet_length(s->>'server_name') NOT BETWEEN 1 AND 253 OR octet_length(s->>'dc_host_name') NOT BETWEEN 1 AND 253 OR octet_length(s->>'domain') NOT BETWEEN 1 AND 253 OR octet_length(s->>'naming_context') NOT BETWEEN 1 AND 4096
 OR jsonb_typeof(s->'pages') IS DISTINCT FROM 'number' OR jsonb_typeof(s->'elapsed_milliseconds') IS DISTINCT FROM 'number'
 OR (s->>'pages')::numeric NOT BETWEEN 1 AND 100 OR (s->>'elapsed_milliseconds')::numeric NOT BETWEEN 0 AND 125000
 OR NOT EXISTS(SELECT FROM adtr.domain_connections c WHERE c.tenant_id=NEW.tenant_id AND c.domain_id=NEW.domain_id AND s->>'domain'=c.canonical_domain AND s->>'server_name'=c.dc_hostname) THEN RAISE EXCEPTION 'directory observation source mismatch' USING ERRCODE='23514';END IF;
 FOREACH k IN ARRAY ARRAY['started_at','completed_at'] LOOP
  IF s->>k !~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}T([01][0-9]|2[0-3]):[0-5][0-9]:[0-5][0-9]([.][0-9]{0,8}[1-9])?(Z|[+-]([01][0-9]|2[0-3]):[0-5][0-9])$' OR right(s->>k,6) IN ('+00:00','-00:00') THEN RAISE EXCEPTION 'noncanonical directory source time' USING ERRCODE='23514';END IF;
 END LOOP;
 started:=(s->>'started_at')::timestamptz;completed:=(s->>'completed_at')::timestamptz;
 IF NOT isfinite(started) OR NOT isfinite(completed) OR started='0001-01-01 00:00:00+00'::timestamptz OR completed<started OR completed-started>interval '125 seconds' THEN RAISE EXCEPTION 'invalid directory source time' USING ERRCODE='23514';END IF;
 FOR o IN SELECT value FROM jsonb_array_elements(b->'objects') LOOP
  IF jsonb_typeof(o) IS DISTINCT FROM 'object' THEN RAISE EXCEPTION 'invalid directory object' USING ERRCODE='23514';END IF;
  IF NEW.dictionary_version IN (2,3,4) THEN
   IF (SELECT count(*) FROM jsonb_object_keys(o))<>(CASE NEW.dictionary_version WHEN 4 THEN 4 WHEN 3 THEN 3 ELSE 2 END) OR NOT o ?& ARRAY['base','supplemental'] THEN RAISE EXCEPTION 'invalid directory object shape' USING ERRCODE='23514';END IF;
   base:=o->'base';extra:=o->'supplemental';
   IF jsonb_typeof(base) IS DISTINCT FROM 'object' OR jsonb_typeof(extra) IS DISTINCT FROM 'object'
   OR NOT adtr.domain_directory_observation_base64_v2(extra->'objectSidBytes',28)
   OR NOT adtr.domain_directory_observation_base64_v2(extra->'mailBytes',1024)
   OR NOT adtr.domain_directory_observation_base64_v2(extra->'whenCreatedBytes',17) THEN RAISE EXCEPTION 'invalid directory supplemental storage' USING ERRCODE='23514';END IF;
   IF extra->'descriptionBytes' IS DISTINCT FROM 'null'::jsonb THEN
    IF jsonb_typeof(extra->'descriptionBytes') IS DISTINCT FROM 'array' THEN RAISE EXCEPTION 'invalid directory description storage' USING ERRCODE='23514';END IF;
    IF jsonb_array_length(extra->'descriptionBytes')<>1 OR extra->'descriptionBytes'->0='null'::jsonb OR NOT adtr.domain_directory_observation_base64_v2(extra->'descriptionBytes'->0,4096) THEN RAISE EXCEPTION 'invalid directory description storage' USING ERRCODE='23514';END IF;
   END IF;
   IF NEW.dictionary_version IN (3,4) AND NOT adtr.domain_directory_identity_v3(o->'identity') THEN RAISE EXCEPTION 'invalid directory identity storage' USING ERRCODE='23514';END IF;
   IF NEW.dictionary_version=4 AND NOT adtr.domain_directory_membership_v4(o->'membership',base->>'kind') THEN RAISE EXCEPTION 'invalid directory membership storage' USING ERRCODE='23514';END IF;
  ELSE base:=o;END IF;
  FOREACH k IN ARRAY ARRAY['objectGUID','distinguishedName','kind'] LOOP
   IF jsonb_typeof(base->k) IS DISTINCT FROM 'string' THEN RAISE EXCEPTION 'invalid directory base type' USING ERRCODE='23514';END IF;
  END LOOP;
  IF base->>'objectGUID' !~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' OR octet_length(base->>'distinguishedName') NOT BETWEEN 1 AND 4096 OR jsonb_typeof(base->'objectClass') IS DISTINCT FROM 'array' THEN RAISE EXCEPTION 'invalid directory base value' USING ERRCODE='23514';END IF;
  IF jsonb_array_length(base->'objectClass') NOT BETWEEN 1 AND 32 OR EXISTS(SELECT FROM jsonb_array_elements(base->'objectClass') v WHERE jsonb_typeof(v) IS DISTINCT FROM 'string' OR v#>>'{}' !~ '^[a-z0-9.-]{1,64}$') THEN RAISE EXCEPTION 'invalid directory classes' USING ERRCODE='23514';END IF;
  SELECT array_agg(value ORDER BY ordinality) INTO classes FROM jsonb_array_elements_text(base->'objectClass') WITH ORDINALITY;
  IF classes IS DISTINCT FROM ARRAY(SELECT value FROM unnest(classes) AS c(value) GROUP BY value ORDER BY value COLLATE "C") OR ('group'=ANY(classes) AND ('user'=ANY(classes) OR 'computer'=ANY(classes))) THEN RAISE EXCEPTION 'invalid directory class order' USING ERRCODE='23514';END IF;
  expected_kind:=CASE WHEN 'computer'=ANY(classes) THEN 'computer' WHEN 'group'=ANY(classes) THEN 'group' WHEN 'user'=ANY(classes) THEN 'user' END;
  IF expected_kind IS NULL OR base->>'kind' IS DISTINCT FROM expected_kind THEN RAISE EXCEPTION 'invalid directory kind' USING ERRCODE='23514';END IF;
  IF base->'samAccountName' IS DISTINCT FROM 'null'::jsonb AND (jsonb_typeof(base->'samAccountName') IS DISTINCT FROM 'string' OR octet_length(base->>'samAccountName') NOT BETWEEN 1 AND 1024 OR length(base->>'samAccountName')>256) THEN RAISE EXCEPTION 'invalid directory SAM name' USING ERRCODE='23514';END IF;
  IF base->'userAccountControl' IS DISTINCT FROM 'null'::jsonb AND (jsonb_typeof(base->'userAccountControl') IS DISTINCT FROM 'number' OR (base->>'userAccountControl')::numeric NOT BETWEEN 0 AND 4294967295) THEN RAISE EXCEPTION 'invalid directory UAC' USING ERRCODE='23514';END IF;
  IF octet_length((CASE WHEN NEW.dictionary_version=4 THEN adtr.domain_directory_observation_canonical_v4(base) ELSE adtr.domain_directory_observation_canonical_v3(base) END))>32768 OR octet_length((CASE WHEN NEW.dictionary_version=4 THEN adtr.domain_directory_observation_canonical_v4(o) ELSE adtr.domain_directory_observation_canonical_v3(o) END))>(CASE NEW.dictionary_version WHEN 4 THEN 262144 WHEN 3 THEN 131072 ELSE 65536 END) THEN RAISE EXCEPTION 'directory object too large' USING ERRCODE='23514';END IF;
 END LOOP;
 IF (SELECT count(DISTINCT CASE NEW.dictionary_version WHEN 1 THEN value->>'objectGUID' ELSE value->'base'->>'objectGUID' END) FROM jsonb_array_elements(b->'objects'))<>NEW.object_count THEN RAISE EXCEPTION 'duplicate directory GUID' USING ERRCODE='23514';END IF;
 IF octet_length((CASE WHEN NEW.dictionary_version=4 THEN adtr.domain_directory_observation_canonical_v4(s) ELSE adtr.domain_directory_observation_canonical_v3(s) END))>8192 OR convert_to((CASE WHEN NEW.dictionary_version=4 THEN adtr.domain_directory_observation_canonical_v4(b) ELSE adtr.domain_directory_observation_canonical_v3(b) END),'UTF8') IS DISTINCT FROM NEW.body THEN RAISE EXCEPTION 'noncanonical directory observation' USING ERRCODE='23514';END IF;
 RETURN NEW;
END; $$;
`
