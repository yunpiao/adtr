"""Injected-host offline contracts. No real APT/dpkg/Node/GPG observation."""
import contextlib, copy, io, json, os, re, stat, tempfile, types, unittest
from pathlib import Path
from unittest.mock import patch
import ci_apt_preflight as p
REPO=Path('/synthetic-repo')
CANARY='SECRET_CANARY_DO_NOT_EMIT'

def config_output(overrides=None):
    values={}
    for name,(_,kind) in p.CONFIG.items():
        values[name]=('true' if name in ('installRecommends','checkDate','checkValidUntil') else 'false') if kind=='bool' else '0' if kind=='int' else 'amd64' if kind=='arch' else kind
    values.update(overrides or {})
    return ''.join(f"{alias}='{values[name]}'\n" for alias,name in p.CONFIG_ALIASES.items() if values[name] is not None).encode()

class FakeHost:
    def __init__(self):
        self.env=dict(GITHUB_SHA='a'*40,GITHUB_RUN_ID='123',GITHUB_RUN_ATTEMPT='4',ImageVersion='20261012.334.1',ImageOS='ubuntu24',RUNNER_OS='Linux',RUNNER_ARCH='X64')
        self.files={str(REPO/path):('SYNTHETIC '+path).encode() for path in p.SOURCE_FILES}
        self.files.update({'/usr/lib/os-release':b'ID=ubuntu\nVERSION_ID="24.04"\nVERSION_CODENAME=noble\n',
          str(REPO/'web/package-lock.json'):json.dumps({'packages':{'node_modules/'+n:{'version':'1.56.1'} for n in ('@playwright/test','playwright','playwright-core')}}).encode(),
          '/etc/apt/sources.list.d/ubuntu.sources':b'Types: deb\nURIs: http://azure.archive.ubuntu.com/ubuntu/\nSuites: noble noble-updates noble-backports noble-security\nComponents: main restricted universe multiverse\nSigned-By: /usr/share/keyrings/ubuntu-archive-keyring.gpg\n',
          '/etc/apt/sources.list.d/microsoft.list':b'deb [arch=amd64,arm64,armhf signed-by=/usr/share/keyrings/microsoft-prod.gpg] https://packages.microsoft.com/ubuntu/24.04/prod noble main\n',
          '/var/lib/dpkg/status':b'SYNTHETIC STATUS', '/var/lib/apt/extended_states':b'SYNTHETIC AUTO'})
        self.commands=[];self.reads=[];self.failures={};self.config=config_output();self.dirty=b''
        self.version=b'apt 2.8.3 (amd64)\nSupported modules: discarded\n'
        self.query=b'libone\tamd64\t1.0-1\tii \nlibheld\tamd64\t1.0-1\thi \nlibresidual\tall\t1.0-1\trc \n'
        self.indexes=[('public_Packages.lz4',stat.S_IFREG|0o644,100,1700000000),('public_InRelease',stat.S_IFREG|0o644,20,1700000001),('partial',stat.S_IFDIR|0o755,4096,1700000002)]
    def read(self,path,limit,optional=False):
        path=str(path);self.reads.append(path)
        if path not in self.files:
            if optional:return None
            raise p.Blocked('file_missing')
        value=self.files[path]
        if isinstance(value,list):value=value.pop(0)
        if isinstance(value,Exception):raise value
        p.require(type(value) is bytes and len(value)<=limit,'file_limit');return value
    def entries(self,path,limit):
        if str(path)=='/etc/apt/sources.list.d':return [(n,stat.S_IFREG|0o644,len(self.files['/etc/apt/sources.list.d/'+n]),1700000000) for n in ('ubuntu.sources','microsoft.list')]
        return self.indexes
    def node_path(self):return '/usr/bin/node'
    def key_fingerprints(self,path):
        p.require(path in p.KEYRINGS,'keyring_unsupported');return 'd'*64,['A'*40],['B'*40]
    def run(self,argv,limit=65536,home=None):
        self.commands.append(argv)
        if argv[0] in self.failures:raise p.Blocked(self.failures[argv[0]])
        if argv[0]=='/usr/bin/git':return b'a'*40+b'\n' if argv[-1]=='HEAD' else b'b'*40+b'\n' if argv[-1]=='HEAD^{tree}' else self.dirty
        if argv[0]=='/usr/bin/apt-get':return self.version
        if argv[0]=='/usr/bin/apt-config':return self.config
        if argv[0]=='/usr/bin/dpkg':return b'amd64\n'
        if argv[0]=='/usr/bin/dpkg-query':return self.query
        if argv[0]=='/usr/bin/node':return b'v20.19.0\n'
        raise AssertionError('unexpected command')

