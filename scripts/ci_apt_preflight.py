#!/usr/bin/env python3
"""Read-only hosted-runner observations. No update/install/download or trust change.

Only closed sanitized data is emitted. This does not prove APT solver/hook safety.
All host I/O is injectable; local contracts never collect the current host state.
"""
from __future__ import annotations
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import selectors
import signal
import stat
import subprocess
import tempfile
import time
from urllib.parse import urlsplit

CODES = frozenset(('identity_invalid','checkout_dirty','checkout_mismatch','source_changed',
    'apt_config_present','tool_missing','command_failed','command_timeout','output_limit',
    'deadline','unsafe_file','file_changed','file_missing','file_limit','directory_limit',
    'os_unsupported','apt_version_invalid','architecture_mismatch','node_unsupported',
    'lock_invalid','installed_playwright_invalid','config_invalid','insecure_apt_configuration',
    'source_unknown','source_credential_like','source_invalid','source_implicit_trust',
    'keyring_unsupported','key_invalid','secret_key_rejected','installed_state_invalid',
    'installed_state_changed','index_invalid','receipt_invalid','output_exists','output_unsafe',
    'unexpected_failure','cleanup_failed'))
HEX = re.compile(r'[0-9a-f]{64}')
SHA1 = re.compile(r'[0-9a-f]{40}')
SEMVER = re.compile(r'[0-9]+\.[0-9]+\.[0-9]+')
VERSION = re.compile(r'[0-9][A-Za-z0-9.+:~\-]{0,127}')
PKG = re.compile(r'[a-z0-9][a-z0-9+.-]{0,127}')
ARCHES = frozenset(('amd64','arm64','armhf','i386','all'))
SUITES = frozenset(('noble','noble-updates','noble-backports','noble-security'))
COMPONENTS = frozenset(('main','restricted','universe','multiverse'))
KEYRINGS = {'/usr/share/keyrings/ubuntu-archive-keyring.gpg':'ubuntu-archive',
            '/usr/share/keyrings/microsoft-prod.gpg':'microsoft-prod'}
PUBLIC_SOURCES = {('http','azure.archive.ubuntu.com','/ubuntu'):'ubuntu-azure',
                  ('http','archive.ubuntu.com','/ubuntu'):'ubuntu-archive',
                  ('http','security.ubuntu.com','/ubuntu'):'ubuntu-security',
                  ('https','packages.microsoft.com','/ubuntu/24.04/prod'):'microsoft-prod'}
SOURCE_FILES = ('.github/workflows/ci-apt-preflight.yml','scripts/ci_apt_preflight.py',
                'scripts/test_ci_apt_preflight_contract.py')
LIMITATIONS = ('noAPTupdate','noInstall','noFreshDownloadMeasurement','noHookLockProof',
               'noFullConfigurationProof','expectedVersusObservedToolchainSeparated')
# alias -> exact APT key, admissible type/value. /f and /d are documented path expansion.
CONFIG = {
 'architecture':('APT::Architecture','arch'),
 'installRecommends':('APT::Install-Recommends','bool'),
 'installSuggests':('APT::Install-Suggests','bool'),
 'checkDate':('Acquire::Check-Date','bool'),
 'checkValidUntil':('Acquire::Check-Valid-Until','bool'),
 'maxFutureTime':('Acquire::Max-FutureTime','int'),
 'minValidTime':('Acquire::Min-ValidTime','int'),
 'maxValidTime':('Acquire::Max-ValidTime','int'),
 'allowUnauthenticated':('APT::Get::AllowUnauthenticated','bool'),
 'allowInsecure':('Acquire::AllowInsecureRepositories','bool'),
 'allowDowngradeInsecure':('Acquire::AllowDowngradeToInsecureRepositories','bool'),
 'noLocking':('Debug::NoLocking','bool'),
 'sandboxUser':('APT::Sandbox::User','_apt'),
 'sourcesList':('Dir::Etc::sourcelist/f','/etc/apt/sources.list'),
 'sourcesParts':('Dir::Etc::sourceparts/d','/etc/apt/sources.list.d/'),
 'preferences':('Dir::Etc::preferences/f','/etc/apt/preferences'),
 'preferencesParts':('Dir::Etc::preferencesparts/d','/etc/apt/preferences.d/'),
 'configParts':('Dir::Etc::parts/d','/etc/apt/apt.conf.d/'),
 'statusFile':('Dir::State::status/f','/var/lib/dpkg/status'),
 'listsDirectory':('Dir::State::lists/d','/var/lib/apt/lists/'),
}
CONFIG_ALIASES = {f'P{i}':name for i,name in enumerate(CONFIG)}
DPKG_FORMAT = '${Package}\t${Architecture}\t${Version}\t${db:Status-Abbrev}\n'
INDEX_TYPES = ('Packages','Packages.lz4','Packages.gz','Packages.xz','InRelease','Release','Release.gpg','other')

class Blocked(ValueError):
    def __init__(self, code):
        super().__init__(code if code in CODES else 'unexpected_failure')


def require(condition, code):
    if not condition: raise Blocked(code)


def sha(raw): return hashlib.sha256(raw).hexdigest()

def canonical(value): return json.dumps(value,sort_keys=True,separators=(',',':'),ensure_ascii=True).encode()


