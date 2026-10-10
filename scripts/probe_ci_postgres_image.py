"""Focused real Docker proof for the same image transfer used by full CI."""
import argparse
import json
import os
from pathlib import Path
import secrets
import shutil
import subprocess
import tempfile
import time
import uuid

from ci_postgres_image import (IMAGE_ID, REFERENCE, ImageTransferError, consume,
                               postgres_image_args, require)
from e2e_owned_container import OWNER_LABEL, remove_owned_container


def prove_negative_paths(directory, archive_sha, producer_attempt, environ=os.environ, run=subprocess.run):
    checked = []

    def no_docker(command, **kwargs):
        require(command[0] != 'docker', 'Negative probe reached Docker before rejecting invalid input')
        return run(command, **kwargs)

    for scenario in ['wrong-archive-sha', 'wrong-run', 'wrong-checkout', 'corrupt-archive']:
        with tempfile.TemporaryDirectory(prefix='adtr-image-negative-') as temporary:
            candidate = Path(temporary) / 'bundle'
            shutil.copytree(directory, candidate)
            expected_sha = archive_sha
            current_env = dict(environ)
            if scenario == 'wrong-archive-sha':
                expected_sha = '0' * 64 if archive_sha != '0' * 64 else '1' * 64
            elif scenario == 'wrong-run':
                current_env['GITHUB_RUN_ID'] = str(int(current_env['GITHUB_RUN_ID']) + 1)
            elif scenario == 'wrong-checkout':
                metadata = json.loads((candidate / 'metadata.json').read_text())
                metadata['checkout_sha'] = '0' * 40
                (candidate / 'metadata.json').write_text(json.dumps(metadata))
            else:
                archive = candidate / 'postgres.tar.gz'
                with archive.open('r+b') as stream:
                    first = stream.read(1)
                    stream.seek(0)
                    stream.write(bytes([first[0] ^ 1]))
            try:
                consume(candidate, expected_sha, producer_attempt, current_env, no_docker)
            except ImageTransferError as error:
                require('reached Docker' not in str(error), str(error))
                checked.append(scenario)
            else:
                raise ImageTransferError(f'Negative probe unexpectedly accepted {scenario}')
    return checked


def prove_database(environ=os.environ, run=subprocess.run, pause=time.sleep):
    name = 'adtr-auth-e2e-' + uuid.uuid4().hex[:12]
    owner = uuid.uuid4().hex
    env = dict(environ, POSTGRES_PASSWORD=secrets.token_hex(24))
    image_args = postgres_image_args(REFERENCE, environ, run)
    require(image_args == ['--pull=never', IMAGE_ID], 'Probe must use the verified loaded image without pulling')
    try:
        run(['docker', 'run', '--detach', '--rm', '--name', name, '--network', 'none',
             '--label', f'{OWNER_LABEL}={owner}', '-e', 'POSTGRES_PASSWORD', *image_args],
            env=env, check=True, capture_output=True, text=True, timeout=60)
        for _ in range(120):
            ready = run(['docker', 'exec', name, 'pg_isready', '-h', '127.0.0.1', '-U', 'postgres', '-d', 'postgres'],
                        check=False, capture_output=True, timeout=5)
            if ready.returncode == 0:
                break
            pause(0.5)
        else:
            raise ImageTransferError('Transferred PostgreSQL did not become ready')
        actual_id = run(['docker', 'inspect', '--format', '{{.Image}}', name],
                        check=True, capture_output=True, text=True, timeout=10).stdout.strip()
        require(actual_id == IMAGE_ID, 'Actual database container image ID mismatch')
        version = run(['docker', 'exec', name, 'psql', '-U', 'postgres', '-tAc',
                       "SELECT current_setting('server_version_num')"],
                      check=True, capture_output=True, text=True, timeout=10).stdout.strip()
        require(version == '170006', 'Unexpected real PostgreSQL version')
        return {'database_ready': True, 'server_version_num': version,
                'container_image_id': actual_id, 'pull_policy': 'never', 'network': 'none'}
    finally:
        remove_owned_container(name, owner, run)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--directory', required=True)
    parser.add_argument('--output', required=True)
    args = parser.parse_args()
    started = time.monotonic()
    archive_sha = os.environ.get('ADTR_CI_POSTGRES_ARCHIVE_SHA', '')
    attempt = os.environ.get('ADTR_CI_POSTGRES_PRODUCER_ATTEMPT', '')
    negative = prove_negative_paths(args.directory, archive_sha, attempt)
    before_load = time.monotonic()
    consume(args.directory, archive_sha, attempt)
    consume_seconds = time.monotonic() - before_load
    env = dict(os.environ, ADTR_CI_POSTGRES_BUNDLE=args.directory)
    result = prove_database(env)
    metadata = json.loads((Path(args.directory) / 'metadata.json').read_text())
    result.update(negative_cases=negative, consume_seconds=round(consume_seconds, 3),
                  probe_seconds=round(time.monotonic() - started, 3),
                  archive_bytes=metadata['archive_bytes'], archive_sha256=archive_sha,
                  run_id=metadata['run_id'], checkout_sha=metadata['checkout_sha'],
                  producer_attempt=attempt)
    Path(args.output).write_text(json.dumps(result, indent=2, sort_keys=True) + '\n')
    print(json.dumps(result, sort_keys=True))


if __name__ == '__main__':
    main()