class CollectorContracts(unittest.TestCase):
    def setUp(self):self.host=FakeHost()
    def collect(self):return p.Collector(self.host,REPO).collect()
    def blocked(self,code):
        data=self.collect();self.assertEqual(data['status'],'blocked');self.assertIn(code,data['reasonCodes']);self.assertNotIn(CANARY,json.dumps(data));return data
    def test_observes_original_ubuntu_and_microsoft_without_provisioning(self):
        data=self.collect();self.assertEqual(data['status'],'observed');self.assertEqual(data['sources']['enabledCount'],2)
        self.assertEqual({x['sourceClass'] for x in data['sources']['publicEntries']},{'ubuntu-azure','microsoft-prod'})
        self.assertEqual(data['toolchain']['observedNodeVersion'],'20.19.0');self.assertEqual(data['toolchain']['expectedNodeVersion'],'24.19.0')
        self.assertFalse(data['aptConfig']['fullEffectiveConfigBound'])
    def test_actual_head_tree_and_source_hashes(self):
        d=self.collect()['identity'];self.assertEqual(d['checkoutSHA'],'a'*40);self.assertEqual(d['treeSHA'],'b'*40);self.assertTrue(d['checkoutClean'])
        self.assertEqual(d['workflowSHA256'],p.sha(self.host.files[str(REPO/p.SOURCE_FILES[0])]))
    def test_checkout_env_mismatch_stops_host_observation(self):
        self.host.env['GITHUB_SHA']='c'*40;self.blocked('checkout_mismatch');self.assertTrue(all(c[0]=='/usr/bin/git' for c in self.host.commands))
    def test_dirty_names_not_emitted(self):self.host.dirty=('?? '+CANARY).encode();self.blocked('checkout_dirty')
    def test_identity_fields_fail_closed(self):
        for name in ('GITHUB_RUN_ID','GITHUB_RUN_ATTEMPT','ImageVersion','ImageOS','RUNNER_OS','RUNNER_ARCH'):
            with self.subTest(name=name):self.host=FakeHost();self.host.env[name]=CANARY;self.blocked('identity_invalid')
    def test_source_drift(self):self.host.files[str(REPO/p.SOURCE_FILES[1])]=[b'first',b'second'];self.blocked('source_changed')
    def test_apt_config_any_presence_blocks_all_apt(self):
        for value in ('',CANARY):
            self.host=FakeHost();self.host.env['APT_CONFIG']=value;self.blocked('apt_config_present')
            self.assertFalse(any(c[0] in ('/usr/bin/apt-get','/usr/bin/apt-config') for c in self.host.commands))
    def test_unknown_apt_version(self):self.host.version=CANARY.encode();self.blocked('apt_version_invalid')
    def test_missing_tool_no_install_fallback(self):self.host.failures['/usr/bin/apt-get']='tool_missing';self.blocked('tool_missing')
    def test_unsafe_configuration_not_emitted(self):self.host.config=config_output({'sandboxUser':CANARY});self.blocked('config_invalid')
    def test_insecure_config_flags_not_accepted_or_changed(self):
        for field,value in (('allowUnauthenticated','true'),('allowInsecure','true'),('allowDowngradeInsecure','true'),('noLocking','true'),('checkDate','false'),('checkValidUntil','false')):
            with self.subTest(field=field):self.host=FakeHost();self.host.config=config_output({field:value});self.blocked('insecure_apt_configuration')
    def test_redirected_sources_not_read(self):
        self.host.config=config_output({'sourcesList':'/private/'+CANARY});self.blocked('config_invalid');self.assertFalse(any(CANARY in f for f in self.host.reads))
    def test_unset_config_explicit(self):self.host.config=config_output({'maxFutureTime':None});self.assertEqual(self.collect()['aptConfig']['maxFutureTime'],'unset')
    def source(self,uri,key='/usr/share/keyrings/microsoft-prod.gpg'):
        self.host.files['/etc/apt/sources.list.d/microsoft.list']=f'deb [signed-by={key}] {uri} noble main\n'.encode()
    def test_credential_source_only_closed_flag(self):
        self.source('https://'+CANARY+'@packages.microsoft.com/ubuntu/24.04/prod');self.assertTrue(self.blocked('source_credential_like')['sources']['credentialLikeSourcePresent'])
    def test_unknown_enabled_source_not_silently_omitted(self):
        self.source('https://'+CANARY+'.invalid/prod');d=self.blocked('source_unknown');self.assertTrue(d['sources']['unknownSourcePresent']);self.assertIsNone(d['sources']['enabledCount'])
    def test_implicit_trust_not_guessed(self):
        self.host.files['/etc/apt/sources.list.d/microsoft.list']=b'deb https://packages.microsoft.com/ubuntu/24.04/prod noble main\n';self.assertTrue(self.blocked('source_implicit_trust')['sources']['implicitTrustUnresolved'])
    def test_arbitrary_signedby_not_read(self):
        self.source('https://packages.microsoft.com/ubuntu/24.04/prod','/etc/apt/auth.conf');self.blocked('keyring_unsupported');self.assertNotIn('/etc/apt/auth.conf',self.host.reads)
    def installed(self,name,version='1.56.1'):self.host.files[str(REPO/'web/node_modules'/name/'package.json')]=json.dumps(dict(name=name,version=version)).encode()
    def test_real_installed_metadata_required(self):
        for name in ('@playwright/test','playwright','playwright-core'):self.installed(name)
        d=self.collect()['toolchain'];self.assertTrue(d['playwrightInstalledObserved']);self.assertEqual(d['observedPlaywrightVersion'],'1.56.1')
    def test_node_modules_presence_not_installed_proof(self):
        self.host.files[str(REPO/'web/node_modules')]=b'directory only';d=self.collect()['toolchain'];self.assertFalse(d['playwrightInstalledObserved']);self.assertIsNone(d['observedPlaywrightVersion'])
    def test_partial_installed_metadata(self):self.installed('playwright');self.blocked('installed_playwright_invalid')
    def test_wrong_installed_metadata(self):self.installed('playwright',CANARY);self.blocked('installed_playwright_invalid')
    def test_lock_version_drift(self):self.host.files[str(REPO/'web/package-lock.json')]=b'{"packages":{}}';self.blocked('lock_invalid')
    def test_complete_dpkg_hash_and_status_counts(self):
        d=self.collect()['installedState'];self.assertEqual((d['rowCount'],d['configuredCount'],d['heldCount'],d['residualConfigCount'],d['unconfiguredCount']),(3,2,1,1,0));self.assertTrue(d['baselineStable']);self.assertNotIn('libheld',json.dumps(d))
    def test_unconfigured_is_observed_not_satisfied(self):self.host.query+=b'libpending\tamd64\t1.0-1\tiU \n';self.assertEqual(self.collect()['installedState']['unconfiguredCount'],1)
    def test_status_drift(self):self.host.files['/var/lib/dpkg/status']=[b'first',b'second'];self.assertFalse(self.blocked('installed_state_changed')['installedState']['baselineStable'])
    def test_auto_state_drift(self):self.host.files['/var/lib/apt/extended_states']=[b'first',b'second'];self.blocked('installed_state_changed')
    def test_absent_auto_state_explicit(self):
        del self.host.files['/var/lib/apt/extended_states'];d=self.collect()['installedState'];self.assertIsNone(d['extendedStatesSHA256']);self.assertEqual(d['extendedStatesBytes'],0)
    def test_dpkg_duplicate_or_malformed_rows(self):
        for bad in (self.host.query+self.host.query,b'bad\n',f'{CANARY}\tamd64\t1\tii \n'.encode()):
            self.host=FakeHost();self.host.query=bad;self.blocked('installed_state_invalid')
    def test_index_statistics_are_not_fresh_transfer_measurement(self):
        d=self.collect()['indexes'];self.assertEqual(d['totalBytes'],120);self.assertEqual(d['regularFileCount'],2);self.assertEqual(d['measurement'],'preexisting-local-index-stat-only')
    def test_index_names_not_output(self):
        self.host.indexes.append((CANARY+'_Packages',stat.S_IFREG|0o644,5,1700000000));d=self.collect();self.assertNotIn(CANARY,json.dumps(d));self.assertEqual(d['indexes']['totalBytes'],125)
    def test_symlink_index_fails(self):self.host.indexes.append(('bad',stat.S_IFLNK|0o777,2,1700000000));self.blocked('index_invalid')
    def test_commands_queries_paths_are_closed(self):
        self.collect()
        for command in self.host.commands:
            for forbidden in ('dump','sudo','update','install','download'):self.assertNotIn(forbidden,command)
        self.assertFalse(any('/auth.conf' in f for f in self.host.reads))
        c=next(c for c in self.host.commands if c[0]=='/usr/bin/apt-config');self.assertEqual(c[1],'shell');self.assertEqual(len(c),2+2*len(p.CONFIG))
    def test_git_no_optional_writes_or_external_hooks(self):
        self.collect()
        for c in self.host.commands:
            if c[0]=='/usr/bin/git':
                for token in ('--no-optional-locks','core.fsmonitor=false','core.untrackedCache=false','core.hooksPath=/dev/null','submodule.recurse=false'):self.assertIn(token,c)
    def test_receipt_closed_keys_types_and_status(self):
        good=self.collect()
        for section,key,value in ((None,'extra',CANARY),(None,'schema',True),('indexes','totalBytes',True),('toolchain','observedNodeVersion',CANARY),('identity','checkoutSHA',CANARY),(None,'reasonCodes',['source_unknown'])):
            bad=copy.deepcopy(good);(bad if section is None else bad[section])[key]=value
            with self.subTest(key=key),self.assertRaises(p.Blocked):p.validate_receipt(bad)