def strict_json(raw):
    require(type(raw) is bytes and len(raw)<=4*1024*1024,'file_limit')
    depth=0;quoted=False;escaped=False
    for byte in raw:
        if quoted:
            if escaped:escaped=False
            elif byte==92:escaped=True
            elif byte==34:quoted=False
        elif byte==34:quoted=True
        elif byte in (91,123):
            depth+=1;require(depth<=32,'receipt_invalid')
        elif byte in (93,125):depth-=1
    def pairs(items):
        out={}
        for k,v in items:
            require(k not in out,'receipt_invalid');out[k]=v
        return out
    try:return json.loads(raw,object_pairs_hook=pairs,parse_constant=lambda _:(_ for _ in ()).throw(Blocked('receipt_invalid')))
    except (ValueError,UnicodeError,RecursionError):raise Blocked('receipt_invalid') from None


def safe_dir(path):
    path=Path(path).absolute();fd=os.open('/',os.O_RDONLY|os.O_DIRECTORY)
    try:
        for part in path.parts[1:]:
            require(part not in ('.','..'),'unsafe_file')
            nxt=os.open(part,os.O_RDONLY|os.O_DIRECTORY|os.O_NOFOLLOW,dir_fd=fd)
            os.close(fd);fd=nxt
        return fd
    except BaseException:
        os.close(fd);raise


class Host:
    """Bounded real backend, used only by the explicitly authorized future probe."""
    def __init__(self, env=None, clock=time.monotonic):
        self.env=os.environ if env is None else env
        self.clock=clock;self.deadline=clock()+120;self.output_bytes=0;self.cleanup_failed=False
    def tick(self):require(self.clock()<self.deadline-2,'deadline')
    def read(self,path,limit,optional=False):
        self.tick();parent=fd=None
        try:
            parent=safe_dir(Path(path).parent)
            fd=os.open(Path(path).name,os.O_RDONLY|os.O_NOFOLLOW|os.O_NONBLOCK,dir_fd=parent)
            before=os.fstat(fd)
            require(stat.S_ISREG(before.st_mode) and before.st_nlink==1,'unsafe_file')
            require(0<=before.st_size<=limit,'file_limit')
            out=bytearray()
            while True:
                self.tick();chunk=os.read(fd,min(65536,limit+1-len(out)))
                if not chunk:break
                out.extend(chunk);require(len(out)<=limit,'file_limit')
            after=os.fstat(fd)
            require((before.st_dev,before.st_ino,before.st_size,before.st_mtime_ns,before.st_ctime_ns)==
                    (after.st_dev,after.st_ino,after.st_size,after.st_mtime_ns,after.st_ctime_ns),'file_changed')
            return bytes(out)
        except FileNotFoundError:
            if optional:return None
            raise Blocked('file_missing') from None
        except OSError:raise Blocked('unsafe_file') from None
        finally:
            if fd is not None:os.close(fd)
            if parent is not None:os.close(parent)
    def entries(self,path,limit):
        self.tick();fd=None;out=[]
        try:
            fd=safe_dir(path)
            with os.scandir(fd) as entries:
                for entry in entries:
                    self.tick();require(len(out)<limit,'directory_limit')
                    info=entry.stat(follow_symlinks=False)
                    out.append((entry.name,info.st_mode,info.st_size,int(info.st_mtime)))
            return out
        except FileNotFoundError:raise Blocked('file_missing') from None
        except OSError:raise Blocked('unsafe_file') from None
        finally:
            if fd is not None:os.close(fd)
    def node_path(self):
        # Examine only a closed path class; never execute arbitrary PATH entries.
        candidates=[]
        for part in self.env.get('PATH','').split(os.pathsep):
            if part in ('/usr/bin','/usr/local/bin') or re.fullmatch(r'/opt/hostedtoolcache/node/[0-9]+\.[0-9]+\.[0-9]+/x64/bin',part):
                candidates.append(Path(part)/'node')
        for path in candidates:
            if not path.exists():continue
            real=path.resolve(strict=True)
            require(str(real) in ('/usr/bin/node','/usr/local/bin/node') or
                    re.fullmatch(r'/opt/hostedtoolcache/node/[0-9]+\.[0-9]+\.[0-9]+/x64/bin/node',str(real)), 'node_unsupported')
            for parent in [real,*real.parents]:
                info=parent.stat()
                require(info.st_uid==0 and not info.st_mode&0o022,'node_unsupported')
            require(stat.S_ISREG(real.stat().st_mode),'node_unsupported')
            return str(real)
        raise Blocked('node_unsupported')
    def run(self,argv,limit=65536,home=None):
        self.tick()
        if argv[0] in ('/usr/bin/apt-get','/usr/bin/apt-config'):
            require('APT_CONFIG' not in self.env,'apt_config_present')
        env={'PATH':'/usr/bin:/bin','LC_ALL':'C','HOME':str(home or '/nonexistent'),
             'GIT_CONFIG_NOSYSTEM':'1','GIT_CONFIG_GLOBAL':'/dev/null'}
        process=None;completed=False;end=min(self.deadline-2,self.clock()+15)
        try:
            process=subprocess.Popen(argv,stdin=subprocess.DEVNULL,stdout=subprocess.PIPE,stderr=subprocess.PIPE,
                                     env=env,start_new_session=True)
            buffers={process.stdout:bytearray(),process.stderr:bytearray()};total=0
            with selectors.DefaultSelector() as selector:
                for pipe in buffers:
                    os.set_blocking(pipe.fileno(),False);selector.register(pipe,selectors.EVENT_READ)
                while selector.get_map():
                    require(self.clock()<end,'command_timeout')
                    for key,_ in selector.select(min(.1,max(0,end-self.clock()))):
                        chunk=os.read(key.fileobj.fileno(),4096)
                        if not chunk:selector.unregister(key.fileobj);continue
                        total+=len(chunk);self.output_bytes+=len(chunk)
                        require(total<=limit and self.output_bytes<=20*1024*1024,'output_limit')
                        buffers[key.fileobj].extend(chunk)
                code=process.wait(timeout=max(.001,end-self.clock()))
            require(code==0,'command_failed')
            completed=True
            return bytes(buffers[process.stdout])
        except FileNotFoundError:raise Blocked('tool_missing') from None
        except subprocess.TimeoutExpired:raise Blocked('command_timeout') from None
        except OSError:raise Blocked('command_failed') from None
        finally:
            if process is not None:
                # The leader may already have exited while an owned descendant
                # still holds a pipe. Error cleanup targets the group regardless.
                if not completed or process.poll() is None:
                    try:os.killpg(process.pid,signal.SIGKILL)
                    except ProcessLookupError:pass
                    except OSError:self.cleanup_failed=True
                    if process.poll() is None:
                        try:process.wait(timeout=2)
                        except subprocess.TimeoutExpired:self.cleanup_failed=True
                process.stdout.close();process.stderr.close()
    def key_fingerprints(self,path):
        require(path in KEYRINGS,'keyring_unsupported')
        raw=self.read(path,1024*1024)
        with tempfile.TemporaryDirectory(prefix='adtr-apt-public-') as directory:
            copy=Path(directory)/'public.gpg';copy.write_bytes(raw);copy.chmod(0o400)
            output=self.run(['/usr/bin/gpg','--no-options','--batch','--no-autostart','--no-auto-key-retrieve',
                '--auto-key-locate','clear','--homedir',directory,'--with-colons','--show-keys',str(copy)],home=directory)
        primary,sub=parse_keys(output)
        return sha(raw),primary,sub


