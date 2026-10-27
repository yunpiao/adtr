"""C-only provenance, post-cleanup finalization and exact-nine publication.

There is intentionally no finalize CLI or success Boolean. The runner must keep
its independently captured provenance and the fixture's original retained bytes,
finish all owned cleanup, then call the keyword-only finalizer exactly once.
"""
import argparse
import base64
import ctypes
from dataclasses import dataclass
import fcntl
import json
import os
from pathlib import Path
import re
import secrets
import shutil
import stat
import sys
import tempfile
from typing import Mapping, MutableMapping

from validate_user_asset_export_v3_evidence import checkout_provenance
from validate_user_asset_export_v3_uac_evidence import (
    PROFILE, RAW_NAME, UACEvidenceError, b64, equal, load_oracle, require, schema, sha,
    strict_json, validate_uac_evidence,
)

PROVENANCE = Path('.e2e-diagnostics/user-asset-export-v3-uac-provenance.json')
CANDIDATE_NAME = 'user-asset-export-v3-uac-candidate.json'
EVIDENCE_NAME = 'user-asset-export-v3-uac-evidence.json'
ARTIFACTS = Path('web/playwright-report/user-asset-export-v3-uac-mixed33-validated')
NONCE_ENV = 'ADTR_LDAP_FIXTURE_UAC_RUN_NONCE'
MIXED_MODES = ('GROUP_ASSETS_V3', 'COMPUTER_ASSETS_V3', 'V2', 'V4', 'EMPTY', 'SLOW', 'USER_ASSETS_V2', 'GROUP_MEMBERS_V4_PAGINATION', 'GROUP_MEMBERS_V4_REFRESH')


@dataclass(frozen=True)
class ArtifactDigest:
    filename: str
    sha256: str
    byte_count: int


@dataclass(frozen=True)
class FinalizedUACArtifacts:
    relative_directory: str
    files: tuple[ArtifactDigest, ...]


# Diagnostics never authorize evidence. Codes and entered stages are closed
# constants; unknown errors, subclasses and mutable external attributes cannot
# contribute text. Keep the validator/schema and its exception type unchanged.
_UAC_CODES = frozenset("configuration provenance candidate_discovery candidate_schema source_binding task_binding raw_transcript terminal_seal post_parse post_binding post_count artifact_binding cleanup publication".split())
_UAC_STAGES = frozenset("""
runner_main candidate_discovery
cleanup_snapshot cleanup_worker cleanup_api cleanup_ldap_close cleanup_ldap_retained
cleanup_log_scan cleanup_log_close cleanup_database_remove cleanup_diagnostic_phase
cleanup_diagnostic_write cleanup_diagnostic_result
finalize_preflight finalize_cleanup_receipt finalize_provenance finalize_candidate_discovery
finalize_candidate_schema finalize_wire finalize_seal finalize_wire_binding finalize_schema
finalize_artifact_inventory finalize_artifact_read finalize_validate finalize_destination
finalize_stage_write finalize_stage_readback finalize_atomic_publish finalize_stage_cleanup
ldap_stop ldap_inspect ldap_exit ldap_retained_read ldap_container_remove ldap_network_remove ldap_private_remove
ldap_evidence_open ldap_evidence_file ldap_evidence_read ldap_evidence_json ldap_evidence_schema
ldap_evidence_binding ldap_evidence_counts ldap_evidence_state ldap_evidence_sequence
""".split())
_UAC_DIAGNOSTIC_MARKER = object()
_UAC_DIAGNOSTIC_ATTRIBUTE = '_adtr_uac_diagnostic'


def _uac_literal(value, allowed, fallback):
    return value if type(value) is str and value in allowed else fallback


def _uac_args_code(error):
    error_type = type(error)
    # Do not use type-set/tuple membership: hostile metaclasses can implement
    # equality/hash hooks even when type(error) itself did not call user code.
    if not (error_type is UACEvidenceError or error_type is RuntimeError or error_type is ValueError):
        return None
    try:
        args = BaseException.args.__get__(error)
        if type(args) is not tuple or len(args) != 1 or type(args[0]) is not str:
            return None
        return next((code for code in _UAC_CODES if args[0] == 'uac_evidence_invalid:' + code), None)
    except Exception:
        return None