class ParserContracts(unittest.TestCase):
    def test_exact_public_classes(self):
        for key,value in p.PUBLIC_SOURCES.items():self.assertEqual(p.source_class(key[0]+'://'+key[1]+key[2]+'/'),value)
    def test_credential_query_fragment_encoded_unknown_uri(self):
        for uri in ('https://user:password@packages.microsoft.com/ubuntu/24.04/prod','https://packages.microsoft.com/ubuntu/24.04/prod?token='+CANARY,'https://packages.microsoft.com/ubuntu/24.04/prod#'+CANARY,'https://packages.microsoft.com/%75buntu/24.04/prod','https://packages.microsoft.com:443/ubuntu/24.04/prod','https://unknown.invalid/prod'):
            with self.assertRaises(p.Blocked):p.source_class(uri)
    def test_duplicate_source_fields(self):
        with self.assertRaises(p.Blocked):p.parse_source_file(b'Types: deb\nTypes: deb\n','.sources')
    def test_fragment_never_stripped_as_comment(self):
        with self.assertRaises(p.Blocked):p.parse_source_file(b'deb [signed-by=/usr/share/keyrings/microsoft-prod.gpg] https://packages.microsoft.com/ubuntu/24.04/prod#private noble main\n','.list')
    def test_unrecognized_options_not_ignored(self):
        with self.assertRaises(p.Blocked):p.parse_source_file(b'deb [trusted=yes signed-by=/usr/share/keyrings/microsoft-prod.gpg] https://packages.microsoft.com/ubuntu/24.04/prod noble main\n','.list')
    def test_apt_shell_output_never_eval(self):
        for raw in (b"P0='$(cat secret)'\n",b"P0='amd64'\nP0='amd64'\n",b"UNKNOWN='true'\n",b"P0='amd64';echo bad\n"):
            with self.assertRaises(p.Blocked):p.parse_config(raw)
    def key(self,kind='pub'):
        return f'{kind}:-:4096:1:1234567890ABCDEF:1577836800:0:::::sc:\nfpr:::::::::'.encode()+b'A'*40+b':\nuid:-::::1577836800:::'+CANARY.encode()+b':\n'
    def test_key_uid_not_exported(self):
        result=p.parse_keys(self.key());self.assertEqual(result,(['A'*40],[]));self.assertNotIn(CANARY,json.dumps(result))
    def test_secret_key_records_fail(self):
        for kind in ('sec','ssb'):
            with self.assertRaisesRegex(p.Blocked,'secret_key_rejected'):p.parse_keys(self.key(kind))
    def test_key_unknown_algorithm_missing_fingerprint_record(self):
        for raw in (self.key().replace(b':4096:1:',b':4096:99:'),b'pub:-:4096:1:KEY:1577836800:0:::::sc:\n',self.key()+b'unknown:private\n'):
            with self.assertRaises(p.Blocked):p.parse_keys(raw)
    def test_index_resource_limits(self):
        with self.assertRaises(p.Blocked):p.index_summary([('x',stat.S_IFREG,1,0)]*10001)
        with self.assertRaises(p.Blocked):p.index_summary([('x',stat.S_IFREG,4*1024**3+1,0)])
    def test_json_duplicates_nonfinite_deep(self):
        for raw in (b'{"x":1,"x":2}',b'{"x":NaN}',b'['*2000+b'0'+b']'*2000):
            with self.assertRaises(p.Blocked):p.strict_json(raw)