def parse_keys(raw):
    require(type(raw) is bytes and len(raw)<=65536,'key_invalid')
    try:lines=raw.decode('utf-8','strict').splitlines()
    except UnicodeError:raise Blocked('key_invalid') from None
    primary=[];sub=[];pending=None;count=0
    for line in lines:
        pieces=line.split(':');kind=pieces[0]
        require(kind not in ('sec','ssb'),'secret_key_rejected')
        require(kind in ('pub','sub','fpr','uid','uat','tru','rvk','grp'),'key_invalid')
        if kind in ('pub','sub'):
            count+=1;require(count<=64 and pending is None and len(pieces)>=12,'key_invalid')
            require(pieces[2].isdigit() and 255<=int(pieces[2])<=16384 and pieces[3] in ('1','2','3','16','17','18','19','22'),'key_invalid')
            require(pieces[5].isdigit() and 0<=int(pieces[5])<=4102444800,'key_invalid')
            require(pieces[6]=='' or pieces[6].isdigit() and int(pieces[6])<=4102444800,'key_invalid')
            pending=kind
        elif kind=='fpr':
            require(pending is not None and len(pieces)>9 and re.fullmatch(r'(?:[0-9A-F]{40}|[0-9A-F]{64})',pieces[9]),'key_invalid')
            target=primary if pending=='pub' else sub
            require(pieces[9] not in primary+sub,'key_invalid');target.append(pieces[9]);pending=None
    require(primary and pending is None,'key_invalid')
    return sorted(primary),sorted(sub)


def parse_config(raw):
    require(len(raw)<=65536,'config_invalid')
    try:lines=raw.decode('ascii').splitlines()
    except UnicodeError:raise Blocked('config_invalid') from None
    result={key:'unset' for key in CONFIG};seen=set()
    for line in lines:
        match=re.fullmatch(r"(P[0-9]+)='([^'\r\n]*)'",line)
        require(match is not None and match[1] in CONFIG_ALIASES and match[1] not in seen,'config_invalid')
        seen.add(match[1]);name=CONFIG_ALIASES[match[1]];value=match[2];kind=CONFIG[name][1]
        if kind=='bool':
            require(value.lower() in ('true','false','yes','no','1','0'),'config_invalid')
            value=value.lower() in ('true','yes','1')
        elif kind=='int':
            require(value.isdigit() and int(value)<=315360000,'config_invalid');value=int(value)
        elif kind=='arch':require(value=='amd64','architecture_mismatch')
        else:require(value==kind,'config_invalid')
        result[name]=value
    for name in ('allowUnauthenticated','allowInsecure','allowDowngradeInsecure','noLocking'):
        require(result[name] is not True,'insecure_apt_configuration')
    for name in ('checkDate','checkValidUntil'):
        require(result[name] is not False,'insecure_apt_configuration')
    result.update(coverage='fixed-queries-only',fullEffectiveConfigBound=False)
    return result


def source_class(uri):
    require(type(uri) is str and len(uri)<=1024,'source_invalid')
    require(not any(ord(c)<33 or ord(c)>126 for c in uri),'source_invalid')
    try:parsed=urlsplit(uri)
    except ValueError:raise Blocked('source_invalid') from None
    require(not parsed.username and not parsed.password and not parsed.query and not parsed.fragment
            and '%' not in uri and '@' not in uri,'source_credential_like')
    try:port=parsed.port
    except ValueError:raise Blocked('source_invalid') from None
    require(port is None and parsed.hostname==parsed.netloc,'source_unknown')
    path=parsed.path[:-1] if parsed.path.endswith('/') else parsed.path
    key=(parsed.scheme,parsed.hostname,path)
    require(key in PUBLIC_SOURCES,'source_unknown')
    return PUBLIC_SOURCES[key]