def _uac_owned_diagnostic(error):
    code = _uac_args_code(error)
    if code is None:
        return None
    try:
        attributes = object.__getattribute__(error, '__dict__')
        if type(attributes) is not dict:
            return None
        # A dict lookup can invoke __eq__ on a colliding non-string key even
        # when the dict and exception types are exact. Iterate with the builtin
        # method; inspect only exact-string keys before comparing their value.
        tag = None
        for key, value in dict.items(attributes):
            if type(key) is str and key == _UAC_DIAGNOSTIC_ATTRIBUTE:
                tag = value
                break
        if (type(tag) is tuple and len(tag) == 3 and tag[0] is _UAC_DIAGNOSTIC_MARKER
                and type(tag[1]) is str and tag[1] in _UAC_CODES and tag[1] == code
                and type(tag[2]) is str and tag[2] in _UAC_STAGES):
            return tag[1], tag[2]
    except Exception:
        pass
    return None


def uac_diagnostic_error(code, stage, *, kind='evidence'):
    """Fresh fixed error; never accepts or stores an original exception."""
    code = _uac_literal(code, _UAC_CODES, 'publication')
    stage = _uac_literal(stage, _UAC_STAGES, 'runner_main')
    kind = _uac_literal(kind, frozenset(('evidence', 'runtime', 'value')), 'evidence')
    if kind == 'runtime':
        error = RuntimeError('uac_evidence_invalid:' + code)
    elif kind == 'value':
        error = ValueError('uac_evidence_invalid:' + code)
    else:
        error = UACEvidenceError(code)
    object.__setattr__(error, _UAC_DIAGNOSTIC_ATTRIBUTE, (_UAC_DIAGNOSTIC_MARKER, code, stage))
    return error


def uac_failure_code(error, *, fallback='publication'):
    fallback = _uac_literal(fallback, _UAC_CODES, 'publication')
    owned = _uac_owned_diagnostic(error)
    if owned is not None:
        return owned[0]
    # Only the exact validator error can preserve a code without our own tag.
    return (_uac_args_code(error) or fallback) if type(error) is UACEvidenceError else fallback


def uac_failure_stage(error, *, fallback='runner_main'):
    fallback = _uac_literal(fallback, _UAC_STAGES, 'runner_main')
    owned = _uac_owned_diagnostic(error)
    return owned[1] if owned is not None else fallback


def uac_failure_message(error):
    """No str/repr/getter, traceback or exception-chain inspection."""
    return 'uac_evidence_invalid:' + uac_failure_code(error) + ';stage=' + uac_failure_stage(error)


def configure_uac(env: MutableMapping[str, str], *, nonce: str) -> None:
    require(type(nonce) is str and re.fullmatch(r'[0-9a-f]{32}', nonce) is not None, 'configuration')
    require(not any(env.get('ADTR_E2E_LDAP_DIRECTORY_' + mode) not in (None, '', 'false') for mode in MIXED_MODES), 'configuration')
    require(not env.get('ADTR_E2E_LDAP_EMPTY_TRANSITION_DIR') and
            not any(env.get(k) not in (None, '', 'false') for k in ('ADTR_DIRECTORY_READ_V2_ENABLED', 'ADTR_DIRECTORY_READ_V4_ENABLED', 'ADTR_E2E_USER_ASSET_EXPORT_V3')) and
            env.get('ADTR_E2E_LDAP_DIRECTORY_USER_UAC_V3_PROFILE') in (None, '', PROFILE) and
            env.get(NONCE_ENV) in (None, '', nonce), 'configuration')
    env['ADTR_E2E_USER_ASSET_EXPORT_V3_UAC'] = 'true'
    env[NONCE_ENV] = nonce


def safe_ancestors(path: Path, *, include_leaf=True):
    path = Path(os.path.abspath(path))
    for part in reversed((path if include_leaf else path.parent).parents):
        require(not part.is_symlink(), 'publication')
    require(not (path if include_leaf else path.parent).is_symlink(), 'publication')
    return path