class FilesystemContracts(unittest.TestCase):
    def setUp(self):self.tmp=tempfile.TemporaryDirectory(prefix='adtr-preflight-contract-');self.root=Path(self.tmp.name);self.host=p.Host(env={})
    def tearDown(self):self.tmp.cleanup()
    def test_synthetic_safe_read(self):
        f=self.root/'safe';f.write_bytes(b'public');self.assertEqual(self.host.read(f,6),b'public')
    def test_symlink_parent_or_file_fail(self):
        f=self.root/'safe';f.write_bytes(b'public');link=self.root/'link';link.symlink_to(f)
        with self.assertRaises(p.Blocked):self.host.read(link,100)
        parent=self.root/'parent';parent.symlink_to(self.root,target_is_directory=True)
        with self.assertRaises(p.Blocked):self.host.read(parent/'safe',100)
    def test_hardlink_or_oversize_fail(self):
        f=self.root/'safe';f.write_bytes(b'public')
        with self.assertRaises(p.Blocked):self.host.read(f,1)
        os.link(f,self.root/'hard')
        with self.assertRaises(p.Blocked):self.host.read(f,100)
    def test_optional_missing(self):self.assertIsNone(self.host.read(self.root/'missing',1,optional=True))
    def test_directory_count_bound(self):
        for i in range(3):(self.root/str(i)).write_bytes(b'x')
        with self.assertRaisesRegex(p.Blocked,'directory_limit'):self.host.entries(self.root,2)
    def paths(self):
        (self.root/'_runner_file_commands').mkdir();m=self.root/'_runner_file_commands'/'set_output_00000000-0000-0000-0000-000000000000';m.write_bytes(b'');return self.root/'adtr-apt-preflight.json',m
    def test_exclusive_receipt_then_marker(self):
        path,marker=self.paths();data=p.Collector(FakeHost(),REPO).collect();p.write_receipt(path,data,marker)
        self.assertEqual(json.loads(path.read_bytes()),data);self.assertEqual(marker.read_bytes(),b'receipt_written=true\n')
    def test_stale_existing_output_not_marked_or_replaced(self):
        path,marker=self.paths();path.write_bytes(b'OLD SUCCESS')
        with self.assertRaisesRegex(p.Blocked,'output_exists'):p.write_receipt(path,p.Collector(FakeHost(),REPO).collect(),marker)
        self.assertEqual(path.read_bytes(),b'OLD SUCCESS');self.assertEqual(marker.read_bytes(),b'')
    def test_symlink_output_not_followed(self):
        path,marker=self.paths();target=self.root/'target';target.write_bytes(b'old');path.symlink_to(target)
        with self.assertRaises(p.Blocked):p.write_receipt(path,p.Collector(FakeHost(),REPO).collect(),marker)
        self.assertEqual(target.read_bytes(),b'old');self.assertEqual(marker.read_bytes(),b'')
    def test_nonempty_marker_rejected(self):
        path,marker=self.paths();marker.write_bytes(b'receipt_written=true\n')
        with self.assertRaisesRegex(p.Blocked,'output_unsafe'):p.write_receipt(path,p.Collector(FakeHost(),REPO).collect(),marker)
        self.assertFalse(path.exists())
    def test_global_deadline_before_read(self):
        times=iter((0,120));host=p.Host(env={},clock=lambda:next(times))
        with self.assertRaisesRegex(p.Blocked,'deadline'):host.read(self.root/'not-opened',1)

class WorkflowContracts(unittest.TestCase):
    def test_exact_one_branch_job_readonly_budget_pins(self):
        raw=Path('.github/workflows/ci-apt-preflight.yml').read_text()
        self.assertIn("branches: ['ci/apt-preflight-20261026']",raw);self.assertIn("github.repository == 'yunpiao/adtr' && github.ref == 'refs/heads/ci/apt-preflight-20261026'",raw)
        self.assertEqual(re.findall(r'^  ([a-z-]+):$',raw.split('jobs:\n',1)[1],re.M),['observe']);self.assertIn('    timeout-minutes: 5\n',raw);self.assertIn('permissions: {}\n',raw)
        self.assertEqual(raw.count('      contents: read\n'),1)
        for token in ('pull_request:','workflow_dispatch:','schedule:','sudo ','npm ','setup-node','setup-go','download-artifact','apt-get ','secrets.','continue-on-error','branches: [main]'):self.assertNotIn(token,raw)
        self.assertEqual(re.findall(r'uses: ([^\n ]+)',raw),['actions/checkout@11bd71901bbe5b1630ceea73d27597364c9af683','actions/upload-artifact@ea165f8d65b6e75b540449e92b4886f43607fa02'])
    def test_current_receipt_marker_exact_upload(self):
        raw=Path('.github/workflows/ci-apt-preflight.yml').read_text();self.assertIn("if: ${{ always() && steps.collect.outputs.receipt_written == 'true' }}",raw)
        self.assertIn('path: ${{ runner.temp }}/adtr-apt-preflight.json\n',raw);self.assertIn('if-no-files-found: error\n',raw);self.assertIn('retention-days: 1\n',raw);self.assertIn('python3 -I scripts/ci_apt_preflight.py',raw)
    def test_cli_argument_error_closed(self):
        out=io.StringIO()
        with contextlib.redirect_stdout(out):code=p.main(['--wrong-'+CANARY],host=FakeHost())
        self.assertEqual(code,1);self.assertNotIn(CANARY,out.getvalue())


# No child actually launches: pipes, selector, clock and Popen are all injected.
class FakePipe:
    def __init__(self,number):self.number=number;self.closed=False
    def fileno(self):return self.number
    def close(self):self.closed=True
class FakeProcess:
    def __init__(self):self.stdout=FakePipe(101);self.stderr=FakePipe(102);self.pid=123456789;self.done=False;self.code=0
    def poll(self):return self.code if self.done else None
    def wait(self,timeout):self.done=True;return self.code
class FakeSelector:
    def __init__(self):self.items={}
    def __enter__(self):return self
    def __exit__(self,*a):pass
    def register(self,pipe,event):self.items[pipe]=types.SimpleNamespace(fileobj=pipe)
    def unregister(self,pipe):del self.items[pipe]
    def get_map(self):return self.items
    def select(self,timeout):return [(value,1) for value in list(self.items.values())]