def _source_entry(types,uris,suites,components,architectures,keyring):
    require(0<len(types)<=2 and len(set(types))==len(types) and set(types)<={'deb','deb-src'},'source_invalid')
    require(suites and set(suites)<=SUITES and components and set(components)<=COMPONENTS,'source_unknown')
    require(len(set(suites))==len(suites) and len(set(components))==len(components),'source_invalid')
    require(not architectures or set(architectures)<=ARCHES,'source_unknown')
    require(len(set(architectures))==len(architectures),'source_invalid')
    require(keyring is not None,'source_implicit_trust')
    require(keyring in KEYRINGS,'keyring_unsupported')
    require(0<len(uris)<=4 and len(set(uris))==len(uris),'source_invalid')
    return [dict(sourceClass=source_class(uri),types=sorted(types),suites=sorted(suites),
                 components=sorted(components),architectures=sorted(architectures),signedByClass=KEYRINGS[keyring],
                 keyringSHA256=None,primaryFingerprints=[],subkeyFingerprints=[]) for uri in uris]


def parse_source_file(raw,kind):
    require(type(raw) is bytes and len(raw)<=262144,'file_limit')
    try:text=raw.decode('utf-8','strict')
    except UnicodeError:raise Blocked('source_invalid') from None
    require('\x00' not in text and '\r' not in text,'source_invalid')
    enabled=[];disabled=0
    if kind=='.list':
        for line in text.splitlines():
            line=line.strip()
            if not line or line.startswith('#'):continue
            require(len(line)<=8192,'source_invalid')
            # Comment only when preceded by whitespace, never strip URI fragment.
            line=re.split(r'\s+#',line,maxsplit=1)[0].rstrip()
            m=re.fullmatch(r'(deb|deb-src)\s+(?:\[([^\]]+)\]\s+)?(\S+)\s+(\S+)\s+(.+)',line)
            require(m is not None,'source_invalid');opts={}
            for token in (m[2] or '').split():
                pair=token.split('=',1)
                require(len(pair)==2 and pair[0] in ('arch','signed-by') and pair[0] not in opts,'source_unknown')
                opts[pair[0]]=pair[1]
            enabled.extend(_source_entry([m[1]],[m[3]],[m[4]],m[5].split(),opts.get('arch','').split(',') if opts.get('arch') else [],opts.get('signed-by')))
    else:
        require(kind=='.sources','source_invalid');stanzas=[];current={};last=None
        for line in text.splitlines()+['']:
            require(len(line)<=8192,'source_invalid')
            if line.startswith('#'):continue
            if not line.strip():
                if current:stanzas.append(current);current={};last=None
                require(len(stanzas)<=32,'source_invalid');continue
            if line[0].isspace():
                require(last is not None,'source_invalid');current[last]+=' '+line.strip();continue
            m=re.fullmatch(r'([A-Za-z][A-Za-z-]*):\s*(.*)',line)
            require(m is not None and m[1] not in current,'source_invalid')
            last=m[1];current[last]=m[2]
        for stanza in stanzas:
            require(set(stanza)<={'Types','URIs','Suites','Components','Architectures','Signed-By','Enabled'},'source_unknown')
            require(stanza.get('Enabled','yes') in ('yes','no'),'source_invalid')
            if stanza.get('Enabled')=='no':
                for uri in stanza.get('URIs','').split():source_class(uri)
                disabled+=1;continue
            require({'Types','URIs','Suites','Components'}<=set(stanza),'source_invalid')
            enabled.extend(_source_entry(stanza['Types'].split(),stanza['URIs'].split(),stanza['Suites'].split(),
                         stanza['Components'].split(),stanza.get('Architectures','').split(),stanza.get('Signed-By')))
    require(len(enabled)<=32,'source_invalid')
    return enabled,disabled


def installed_summary(raw):
    require(type(raw) is bytes and len(raw)<=16*1024*1024,'installed_state_invalid')
    try:lines=raw.decode('ascii','strict').splitlines()
    except UnicodeError:raise Blocked('installed_state_invalid') from None
    rows=[];seen=set();configured=held=residual=unconfigured=0
    for line in lines:
        fields=line.split('\t');require(len(fields)==4 and len(rows)<20000,'installed_state_invalid')
        name,arch,version,status=fields
        require(PKG.fullmatch(name) and arch in ARCHES and (VERSION.fullmatch(version) or version==''),'installed_state_invalid')
        require(re.fullmatch(r'[uihrp][ncHUFWti][ R]',status),'installed_state_invalid')
        require((name,arch) not in seen,'installed_state_invalid');seen.add((name,arch));rows.append(fields)
        if status[1]=='i' and status[2]==' ':configured+=1
        elif status[1]=='c' and status[2]==' ':residual+=1
        else:unconfigured+=1
        if status[0]=='h':held+=1
    require(rows,'installed_state_invalid')
    return dict(dpkgQueryCanonicalSHA256=sha(canonical(sorted(rows))),rowCount=len(rows),configuredCount=configured,
                heldCount=held,residualConfigCount=residual,unconfiguredCount=unconfigured)


def index_summary(entries):
    counts={k:0 for k in INDEX_TYPES};sizes={k:0 for k in INDEX_TYPES};times=[];regular=total=0
    require(type(entries) is list and len(entries)<=10000,'directory_limit')
    for name,mode,size,mtime in entries:
        require(type(name) is str and type(size) is int and 0<=size<=4*1024**3,'index_invalid')
        if stat.S_ISDIR(mode):continue
        require(stat.S_ISREG(mode),'index_invalid');regular+=1;total+=size
        require(total<=8*1024**3 and type(mtime) is int and 0<=mtime<=4102444800,'index_invalid')
        kind=next((x for x in INDEX_TYPES[:-1] if name.endswith('_'+x)),'other')
        counts[kind]+=1;sizes[kind]+=size;times.append(mtime)
    return dict(entryCount=len(entries),regularFileCount=regular,totalBytes=total,suffixCounts=counts,suffixBytes=sizes,
                newestMtime=max(times) if times else None,oldestMtime=min(times) if times else None,
                measurement='preexisting-local-index-stat-only')