def read_regular(path: Path, cap: int, code='artifact_binding') -> bytes:
    fd = None
    try:
        path = safe_ancestors(path)
        st = path.lstat()
        require(stat.S_ISREG(st.st_mode) and 0 < st.st_size <= cap, code)
        fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
        current = os.fstat(fd)
        require(stat.S_ISREG(current.st_mode) and (current.st_dev, current.st_ino) == (st.st_dev, st.st_ino), code)
        chunks, count = [], 0
        while True:
            chunk = os.read(fd, min(65536, cap + 1 - count))
            if not chunk:
                break
            chunks.append(chunk)
            count += len(chunk)
            require(count <= cap, code)
        require(count == current.st_size and os.fstat(fd).st_size == count, code)
        return b''.join(chunks)
    except Exception:
        raise UACEvidenceError(code) from None
    finally:
        if fd is not None:
            os.close(fd)


def _write_private(path, raw):
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, 'wb') as output:
        output.write(raw)
        output.flush()
        os.fsync(output.fileno())


def _json_bytes(value):
    return (json.dumps(value, ensure_ascii=True, sort_keys=True, separators=(',', ':')) + '\n').encode('utf-8')


def _current(repo):
    try:
        value = checkout_provenance(repo)
        schema(value, 'Provenance', 'provenance')
        return value
    except Exception:
        raise UACEvidenceError('provenance') from None


def capture_uac_provenance(*, repo: Path) -> bytes:
    temp = None
    try:
        repo = safe_ancestors(repo)
        value = _current(repo)
        raw = _json_bytes(value)
        require(len(raw) <= 2 << 20, 'provenance')
        target = repo / PROVENANCE
        safe_ancestors(target.parent)
        target.parent.mkdir(parents=True, exist_ok=True)
        require(not target.exists() and not target.is_symlink(), 'provenance')
        fd, name = tempfile.mkstemp(prefix='.uac-provenance-', dir=target.parent)
        temp = Path(name)
        with os.fdopen(fd, 'wb') as output:
            output.write(raw)
            output.flush()
            os.fsync(output.fileno())
        # Hard-link publication is exclusive and atomic for a regular file.
        os.link(temp, target, follow_symlinks=False)
        return raw
    except Exception:
        raise UACEvidenceError('provenance') from None
    finally:
        if temp is not None:
            temp.unlink(missing_ok=True)


def discover_uac_candidate(*, repo: Path) -> Path:
    try:
        root = safe_ancestors(repo / 'web/test-results')
        require(root.is_dir(), 'candidate_discovery')
        found = []
        for directory, dirs, files in os.walk(root, followlinks=False):
            require(not any((Path(directory) / name).is_symlink() for name in dirs + files), 'candidate_discovery')
            if CANDIDATE_NAME in files:
                found.append(Path(directory) / CANDIDATE_NAME)
        require(len(found) == 1, 'candidate_discovery')
        read_regular(found[0], 8 << 20, 'candidate_discovery')
        return found[0]
    except Exception:
        raise UACEvidenceError('candidate_discovery') from None


def _artifact_names(data):
    names = [PROVENANCE.name, EVIDENCE_NAME, RAW_NAME]
    for case in data['cases']:
        task_id = case['create']['taskID']
        schema(task_id, 'ID', 'artifact_binding')
        names.extend((f'user-assets-v3-{task_id}.xlsx', f'{case["name"]}-ready.png'))
    require(len(set(names)) == len(names) == 9 and all(Path(n).name == n and '/' not in n and '\n' not in n and '\r' not in n for n in names), 'artifact_binding')
    return sorted(names)


def _cap(filename):
    if filename == PROVENANCE.name:
        return 2 << 20
    if filename == EVIDENCE_NAME:
        return 8 << 20
    if filename == RAW_NAME:
        return 256 << 10
    return (32 if filename.endswith('.xlsx') else 10) << 20