class ResourceContracts(unittest.TestCase):
    def run_injected(self,host,chunks=None,code=0,limit=65536):
        process=FakeProcess();process.code=code;chunks={101:[b'ok',b''],102:[b'']} if chunks is None else chunks
        stack=contextlib.ExitStack();self.addCleanup(stack.close)
        popen=stack.enter_context(patch.object(p.subprocess,'Popen',return_value=process))
        stack.enter_context(patch.object(p.selectors,'DefaultSelector',FakeSelector))
        stack.enter_context(patch.object(p.os,'set_blocking'))
        stack.enter_context(patch.object(p.os,'read',side_effect=lambda fd,size:chunks[fd].pop(0)))
        killed=stack.enter_context(patch.object(p.os,'killpg'))
        return process,popen,killed,lambda:host.run(['/usr/bin/apt-get','--version'],limit=limit)
    def test_child_environment_is_fixed_and_injection_free(self):
        host=p.Host(env={'PYTHONPATH':CANARY,'PYTHONHOME':CANARY,'GNUPGHOME':CANARY,'GPG_AGENT_INFO':CANARY,'NODE_OPTIONS':CANARY,'LD_PRELOAD':CANARY,'LD_LIBRARY_PATH':CANARY,'GIT_CONFIG_COUNT':'1','TOKEN':CANARY},clock=lambda:0)
        process,popen,killed,run=self.run_injected(host);self.assertEqual(run(),b'ok')
        env=popen.call_args.kwargs['env'];self.assertNotIn(CANARY,json.dumps(env));self.assertEqual(env['LC_ALL'],'C');self.assertEqual(env['GIT_CONFIG_NOSYSTEM'],'1');self.assertEqual(env['GIT_CONFIG_GLOBAL'],'/dev/null')
        self.assertTrue(popen.call_args.kwargs['start_new_session']);self.assertTrue(process.stdout.closed and process.stderr.closed);killed.assert_not_called()
    def test_live_stdout_stderr_limit_kills_owned_group(self):
        process,popen,killed,run=self.run_injected(p.Host(env={},clock=lambda:0),{101:[b'x'*5],102:[b'y'*5]},limit=8)
        with self.assertRaisesRegex(p.Blocked,'output_limit'):run()
        killed.assert_called_once_with(process.pid,p.signal.SIGKILL);self.assertTrue(process.stdout.closed)
    def test_aggregate_output_budget_across_commands(self):
        host=p.Host(env={},clock=lambda:0);host.output_bytes=20*1024*1024
        _,_,killed,run=self.run_injected(host)
        with self.assertRaisesRegex(p.Blocked,'output_limit'):run()
        killed.assert_called_once()
    def test_child_timeout_injected_clock(self):
        times=iter((0,0,0,16));host=p.Host(env={},clock=lambda:next(times))
        process,_,killed,run=self.run_injected(host)
        with self.assertRaisesRegex(p.Blocked,'command_timeout'):run()
        killed.assert_called_once_with(process.pid,p.signal.SIGKILL)
    def test_whole_deadline_shared_by_all_commands(self):
        host=p.Host(env={},clock=lambda:0);host.clock=lambda:119
        _,popen,_,run=self.run_injected(host)
        with self.assertRaisesRegex(p.Blocked,'deadline'):run()
        popen.assert_not_called()
    def test_nonzero_stderr_not_leaked(self):
        _,_,_,run=self.run_injected(p.Host(env={},clock=lambda:0),{101:[b''],102:[CANARY.encode(),b'']},code=1)
        with self.assertRaisesRegex(p.Blocked,'command_failed') as caught:run()
        self.assertNotIn(CANARY,str(caught.exception))
    def test_every_apt_invocation_checks_empty_config_presence(self):
        host=p.Host(env={'APT_CONFIG':''},clock=lambda:0)
        _,popen,_,run=self.run_injected(host)
        with self.assertRaisesRegex(p.Blocked,'apt_config_present'):run()
        popen.assert_not_called()
    def test_public_key_command_no_agent_no_key_retrieval(self):
        host=p.Host(env={},clock=lambda:0)
        output=ParserContracts().key()
        with patch.object(host,'read',return_value=b'SYNTHETIC PUBLIC BYTES'),patch.object(host,'run',return_value=output) as run:
            digest,primary,sub=host.key_fingerprints('/usr/share/keyrings/ubuntu-archive-keyring.gpg')
        command=run.call_args.args[0]
        for option in ('--no-options','--no-autostart','--no-auto-key-retrieve','--show-keys','--with-colons'):self.assertIn(option,command)
        self.assertNotIn('--import',command);self.assertNotIn('--generate-key',command);self.assertEqual(primary,['A'*40])
    def test_missing_critical_authenticated_fields_cannot_be_observed(self):
        data=p.Collector(FakeHost(),REPO).collect();data['sources']['publicEntries'][0]['keyringSHA256']=None
        with self.assertRaises(p.Blocked):p.validate_receipt(data)
    def test_disabled_credential_source_also_closed_rejection(self):
        raw=f'Enabled: no\nTypes: deb\nURIs: https://{CANARY}@packages.microsoft.com/ubuntu/24.04/prod\n'.encode()
        with self.assertRaisesRegex(p.Blocked,'source_credential_like'):p.parse_source_file(raw,'.sources')


class LeaderExitCleanupContracts(unittest.TestCase):
    run_injected=ResourceContracts.run_injected
    def test_leader_exited_but_descendant_pipe_open_timeout_kills_group(self):
        times=iter((0,0,0,16));host=p.Host(env={},clock=lambda:next(times))
        process,_,killed,run=self.run_injected(host);process.done=True
        with self.assertRaisesRegex(p.Blocked,'command_timeout'):run()
        killed.assert_called_once_with(process.pid,p.signal.SIGKILL)
    def test_leader_exited_pipe_output_overflow_kills_group(self):
        process,_,killed,run=self.run_injected(p.Host(env={},clock=lambda:0),{101:[b'x'*9],102:[b'']},limit=8);process.done=True
        with self.assertRaisesRegex(p.Blocked,'output_limit'):run()
        killed.assert_called_once_with(process.pid,p.signal.SIGKILL)
    def test_cleanup_failure_retains_first_error_and_closed_flag(self):
        host=p.Host(env={},clock=lambda:0)
        process,_,killed,run=self.run_injected(host,{101:[b'x'*9],102:[b'']},limit=8);process.done=True
        killed.side_effect=PermissionError('SYNTHETIC '+CANARY)
        with self.assertRaisesRegex(p.Blocked,'output_limit'):run()
        self.assertTrue(host.cleanup_failed)
        fake=FakeHost();fake.cleanup_failed=True;data=p.Collector(fake,REPO).collect()
        self.assertEqual(data['status'],'blocked');self.assertIn('cleanup_failed',data['reasonCodes']);self.assertNotIn(CANARY,json.dumps(data))


PROBE_WORKFLOW_SHA256='1715f6c523a9b9d17e3a7dc63d67c186f0f768edeb208b41e373689719fc5007'

def require_exact_probe_workflow(raw):
    if type(raw) is not bytes or p.sha(raw)!=PROBE_WORKFLOW_SHA256:
        raise ValueError('probe workflow differs from exact reviewed scope')

class ExactWorkflowScopeContracts(unittest.TestCase):
    def test_complete_exact_workflow_hash(self):
        require_exact_probe_workflow(Path('.github/workflows/ci-apt-preflight.yml').read_bytes())
    def test_mutations_cannot_expand_branch_event_permission_budget_or_artifact(self):
        raw=Path('.github/workflows/ci-apt-preflight.yml').read_bytes()
        for before,after in ((b'on:\n',b'on:\n  pull_request:\n'),
            (b"branches: ['ci/apt-preflight-20261026']",b"branches: ['ci/apt-preflight-20261026', main]"),
            (b'  push:\n',b'  push:\n    tags: [v*]\n'),
            (b'permissions: {}',b'permissions: {contents: write}'),
            (b'contents: read',b'contents: write'),
            (b'timeout-minutes: 5',b'timeout-minutes: 6'),
            (b"always() && steps.collect.outputs.receipt_written == 'true'",b'always()'),
            (b'/adtr-apt-preflight.json',b'/*')):
            changed=raw.replace(before,after,1);self.assertNotEqual(changed,raw)
            with self.subTest(before=before),self.assertRaises(ValueError):require_exact_probe_workflow(changed)
    def test_any_extra_bytes_and_truncated_workflow_fail(self):
        raw=Path('.github/workflows/ci-apt-preflight.yml').read_bytes()
        for value in (raw+b'\n',raw[:-1],raw.replace(b'\n',b'\r\n')):
            with self.assertRaises(ValueError):require_exact_probe_workflow(value)