IDENTITY_FIELDS=('checkoutSHA','treeSHA','collectorSHA256','workflowSHA256','contractSHA256','checkoutClean',
                 'runID','runAttempt','runnerImage','imageOS','runnerOS','runnerArch')
TOOL_FIELDS=('aptVersion','aptArchitecture','observedNodeVersion','expectedNodeVersion','lockedPlaywrightVersion',
             'playwrightInstalledObserved','observedPlaywrightVersion','packageLockSHA256')
STATE_FIELDS=('statusSHA256','statusBytes','dpkgQueryCanonicalSHA256','rowCount','configuredCount','heldCount',
              'residualConfigCount','unconfiguredCount','extendedStatesSHA256','extendedStatesBytes','baselineStable')
SOURCE_KEYS=('enabledCount','disabledCount','publicEntries','unknownSourcePresent','credentialLikeSourcePresent','implicitTrustUnresolved')
SOURCE_ENTRY_KEYS=('sourceClass','types','suites','components','architectures','signedByClass','keyringSHA256','primaryFingerprints','subkeyFingerprints')
INDEX_KEYS=('entryCount','regularFileCount','totalBytes','suffixCounts','suffixBytes','newestMtime','oldestMtime','measurement')


def empty_receipt():
    return {'schema':1,'status':'blocked','reasonCodes':[],
      'identity':dict.fromkeys(IDENTITY_FIELDS),
      'os':dict.fromkeys(('ID','version','codename','dpkgArchitecture')),
      'toolchain':dict(dict.fromkeys(TOOL_FIELDS),expectedNodeVersion='24.19.0'),
      'sources':dict(enabledCount=None,disabledCount=None,publicEntries=[],unknownSourcePresent=False,
                     credentialLikeSourcePresent=False,implicitTrustUnresolved=False),
      'aptConfig':dict(dict.fromkeys(CONFIG),coverage='fixed-queries-only',fullEffectiveConfigBound=False),
      'installedState':dict.fromkeys(STATE_FIELDS),
      'indexes':dict(dict.fromkeys(INDEX_KEYS),measurement='preexisting-local-index-stat-only'),
      'limitations':list(LIMITATIONS)}