def _validate_directory(repo, directory, trusted):
    require(directory.is_dir() and not directory.is_symlink(), 'publication')
    data = strict_json(read_regular(directory / EVIDENCE_NAME, 8 << 20))
    schema(data, 'FinalEvidence')
    names = _artifact_names(data)
    require(sorted(p.name for p in directory.iterdir()) == names, 'publication')
    files = {name: read_regular(directory / name, _cap(name)) for name in names}
    require(equal(strict_json(files[PROVENANCE.name], 2 << 20, 'provenance'), trusted), 'provenance')
    validate_uac_evidence(data, lambda name: files[name], trusted, oracle=load_oracle(repo))
    return tuple(ArtifactDigest(name, sha(files[name]), len(files[name])) for name in names)


def _rename_new_directory(source, target):
    # Linux CI: RENAME_NOREPLACE is atomic even if another process races us.
    # Never fall back to rename that can replace somebody else's empty directory.
    libc = ctypes.CDLL(None, use_errno=True)
    rename = getattr(libc, 'renameat2', None)
    require(rename is not None, 'publication')
    rename.argtypes = [ctypes.c_int, ctypes.c_char_p, ctypes.c_int, ctypes.c_char_p, ctypes.c_uint]
    rename.restype = ctypes.c_int
    require(rename(-100, os.fsencode(source), -100, os.fsencode(target), 1) == 0, 'publication')


def finalize_uac_evidence(*, repo: Path, run_nonce: str, provenance_bytes: bytes, candidate_path: Path,
                          retained_ldap: tuple[bytes, bytes, int], cleanup_receipt: Mapping[str, object]) -> FinalizedUACArtifacts:
    staging = None
    stage = 'finalize_preflight'
    try:
        repo = safe_ancestors(repo)
        schema(run_nonce, 'Nonce', 'configuration')
        stage = 'finalize_cleanup_receipt'
        require(type(retained_ldap) is tuple and len(retained_ldap) == 3 and type(retained_ldap[2]) is int and retained_ldap[2] == 0, 'cleanup')
        cleanup = dict(cleanup_receipt)
        schema(cleanup, 'CleanupReceipt', 'cleanup')
        require(cleanup['nonce'] == run_nonce, 'cleanup')
        stage = 'finalize_provenance'
        trusted = strict_json(provenance_bytes, 2 << 20, 'provenance')
        schema(trusted, 'Provenance', 'provenance')
        require(equal(trusted, _current(repo)), 'provenance')
        require(read_regular(repo / PROVENANCE, 2 << 20, 'provenance') == provenance_bytes, 'provenance')
        stage = 'finalize_candidate_discovery'
        discovered = discover_uac_candidate(repo=repo)
        require(Path(os.path.abspath(candidate_path)) == discovered, 'candidate_discovery')
        stage = 'finalize_candidate_schema'
        candidate = strict_json(read_regular(discovered, 8 << 20))
        schema(candidate, 'CandidateEvidence')
        require(candidate['nonce'] == run_nonce and equal(candidate['provenance'], trusted), 'provenance')
        wire, seal = retained_ldap[:2]
        # Parse originals before making any wrapper; don't normalize their bytes.
        stage = 'finalize_wire'
        strict_json(wire, 160 << 10, 'raw_transcript')
        stage = 'finalize_seal'
        strict_json(seal, 8 << 10, 'terminal_seal')
        stage = 'finalize_wire_binding'
        require(candidate['candidateTranscriptSHA256'] == sha(wire), 'raw_transcript')
        raw = _json_bytes({'format': 1, 'profile': PROFILE, 'nonce': run_nonce,
            'candidateTranscriptBase64': base64.b64encode(wire).decode('ascii'), 'candidateTranscriptSHA256': sha(wire),
            'terminalSealBase64': base64.b64encode(seal).decode('ascii'), 'terminalSealSHA256': sha(seal)})
        require(len(raw) <= 256 << 10, 'raw_transcript')
        stage = 'finalize_schema'
        final = {key: value for key, value in candidate.items() if key not in ('nonce', 'candidateTranscriptSHA256')}
        final.update(rawWire={'filename': RAW_NAME, 'sha256': sha(raw), 'candidateTranscriptSHA256': sha(wire), 'terminalSealSHA256': sha(seal)}, cleanupReceipt=cleanup)
        schema(final, 'FinalEvidence')
        evidence_bytes = _json_bytes(final)
        require(len(evidence_bytes) <= 8 << 20, 'candidate_schema')
        stage = 'finalize_artifact_inventory'
        names = _artifact_names(final)
        payloads = {PROVENANCE.name: provenance_bytes, EVIDENCE_NAME: evidence_bytes, RAW_NAME: raw}
        stage = 'finalize_artifact_read'
        for name in names:
            if name not in payloads:
                payloads[name] = read_regular(discovered.parent / name, _cap(name))
        stage = 'finalize_validate'
        validate_uac_evidence(final, lambda name: payloads[name], trusted, oracle=load_oracle(repo))
        stage = 'finalize_destination'
        destination = safe_ancestors(repo / ARTIFACTS)
        require(not destination.exists(), 'publication')
        destination.parent.mkdir(parents=True, exist_ok=True)
        stage = 'finalize_stage_write'
        staging = Path(tempfile.mkdtemp(prefix='.uac-validated-', dir=destination.parent))
        for name in names:
            _write_private(staging / name, payloads[name])
        stage = 'finalize_stage_readback'
        digests = _validate_directory(repo, staging, trusted)
        stage = 'finalize_atomic_publish'
        _rename_new_directory(staging, destination)
        staging = None
        return FinalizedUACArtifacts(ARTIFACTS.as_posix(), digests)
    except UACEvidenceError as error:
        raise uac_diagnostic_error(uac_failure_code(error), uac_failure_stage(error, fallback=stage)) from None
    except Exception as error:
        raise uac_diagnostic_error(uac_failure_code(error), uac_failure_stage(error, fallback=stage)) from None
    finally:
        if staging is not None:
            primary = sys.exc_info()[1]
            try:
                shutil.rmtree(staging)
            except Exception:
                if primary is None:
                    raise uac_diagnostic_error('publication', 'finalize_stage_cleanup') from None