class DiagnosticContracts(unittest.TestCase):
    """Closed diagnostics explain rejection; no host observation or weaker policy."""
    def collect(self,host=None):return p.Collector(host or FakeHost(),REPO).collect()
    def assert_closed(self,data):
        p.validate_receipt(data);self.assertNotIn(CANARY,json.dumps(data))
    def source_case(self,raw,kind='.list'):
        host=FakeHost()
        if kind=='.list':host.files['/etc/apt/sources.list.d/microsoft.list']=raw
        else:host.files['/etc/apt/sources.list.d/ubuntu.sources']=raw
        result=self.collect(host);self.assert_closed(result);return result
    def node_path_case(self,path_env,exists=True,target='/usr/bin/node',uid=0,mode=stat.S_IFREG|0o755,error=None):
        host=p.Host(env={'PATH':path_env})
        def resolve(path,strict=False):
            if error:raise error
            return Path(target)
        with patch.object(p.Path,'exists',return_value=exists),patch.object(p.Path,'resolve',resolve),patch.object(p.Path,'stat',return_value=types.SimpleNamespace(st_uid=uid,st_mode=mode)):
            return host.node_path()
    def assert_node_path_block(self,expected,**kwargs):
        with self.assertRaises(p.Blocked) as raised:self.node_path_case(**kwargs)
        self.assertEqual(str(raised.exception),'node_unsupported');self.assertEqual(raised.exception.diagnostic,expected)
    def test_node_path_absent_and_unapproved_path_are_distinct_missing(self):
        for path in ('','/private/'+CANARY):self.assert_node_path_block('path_missing',path_env=path)
    def test_node_allowed_path_without_candidate(self):self.assert_node_path_block('candidate_missing',path_env='/usr/bin',exists=False)
    def test_node_resolved_target_scope_still_rejected(self):self.assert_node_path_block('target_path_unsupported',path_env='/usr/bin',target='/private/'+CANARY)
    def test_node_owner_and_writable_modes_still_rejected(self):
        for uid,mode in ((1001,stat.S_IFREG|0o755),(0,stat.S_IFREG|0o775),(0,stat.S_IFREG|0o757)):
            self.assert_node_path_block('owner_mode_unsupported',path_env='/usr/bin',uid=uid,mode=mode)
    def test_node_nonregular_target_still_rejected(self):self.assert_node_path_block('target_type_unsupported',path_env='/usr/bin',mode=stat.S_IFDIR|0o755)
    def test_node_root_owned_allowed_paths_unchanged(self):
        for path in ('/usr/bin','/usr/local/bin','/opt/hostedtoolcache/node/20.19.0/x64/bin'):
            self.assertEqual(self.node_path_case(path,target=path+'/node'),path+'/node')
    def test_node_path_exception_is_closed_not_raw(self):
        host=FakeHost();host.node_path=lambda:(_ for _ in ()).throw(OSError(CANARY))
        data=self.collect(host);self.assert_closed(data);self.assertEqual(data['toolchain']['nodeObservation'],'path_check_failed');self.assertIn('unexpected_failure',data['reasonCodes'])
    def test_node_path_diagnostics_propagate_without_version_query(self):
        for state in ('path_missing','candidate_missing','target_path_unsupported','owner_mode_unsupported','target_type_unsupported'):
            host=FakeHost();host.node_path=lambda state=state:(_ for _ in ()).throw(p.Blocked('node_unsupported',state))
            data=self.collect(host);self.assert_closed(data);self.assertEqual(data['toolchain']['nodeObservation'],state)
            self.assertFalse(any(c[0]=='/usr/bin/node' for c in host.commands));self.assertIsNone(data['toolchain']['observedNodeVersion'])
    def test_node_version_query_failure_retains_original_codes(self):
        for code in ('tool_missing','command_failed','command_timeout','output_limit','deadline'):
            host=FakeHost();host.failures['/usr/bin/node']=code;data=self.collect(host)
            self.assert_closed(data);self.assertEqual(data['toolchain']['nodeObservation'],'version_query_failed');self.assertIn(code,data['reasonCodes'])
    def test_node_version_format_failure_never_emits_output(self):
        for version in (CANARY.encode(),b'v24.19.0-rc.1\n',b'v20.19.0\r\n',b'20.19.0\n',b'v20.19.0\nextra'):
            host=FakeHost();run=host.run;host.run=lambda argv,limit=65536,home=None:version if argv[0]=='/usr/bin/node' else run(argv,limit,home)
            data=self.collect(host);self.assert_closed(data);self.assertEqual(data['toolchain']['nodeObservation'],'version_format_unsupported');self.assertIn('node_unsupported',data['reasonCodes'])
    def test_node_non24_stable_version_is_observation_not_requirement(self):
        data=self.collect();self.assertEqual(data['toolchain']['nodeObservation'],'observed');self.assertEqual(data['toolchain']['observedNodeVersion'],'20.19.0');self.assertEqual(data['toolchain']['expectedNodeVersion'],'24.19.0')
    def test_identity_block_means_all_source_flags_unknown(self):
        host=FakeHost();host.env['GITHUB_SHA']='c'*40;data=self.collect(host);self.assert_closed(data)
        self.assertEqual(data['sources']['observation'],'not_observed');self.assertEqual(data['toolchain']['nodeObservation'],'not_observed')
        self.assertTrue(all(data['sources'][f] is None for f in p.SOURCE_FLAGS))
    def test_config_block_is_not_source_absence_proof(self):
        host=FakeHost();host.env['APT_CONFIG']='';data=self.collect(host);self.assert_closed(data)
        self.assertEqual(data['sources']['observation'],'config_unavailable');self.assertTrue(all(data['sources'][f] is None for f in p.SOURCE_FLAGS))
    def test_source_read_failure_keeps_unknown_without_partial_counts(self):
        host=FakeHost();host.files['/etc/apt/sources.list.d/ubuntu.sources']=p.Blocked('file_changed')
        # FakeHost.entries normally sizes the bytes; this injected listing is fixed.
        host.entries=lambda path,limit:[('ubuntu.sources',stat.S_IFREG|0o644,10,0)] if str(path)=='/etc/apt/sources.list.d' else host.indexes
        data=self.collect(host);self.assert_closed(data);self.assertEqual(data['sources']['observation'],'source_read_failed')
        self.assertTrue(all(data['sources'][f] is None for f in p.SOURCE_FLAGS));self.assertIsNone(data['sources']['enabledCount']);self.assertEqual(data['sources']['publicEntries'],[])
    def test_source_uri_predicate_diagnostics_keep_original_rejection(self):
        cases=[('https://[bad','uri_invalid','source_invalid'),('https://'+CANARY+'@packages.microsoft.com/ubuntu/24.04/prod','credential_like','source_credential_like'),
            ('https://packages.microsoft.com:443/ubuntu/24.04/prod','uri_authority_unsupported','source_unknown'),
            ('ftp://packages.microsoft.com/ubuntu/24.04/prod','uri_scheme_unsupported','source_unknown'),
            ('https://unknown.invalid/prod','uri_host_unsupported','source_unknown'),
            ('https://packages.microsoft.com/'+CANARY,'uri_path_unsupported','source_unknown')]
        for uri,stage,code in cases:
            with self.subTest(stage=stage):
                raw=f'deb [signed-by=/usr/share/keyrings/microsoft-prod.gpg] {uri} noble main\n'.encode();data=self.source_case(raw)
                self.assertEqual(data['sources']['observation'],stage);self.assertIn(code,data['reasonCodes'])
                self.assertIsNone(data['sources']['enabledCount']);self.assertEqual(data['sources']['publicEntries'],[])
    def test_source_uri_canaries_never_become_diagnostics(self):
        for suffix in ('?token='+CANARY,'#'+CANARY,'/%'+CANARY):
            raw=('deb [signed-by=/usr/share/keyrings/microsoft-prod.gpg] https://packages.microsoft.com/ubuntu/24.04/prod'+suffix+' noble main\n').encode()
            data=self.source_case(raw);self.assertEqual(data['sources']['observation'],'credential_like')
            self.assertIs(data['sources']['credentialLikeSourcePresent'],True);self.assertIsNone(data['sources']['unknownSourcePresent']);self.assertIsNone(data['sources']['implicitTrustUnresolved'])
    def test_source_options_suites_components_architecture_stay_closed(self):
        base='deb [arch=amd64 signed-by=/usr/share/keyrings/microsoft-prod.gpg] https://packages.microsoft.com/ubuntu/24.04/prod noble main\n'
        cases=[(base.replace('arch=amd64','trusted='+CANARY),'list_option_unsupported'),(base.replace(' noble ',' '+CANARY+' '),'suites_unsupported'),
            (base.replace(' main\n',' '+CANARY+'\n'),'components_unsupported'),(base.replace('arch=amd64','arch='+CANARY),'architectures_unsupported')]
        for text,stage in cases:
            data=self.source_case(text.encode());self.assertEqual(data['sources']['observation'],stage);self.assertIn('source_unknown',data['reasonCodes']);self.assertTrue(data['sources']['unknownSourcePresent'])
    def test_source_deb822_unknown_field_and_syntax(self):
        base=b'Types: deb\nURIs: http://azure.archive.ubuntu.com/ubuntu/\nSuites: noble\nComponents: main\nSigned-By: /usr/share/keyrings/ubuntu-archive-keyring.gpg\n'
        for raw,stage,code in ((base+b'Unexpected: '+CANARY.encode()+b'\n','deb822_field_unsupported','source_unknown'),
                (base+b'Types: deb\n','deb822_syntax_invalid','source_invalid')):
            data=self.source_case(raw,'.sources');self.assertEqual(data['sources']['observation'],stage);self.assertIn(code,data['reasonCodes'])
    def test_source_entry_and_types_diagnostics(self):
        base=b'Types: deb\nURIs: http://azure.archive.ubuntu.com/ubuntu/\nSuites: noble\nComponents: main\nSigned-By: /usr/share/keyrings/ubuntu-archive-keyring.gpg\n'
        for raw,stage in ((base.replace(b'Types: deb',b'Types: invalid'),'types_invalid'),(base.replace(b'Suites: noble',b'Suites: noble noble'),'entry_invalid')):
            data=self.source_case(raw,'.sources');self.assertEqual(data['sources']['observation'],stage);self.assertIn('source_invalid',data['reasonCodes'])
    def test_source_list_syntax_and_implicit_trust_and_keyring(self):
        cases=[((CANARY+' malformed\n').encode(),'list_syntax_invalid','source_invalid'),
            (b'deb https://packages.microsoft.com/ubuntu/24.04/prod noble main\n','implicit_trust','source_implicit_trust'),
            (f'deb [signed-by=/private/{CANARY}] https://packages.microsoft.com/ubuntu/24.04/prod noble main\n'.encode(),'keyring_unsupported','keyring_unsupported')]
        for raw,stage,code in cases:
            data=self.source_case(raw);self.assertEqual(data['sources']['observation'],stage);self.assertIn(code,data['reasonCodes'])
    def test_source_first_rejection_stops_before_later_credential(self):
        host=FakeHost();host.files['/etc/apt/sources.list.d/ubuntu.sources']=b'Types: deb\nUnknown: '+CANARY.encode()+b'\n'
        host.files['/etc/apt/sources.list.d/microsoft.list']=f'deb [signed-by=/usr/share/keyrings/microsoft-prod.gpg] https://{CANARY}@packages.microsoft.com/ubuntu/24.04/prod noble main\n'.encode()
        data=self.collect(host);self.assert_closed(data);self.assertEqual(data['sources']['observation'],'deb822_field_unsupported')
        self.assertEqual(data['reasonCodes'],['source_unknown']);self.assertIsNone(data['sources']['credentialLikeSourcePresent'])
    def test_source_partial_key_success_stays_unknown_and_discards_entries(self):
        host=FakeHost();calls=[]
        def keys(path):
            calls.append(path)
            if len(calls)==2:raise p.Blocked('key_invalid')
            return 'd'*64,['A'*40],[]
        host.key_fingerprints=keys;data=self.collect(host);self.assert_closed(data)
        self.assertEqual(len(calls),2);self.assertEqual(data['sources']['observation'],'key_observation_failed');self.assertIsNone(data['sources']['enabledCount']);self.assertEqual(data['sources']['publicEntries'],[])
        self.assertTrue(all(data['sources'][f] is None for f in p.SOURCE_FLAGS))
    def test_source_generic_parse_and_empty_set_stages(self):
        host=FakeHost()
        with patch.object(p,'parse_source_file',side_effect=RuntimeError(CANARY)):
            data=self.collect(host);self.assert_closed(data);self.assertEqual(data['sources']['observation'],'source_parse_failed');self.assertIn('unexpected_failure',data['reasonCodes'])
        host=FakeHost()
        for name in ('ubuntu.sources','microsoft.list'):host.files['/etc/apt/sources.list.d/'+name]=b''
        data=self.collect(host);self.assert_closed(data);self.assertEqual(data['sources']['observation'],'source_set_invalid');self.assertTrue(data['sources']['unknownSourcePresent'])
    def test_only_complete_source_observation_emits_false(self):
        host=FakeHost();host.failures['/usr/bin/node']='command_failed';data=self.collect(host);self.assert_closed(data)
        self.assertEqual(data['status'],'blocked');self.assertEqual(data['sources']['observation'],'complete');self.assertTrue(all(data['sources'][f] is False for f in p.SOURCE_FLAGS));self.assertEqual(data['sources']['enabledCount'],2)
    def test_schema_rejects_diagnostic_free_text_and_old_schema(self):
        good=self.collect()
        for section,key,value in (('toolchain','nodeObservation',CANARY),('sources','observation',CANARY),(None,'schema',1),('toolchain','nodeObservation',None),('sources','observation',True)):
            bad=copy.deepcopy(good);(bad if section is None else bad[section])[key]=value
            with self.assertRaises(p.Blocked):p.validate_receipt(bad)
    def test_schema_rejects_node_diagnostic_version_and_reason_contradictions(self):
        good=self.collect()
        for stage,version,reasons in (('observed',None,[]),('path_missing','20.19.0',['node_unsupported']),('path_missing',None,['command_failed']),('version_query_failed',None,['node_unsupported'])):
            bad=copy.deepcopy(good);bad['toolchain'].update(nodeObservation=stage,observedNodeVersion=version);bad['reasonCodes']=reasons;bad['status']='blocked' if reasons else 'observed'
            with self.assertRaises(p.Blocked):p.validate_receipt(bad)
    def test_schema_rejects_false_absence_and_partial_complete_contradictions(self):
        host=FakeHost();host.env['GITHUB_SHA']='c'*40;unknown=self.collect(host)
        for flag in p.SOURCE_FLAGS:
            bad=copy.deepcopy(unknown);bad['sources'][flag]=False
            with self.assertRaises(p.Blocked):p.validate_receipt(bad)
        good=self.collect()
        for patch_values in ({'observation':'source_read_failed'},{'observation':'complete','enabledCount':None},{'observation':'complete','credentialLikeSourcePresent':None}):
            bad=copy.deepcopy(good);bad['sources'].update(patch_values)
            with self.assertRaises(p.Blocked):p.validate_receipt(bad)
    def test_schema_rejects_source_reason_and_flag_contradictions(self):
        data=self.source_case(b'deb [signed-by=/usr/share/keyrings/microsoft-prod.gpg] https://unknown.invalid/prod noble main\n')
        for mutation in ('reason','flag','positive'):
            bad=copy.deepcopy(data)
            if mutation=='reason':bad['reasonCodes']=['command_failed']
            elif mutation=='flag':bad['sources']['unknownSourcePresent']=None
            else:bad['sources']['credentialLikeSourcePresent']=True
            with self.assertRaises(p.Blocked):p.validate_receipt(bad)
    def test_arbitrary_exception_diagnostic_cannot_escape(self):
        error=p.Blocked(CANARY,CANARY);self.assertEqual(str(error),'unexpected_failure');self.assertIsNone(error.diagnostic)
    def test_public_allowlists_are_exact_unchanged(self):
        self.assertEqual(p.PUBLIC_SOURCES,{('http','azure.archive.ubuntu.com','/ubuntu'):'ubuntu-azure',('http','archive.ubuntu.com','/ubuntu'):'ubuntu-archive',('http','security.ubuntu.com','/ubuntu'):'ubuntu-security',('https','packages.microsoft.com','/ubuntu/24.04/prod'):'microsoft-prod'})
        self.assertEqual(p.KEYRINGS,{'/usr/share/keyrings/ubuntu-archive-keyring.gpg':'ubuntu-archive','/usr/share/keyrings/microsoft-prod.gpg':'microsoft-prod'})
        self.assertEqual(p.SUITES,frozenset(('noble','noble-updates','noble-backports','noble-security')));self.assertEqual(p.COMPONENTS,frozenset(('main','restricted','universe','multiverse')))

    def test_diagnostic_failure_code_maps_cover_exact_closed_states(self):
        self.assertEqual(set(p.NODE_FAILURE_CODES),p.NODE_OBSERVATIONS-{'not_observed','observed'})
        self.assertEqual(set(p.SOURCE_FAILURE_CODES),p.SOURCE_OBSERVATIONS-{'not_observed','complete'})
        self.assertTrue(all(values and values<=p.CODES for values in [*p.NODE_FAILURE_CODES.values(),*p.SOURCE_FAILURE_CODES.values()]))
    def test_complete_source_requires_completed_key_observation_even_when_other_section_blocked(self):
        host=FakeHost();host.failures['/usr/bin/node']='command_failed';data=self.collect(host)
        for key,value in (('keyringSHA256',None),('primaryFingerprints',[]),('types',[]),('suites',[]),('components',[])):
            bad=copy.deepcopy(data);bad['sources']['publicEntries'][0][key]=value
            with self.assertRaises(p.Blocked):p.validate_receipt(bad)
    def test_uppercase_authority_preserves_first_original_rejection(self):
        raw=f'deb [signed-by=/usr/share/keyrings/microsoft-prod.gpg] https://{CANARY}.invalid/prod noble main\n'.encode()
        data=self.source_case(raw);self.assertEqual(data['sources']['observation'],'uri_authority_unsupported');self.assertEqual(data['reasonCodes'],['source_unknown'])

if __name__=='__main__':unittest.main()