class Collector:
    def __init__(self,host,repo):
        self.host=host;self.repo=Path(repo);self.data=empty_receipt();self.reasons=set()
    def part(self,function):
        try:function()
        except Blocked as error:self.reasons.add(str(error))
        except Exception:self.reasons.add('unexpected_failure')
    def git(self,*args):
        return self.host.run(['/usr/bin/git','--no-optional-locks','-c','core.fsmonitor=false',
            '-c','core.untrackedCache=false','-c','core.hooksPath=/dev/null','-c','submodule.recurse=false',
            '-C',str(self.repo),*args])
    def identity(self):
        env=self.host.env
        head=self.git('rev-parse','--verify','HEAD').decode('ascii').strip()
        tree=self.git('rev-parse','--verify','HEAD^{tree}').decode('ascii').strip()
        require(SHA1.fullmatch(head) and SHA1.fullmatch(tree),'identity_invalid')
        require(head==env.get('GITHUB_SHA'),'checkout_mismatch')
        require(not self.git('status','--porcelain=v1','--untracked-files=normal','--ignore-submodules=all'),'checkout_dirty')
        require(re.fullmatch(r'[1-9][0-9]{0,19}',env.get('GITHUB_RUN_ID','')) and re.fullmatch(r'[1-9][0-9]{0,8}',env.get('GITHUB_RUN_ATTEMPT','')),'identity_invalid')
        require(re.fullmatch(r'[0-9]{8}\.[0-9]{1,8}\.[0-9]{1,8}',env.get('ImageVersion','')),'identity_invalid')
        require(env.get('ImageOS')=='ubuntu24' and env.get('RUNNER_OS')=='Linux' and env.get('RUNNER_ARCH')=='X64','identity_invalid')
        hashes=[sha(self.host.read(self.repo/path,1024*1024)) for path in SOURCE_FILES]
        self.data['identity'].update(checkoutSHA=head,treeSHA=tree,workflowSHA256=hashes[0],collectorSHA256=hashes[1],contractSHA256=hashes[2],checkoutClean=True,
            runID=env['GITHUB_RUN_ID'],runAttempt=env['GITHUB_RUN_ATTEMPT'],runnerImage=env['ImageVersion'],imageOS='ubuntu24',runnerOS='Linux',runnerArch='X64')
    def os_info(self):
        raw=self.host.read('/usr/lib/os-release',65536)
        try:text=raw.decode('utf-8')
        except UnicodeError:raise Blocked('os_unsupported') from None
        fields={}
        for line in text.splitlines():
            if not line or line.startswith('#'):continue
            key,sep,value=line.partition('=');require(sep and key not in fields,'os_unsupported')
            fields[key]=value[1:-1] if len(value)>=2 and value.startswith('"') and value.endswith('"') else value
        require(fields.get('ID')=='ubuntu' and fields.get('VERSION_ID')=='24.04' and fields.get('VERSION_CODENAME')=='noble','os_unsupported')
        arch=self.host.run(['/usr/bin/dpkg','--print-architecture'])
        require(arch==b'amd64\n','architecture_mismatch')
        self.data['os'].update(ID='ubuntu',version='24.04',codename='noble',dpkgArchitecture='amd64')
    def apt(self):
        require('APT_CONFIG' not in self.host.env,'apt_config_present')
        raw=self.host.run(['/usr/bin/apt-get','--version'])
        first=raw.splitlines()[0] if raw.splitlines() else b''
        match=re.fullmatch(rb'apt ([0-9]+\.[0-9]+\.[0-9]+(?:[+~a-zA-Z0-9.-]*)?) \(amd64\)',first)
        require(match is not None,'apt_version_invalid')
        self.data['toolchain'].update(aptVersion=match[1].decode(),aptArchitecture='amd64')
        require('APT_CONFIG' not in self.host.env,'apt_config_present')
        argv=['/usr/bin/apt-config','shell']
        for alias,name in CONFIG_ALIASES.items():argv.extend((alias,CONFIG[name][0]))
        self.data['aptConfig']=parse_config(self.host.run(argv))
    def node(self):
        raw=self.host.run([self.host.node_path(),'--version'])
        require(re.fullmatch(rb'v[0-9]+\.[0-9]+\.[0-9]+\n',raw),'node_unsupported')
        self.data['toolchain']['observedNodeVersion']=raw.decode().strip()[1:]
    def playwright(self):
        raw=self.host.read(self.repo/'web/package-lock.json',4*1024*1024)
        lock=strict_json(raw)
        require(type(lock) is dict and type(lock.get('packages')) is dict,'lock_invalid')
        names=('@playwright/test','playwright','playwright-core')
        for name in names:
            entry=lock['packages'].get('node_modules/'+name)
            require(type(entry) is dict and entry.get('version')=='1.56.1','lock_invalid')
        self.data['toolchain'].update(lockedPlaywrightVersion='1.56.1',packageLockSHA256=sha(raw))
        installed=[]
        for name in names:
            item=self.host.read(self.repo/'web/node_modules'/name/'package.json',262144,optional=True)
            if item is None:installed.append(None);continue
            value=strict_json(item)
            require(type(value) is dict and value.get('name')==name and value.get('version')=='1.56.1','installed_playwright_invalid')
            installed.append('1.56.1')
        require(all(v is None for v in installed) or all(v=='1.56.1' for v in installed),'installed_playwright_invalid')
        self.data['toolchain'].update(playwrightInstalledObserved=all(v is not None for v in installed),
                                      observedPlaywrightVersion='1.56.1' if all(installed) else None)
    def sources(self):
        config=self.data['aptConfig']
        require(config['sourcesList']=='/etc/apt/sources.list' and config['sourcesParts']=='/etc/apt/sources.list.d/','config_invalid')
        file_inputs=[]
        primary=self.host.read('/etc/apt/sources.list',262144,optional=True)
        if primary is not None:file_inputs.append((primary,'.list'))
        for name,mode,size,_ in self.host.entries('/etc/apt/sources.list.d',128):
            if not name.endswith(('.list','.sources')):continue
            require(re.fullmatch(r'[A-Za-z0-9_.-]+',name) and stat.S_ISREG(mode),'unsafe_file')
            require(size<=262144,'file_limit')
            file_inputs.append((self.host.read(Path('/etc/apt/sources.list.d')/name,262144),Path(name).suffix))
        public=[];disabled=0
        for raw,kind in file_inputs:
            entries,count=parse_source_file(raw,kind);public.extend(entries);disabled+=count
            require(len(public)<=32,'source_invalid')
        require(public,'source_invalid')
        # Source count is preserved, never omit an enabled unknown source.
        require(disabled<=128,'source_invalid')
        for item in public:
            path=next(path for path,kind in KEYRINGS.items() if kind==item['signedByClass'])
            digest,primary,sub=self.host.key_fingerprints(path)
            item.update(keyringSHA256=digest,primaryFingerprints=primary,subkeyFingerprints=sub)
        self.data['sources'].update(enabledCount=len(public),disabledCount=disabled,publicEntries=public)
    def installed(self):
        require(self.data['aptConfig']['statusFile']=='/var/lib/dpkg/status','config_invalid')
        before=self.host.read('/var/lib/dpkg/status',16*1024*1024)
        auto_before=self.host.read('/var/lib/apt/extended_states',4*1024*1024,optional=True)
        summary=installed_summary(self.host.run(['/usr/bin/dpkg-query','--show','--showformat='+DPKG_FORMAT],limit=16*1024*1024))
        after=self.host.read('/var/lib/dpkg/status',16*1024*1024)
        auto_after=self.host.read('/var/lib/apt/extended_states',4*1024*1024,optional=True)
        stable=before==after and auto_before==auto_after
        self.data['installedState'].update(summary,statusSHA256=sha(before),statusBytes=len(before),
             extendedStatesSHA256=sha(auto_before) if auto_before is not None else None,
             extendedStatesBytes=len(auto_before) if auto_before is not None else 0,baselineStable=stable)
        require(stable,'installed_state_changed')
    def indexes(self):
        require(self.data['aptConfig']['listsDirectory']=='/var/lib/apt/lists/','config_invalid')
        self.data['indexes']=index_summary(self.host.entries('/var/lib/apt/lists',10000))
    def collect(self):
        self.part(self.identity)
        if not self.reasons:
            for fn in (self.os_info,self.apt,self.node,self.playwright,self.sources,self.installed,self.indexes):self.part(fn)
            # Bind source bytes again at the observation end; no hash-then-reopen claim.
            def unchanged():
                require(self.git('rev-parse','--verify','HEAD').decode().strip()==self.data['identity']['checkoutSHA'],'source_changed')
                require(self.git('rev-parse','--verify','HEAD^{tree}').decode().strip()==self.data['identity']['treeSHA'],'source_changed')
                require(not self.git('status','--porcelain=v1','--untracked-files=normal','--ignore-submodules=all'),'checkout_dirty')
                for path,key in zip(SOURCE_FILES,('workflowSHA256','collectorSHA256','contractSHA256')):
                    require(sha(self.host.read(self.repo/path,1024*1024))==self.data['identity'][key],'source_changed')
                if self.data['toolchain']['packageLockSHA256'] is not None:
                    require(sha(self.host.read(self.repo/'web/package-lock.json',4*1024*1024))==self.data['toolchain']['packageLockSHA256'],'source_changed')
            self.part(unchanged)
        self.data['sources']['unknownSourcePresent']=bool(self.reasons & {'source_unknown','source_invalid','keyring_unsupported'})
        self.data['sources']['credentialLikeSourcePresent']='source_credential_like' in self.reasons
        self.data['sources']['implicitTrustUnresolved']='source_implicit_trust' in self.reasons
        if getattr(self.host,'cleanup_failed',False):self.reasons.add('cleanup_failed')
        self.data['reasonCodes']=sorted(self.reasons);self.data['status']='blocked' if self.reasons else 'observed'
        validate_receipt(self.data)
        return self.data