def _github_output(paths):
    fd = None
    try:
        output = Path(os.environ['GITHUB_OUTPUT'])
        temp = Path(os.environ['RUNNER_TEMP'])
        require(output.is_absolute() and temp.is_absolute() and '..' not in output.parts and '..' not in temp.parts, 'publication')
        output, temp = safe_ancestors(output), safe_ancestors(temp)
        require(output.is_relative_to(temp), 'publication')
        require(stat.S_ISREG(output.lstat().st_mode), 'publication')
        fd = os.open(output, os.O_RDWR | os.O_APPEND | os.O_NOFOLLOW | os.O_NONBLOCK)
        fcntl.flock(fd, fcntl.LOCK_EX)
        require(stat.S_ISREG(os.fstat(fd).st_mode) and os.fstat(fd).st_size <= 1 << 20, 'publication')
        previous = os.read(fd, (1 << 20) + 1).decode('utf-8', 'strict')
        require((not previous or previous.endswith('\n')) and re.search(r'(?m)^artifact_paths(?:=|<<)', previous) is None, 'publication')
        delimiter = 'ADTR_UAC_PATHS_' + secrets.token_hex(16)
        require(len(paths) == 9 and all('\n' not in p and '\r' not in p and delimiter not in p for p in paths), 'publication')
        value = ('artifact_paths<<' + delimiter + '\n' + '\n'.join(paths) + '\n' + delimiter + '\n').encode('utf-8')
        require(os.write(fd, value) == len(value), 'publication')
        os.fsync(fd)
    except Exception:
        raise UACEvidenceError('publication') from None
    finally:
        if fd is not None:
            os.close(fd)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=('capture', 'validate'))
    parser.add_argument('--repo', type=Path, required=True)
    parser.add_argument('--github-output', action='store_true')
    args = parser.parse_args()
    try:
        if args.action == 'capture':
            require(not args.github_output, 'configuration')
            capture_uac_provenance(repo=args.repo)
        else:
            repo = safe_ancestors(args.repo)
            trusted = _current(repo)
            files = _validate_directory(repo, safe_ancestors(repo / ARTIFACTS), trusted)
            if args.github_output:
                _github_output([(ARTIFACTS / item.filename).as_posix() for item in files])
    except Exception as error:
        print(uac_failure_message(error), file=sys.stderr)
        return 1
    return 0


if __name__ == '__main__':
    sys.exit(main())