def validate_receipt(data):
    template=empty_receipt()
    require(type(data) is dict and set(data)==set(template),'receipt_invalid')
    require(type(data['schema']) is int and data['schema']==1 and data['status'] in ('observed','blocked'),'receipt_invalid')
    codes=data['reasonCodes']
    require(type(codes) is list and len(codes)<=len(CODES) and all(type(x) is str and x in CODES for x in codes)
            and codes==sorted(set(codes)) and (bool(codes)==(data['status']=='blocked')),'receipt_invalid')
    require(data['limitations']==list(LIMITATIONS),'receipt_invalid')
    for section in ('identity','os','toolchain','sources','aptConfig','installedState','indexes'):
        require(type(data[section]) is dict and set(data[section])==set(template[section]),'receipt_invalid')
    def string(value,pattern):require(value is None or type(value) is str and re.fullmatch(pattern,value),'receipt_invalid')
    def integer(value,maximum=8*1024**3):require(value is None or type(value) is int and 0<=value<=maximum,'receipt_invalid')
    def boolean(value):require(value is None or type(value) is bool,'receipt_invalid')
    identity=data['identity']
    for key in ('checkoutSHA','treeSHA'):string(identity[key],r'[0-9a-f]{40}')
    for key in ('collectorSHA256','workflowSHA256','contractSHA256'):string(identity[key],r'[0-9a-f]{64}')
    for key in ('runID','runAttempt'):string(identity[key],r'[1-9][0-9]{0,19}')
    string(identity['runnerImage'],r'[0-9]{8}\.[0-9]{1,8}\.[0-9]{1,8}');boolean(identity['checkoutClean'])
    for key,value in (('imageOS','ubuntu24'),('runnerOS','Linux'),('runnerArch','X64')):require(identity[key] in (None,value),'receipt_invalid')
    for key,value in (('ID','ubuntu'),('version','24.04'),('codename','noble'),('dpkgArchitecture','amd64')):require(data['os'][key] in (None,value),'receipt_invalid')
    tool=data['toolchain'];string(tool['aptVersion'],r'[0-9]+\.[0-9]+\.[0-9]+[+~a-zA-Z0-9.-]*')
    require(tool['aptArchitecture'] in (None,'amd64') and tool['expectedNodeVersion']=='24.19.0' and tool['lockedPlaywrightVersion'] in (None,'1.56.1'),'receipt_invalid')
    string(tool['observedNodeVersion'],r'[0-9]+\.[0-9]+\.[0-9]+');boolean(tool['playwrightInstalledObserved'])
    require(tool['observedPlaywrightVersion'] in (None,'1.56.1'),'receipt_invalid');string(tool['packageLockSHA256'],r'[0-9a-f]{64}')
    if tool['playwrightInstalledObserved'] is not None:
        require((tool['observedPlaywrightVersion']=='1.56.1')==tool['playwrightInstalledObserved'],'receipt_invalid')
    sources=data['sources'];integer(sources['enabledCount'],32);integer(sources['disabledCount'],128)
    for key in SOURCE_KEYS[-3:]:require(type(sources[key]) is bool,'receipt_invalid')
    require(type(sources['publicEntries']) is list and len(sources['publicEntries'])<=32,'receipt_invalid')
    for entry in sources['publicEntries']:
        require(type(entry) is dict and set(entry)==set(SOURCE_ENTRY_KEYS),'receipt_invalid')
        require(entry['sourceClass'] in PUBLIC_SOURCES.values() and entry['signedByClass'] in KEYRINGS.values(),'receipt_invalid')
        for key,allowed in (('types',{'deb','deb-src'}),('suites',SUITES),('components',COMPONENTS),('architectures',ARCHES)):
            require(type(entry[key]) is list and all(type(x) is str and x in allowed for x in entry[key]) and entry[key]==sorted(set(entry[key])),'receipt_invalid')
        string(entry['keyringSHA256'],r'[0-9a-f]{64}')
        for key in ('primaryFingerprints','subkeyFingerprints'):
            require(type(entry[key]) is list and len(entry[key])<=64 and all(type(x) is str and re.fullmatch(r'(?:[0-9A-F]{40}|[0-9A-F]{64})',x) for x in entry[key]) and entry[key]==sorted(set(entry[key])),'receipt_invalid')
    if sources['enabledCount'] is not None:require(sources['enabledCount']==len(sources['publicEntries']),'receipt_invalid')
    config=data['aptConfig']
    require(config['coverage']=='fixed-queries-only' and config['fullEffectiveConfigBound'] is False,'receipt_invalid')
    for key,(_,kind) in CONFIG.items():
        value=config[key]
        if value is None or value=='unset':continue
        if kind=='bool':require(type(value) is bool,'receipt_invalid')
        elif kind=='int':integer(value,315360000)
        elif kind=='arch':require(value=='amd64','receipt_invalid')
        else:require(value==kind,'receipt_invalid')
    state=data['installedState']
    for key in ('statusSHA256','dpkgQueryCanonicalSHA256','extendedStatesSHA256'):string(state[key],r'[0-9a-f]{64}')
    for key in ('statusBytes','rowCount','configuredCount','heldCount','residualConfigCount','unconfiguredCount','extendedStatesBytes'):integer(state[key],16*1024*1024)
    boolean(state['baselineStable'])
    if state['rowCount'] is not None:
        require(state['rowCount']==state['configuredCount']+state['residualConfigCount']+state['unconfiguredCount'] and state['heldCount']<=state['rowCount'],'receipt_invalid')
    indexes=data['indexes'];require(indexes['measurement']=='preexisting-local-index-stat-only','receipt_invalid')
    for key in ('entryCount','regularFileCount'):integer(indexes[key],10000)
    integer(indexes['totalBytes'])
    for key in ('newestMtime','oldestMtime'):integer(indexes[key],4102444800)
    for key in ('suffixCounts','suffixBytes'):
        if indexes[key] is not None:
            require(type(indexes[key]) is dict and set(indexes[key])==set(INDEX_TYPES),'receipt_invalid')
            for value in indexes[key].values():integer(value)
    if indexes['regularFileCount'] is not None:
        require(indexes['regularFileCount']==sum(indexes['suffixCounts'].values()) and indexes['totalBytes']==sum(indexes['suffixBytes'].values()) and indexes['entryCount']>=indexes['regularFileCount'],'receipt_invalid')
    if data['status']=='observed':
        for section in ('identity','os','aptConfig','installedState','indexes'):
            allowed={'extendedStatesSHA256','oldestMtime','newestMtime'}
            require(all(v is not None or k in allowed for k,v in data[section].items()),'receipt_invalid')
        require(all(tool[k] is not None for k in TOOL_FIELDS if k!='observedPlaywrightVersion') and sources['enabledCount'] is not None and sources['enabledCount']>0 and not any(sources[k] for k in SOURCE_KEYS[-3:]),'receipt_invalid')
        require(identity['checkoutClean'] is True and state['baselineStable'] is True,'receipt_invalid')
        for entry in sources['publicEntries']:
            require(entry['keyringSHA256'] is not None and entry['primaryFingerprints'] and entry['types'] and entry['suites'] and entry['components'],'receipt_invalid')
    require(len(canonical(data))<=65536,'receipt_invalid')


def write_receipt(path,data,marker):
    """Exclusive safe receipt and explicit per-step marker; no stale always-upload."""
    validate_receipt(data);raw=canonical(data)+b'\n'
    path=Path(path).absolute();marker=Path(marker).absolute();parent=outfd=markerfd=None;created=False
    try:
        require(path.name=='adtr-apt-preflight.json' and marker.parent.name=='_runner_file_commands'
                and marker.parent.parent==path.parent and re.fullmatch(r'set_output_[0-9a-f-]{36}',marker.name),'output_unsafe')
        parent=safe_dir(path.parent);info=os.fstat(parent)
        require(info.st_uid==os.getuid() and not info.st_mode&0o022,'output_unsafe')
        markerdir=safe_dir(marker.parent)
        try:markerfd=os.open(marker.name,os.O_WRONLY|os.O_APPEND|os.O_NOFOLLOW,dir_fd=markerdir)
        finally:os.close(markerdir)
        mi=os.fstat(markerfd)
        require(stat.S_ISREG(mi.st_mode) and mi.st_nlink==1 and mi.st_uid==os.getuid() and mi.st_size==0,'output_unsafe')
        outfd=os.open(path.name,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o600,dir_fd=parent);created=True
        offset=0
        while offset<len(raw):offset+=os.write(outfd,raw[offset:])
        os.fsync(outfd);require(os.fstat(outfd).st_size==len(raw),'output_unsafe')
        # Marker is only written after current exclusive output fully validated.
        require(os.write(markerfd,b'receipt_written=true\n')==len(b'receipt_written=true\n'),'output_unsafe')
    except FileExistsError:raise Blocked('output_exists') from None
    except OSError:raise Blocked('output_unsafe') from None
    finally:
        if outfd is not None:os.close(outfd)
        if markerfd is not None:os.close(markerfd)
        if parent is not None:os.close(parent)


class ClosedParser(argparse.ArgumentParser):
    def error(self,message):raise Blocked('receipt_invalid')


def main(argv=None,host=None):
    try:
        parser=ClosedParser();parser.add_argument('--output',required=True)
        args=parser.parse_args(argv)
        host=Host() if host is None else host
        data=Collector(host,Path(__file__).resolve().parents[1]).collect()
        marker=host.env.get('GITHUB_OUTPUT','')
        require(bool(marker),'output_unsafe')
        write_receipt(args.output,data,marker)
        print('apt-preflight status='+data['status'])
        return 0 if data['status']=='observed' else 1
    except Blocked as error:
        print('apt-preflight status=blocked code='+str(error));return 1
    except Exception:
        print('apt-preflight status=blocked code=unexpected_failure');return 1


if __name__=='__main__':raise SystemExit(main())
