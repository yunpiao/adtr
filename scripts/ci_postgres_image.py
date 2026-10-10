"""Same-run PostgreSQL image transfer with a fixed content-addressed trust root.

The registry source, tag and index digest remain unchanged. Docker save/load may
lose RepoDigests; consumers execute only the verified config/image ID, without a
pull fallback. This is test infrastructure, never a production image publisher.
"""
import argparse
from contextlib import contextmanager
import gzip
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile
import time

REFERENCE = ('public.ecr.aws/docker/library/postgres:17.6-alpine@sha256:'
             'ef257d85f76e48da1c64832459b59fcaba1a4dac97bf5d7450c77753542eee94')
INDEX_DIGEST = REFERENCE.split('@')[1]
MANIFEST_DIGEST = 'sha256:747d5ed1fdeeb124b880fbe3d7c6557d2c4064ae41d6b6297d417882effce4be'
IMAGE_ID = 'sha256:d741b376874687de90374fd34f55c6b2760e8f7bd7e4ae5cd47f50757fc08cf8'
INDEX_TYPE = 'application/vnd.oci.image.index.v1+json'
MANIFEST_TYPE = 'application/vnd.oci.image.manifest.v1+json'
CONFIG_TYPE = 'application/vnd.oci.image.config.v1+json'
FILES = {'index.json', 'manifest.json', 'metadata.json', 'postgres.tar.gz'}


class ImageTransferError(RuntimeError):
    pass


def require(condition, message):
    if not condition:
        raise ImageTransferError(message)


def sha256_bytes(data):
    return 'sha256:' + hashlib.sha256(data).hexdigest()


def sha256_file(path):
    digest = hashlib.sha256()
    with path.open('rb') as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b''):
            digest.update(chunk)
    return digest.hexdigest()


def read_json(raw, description):
    try:
        result = json.loads(raw)
        require(isinstance(result, dict), f'{description} must be a JSON object')
        return result
    except (ValueError, TypeError) as error:
        raise ImageTransferError(f'{description} contains invalid JSON') from error


def validate_index(raw):
    require(sha256_bytes(raw) == INDEX_DIGEST, 'Fixed registry index digest mismatch')
    index = read_json(raw, 'index')
    require(index.get('schemaVersion') == 2 and index.get('mediaType') == INDEX_TYPE,
            'Unsupported index schema or media type')
    manifests = index.get('manifests')
    require(isinstance(manifests, list) and all(isinstance(item, dict) for item in manifests),
            'Index manifests must be descriptors')
    candidates = [item for item in manifests if item.get('platform') == {'architecture': 'amd64', 'os': 'linux'}]
    require(len(candidates) == 1, 'Expected exactly one unambiguous linux/amd64 manifest')
    child = candidates[0]
    require(child.get('mediaType') == MANIFEST_TYPE and child.get('digest') == MANIFEST_DIGEST
            and type(child.get('size')) is int and child['size'] > 0,
            'Fixed platform manifest descriptor mismatch')
    return child


def validate_chain(index_raw, manifest_raw):
    child = validate_index(index_raw)
    require(len(manifest_raw) == child['size'] and sha256_bytes(manifest_raw) == child['digest'],
            'Platform manifest bytes do not match the fixed index')
    manifest = read_json(manifest_raw, 'manifest')
    require(manifest.get('schemaVersion') == 2 and manifest.get('mediaType') == MANIFEST_TYPE,
            'Unsupported manifest schema or media type')
    config = manifest.get('config')
    require(isinstance(config, dict) and config.get('mediaType') == CONFIG_TYPE
            and config.get('digest') == IMAGE_ID and type(config.get('size')) is int and config['size'] > 0,
            'Fixed config/image digest mismatch')
    layers = manifest.get('layers')
    require(isinstance(layers, list) and len(layers) == 10 and all(
        isinstance(layer, dict) and layer.get('mediaType') == 'application/vnd.oci.image.layer.v1.tar+gzip'
        and re.fullmatch(r'sha256:[0-9a-f]{64}', str(layer.get('digest', '')))
        and type(layer.get('size')) is int and layer['size'] > 0 for layer in layers),
        'Invalid fixed-image layer descriptors')
    return IMAGE_ID


def context(environ=os.environ, run=subprocess.run):
    require(re.fullmatch(r'[1-9][0-9]*', environ.get('GITHUB_RUN_ID', '')) is not None,
            'A valid current GitHub run ID is required')
    require(re.fullmatch(r'[1-9][0-9]*', environ.get('GITHUB_RUN_ATTEMPT', '')) is not None,
            'A valid current GitHub run attempt is required')
    sha = run(['git', 'rev-parse', 'HEAD'], check=True, capture_output=True, text=True, timeout=15).stdout.strip()
    require(re.fullmatch(r'[0-9a-f]{40}', sha) is not None, 'Invalid actual checkout SHA')
    event = {key: environ.get(key, '') for key in ['ADTR_EVENT_HEAD_SHA', 'ADTR_EVENT_BASE_SHA']}
    require(all(not value or re.fullmatch(r'[0-9a-f]{40}', value) for value in event.values()),
            'Invalid event head/base SHA')
    return {'run_id': environ['GITHUB_RUN_ID'], 'checkout_sha': sha, 'event': event}


def validate_bundle(directory, expected_archive_sha, current, producer_attempt):
    directory = Path(directory)
    require(directory.is_dir() and not directory.is_symlink(), 'Image bundle directory is missing or symlinked')
    paths = {p.name for p in directory.iterdir()}
    require(paths in (FILES, FILES | {'verified.json'}), 'Image bundle has missing or unexpected files')
    for name in paths:
        path = directory / name
        require(path.is_file() and not path.is_symlink(), f'Image bundle file is missing or unsafe: {name}')
    require(re.fullmatch(r'[0-9a-f]{64}', expected_archive_sha or '') is not None,
            'Expected archive SHA256 must come from the producer job output')
    for name in ('index.json', 'manifest.json', 'metadata.json'):
        require((directory / name).stat().st_size <= 1024 * 1024, f'Oversized image metadata: {name}')
    validate_chain((directory / 'index.json').read_bytes(), (directory / 'manifest.json').read_bytes())
    metadata = read_json((directory / 'metadata.json').read_bytes(), 'metadata')
    require(metadata.get('schema') == 1 and metadata.get('reference') == REFERENCE
            and metadata.get('platform') == 'linux/amd64' and metadata.get('image_id') == IMAGE_ID,
            'Image metadata source/platform/identity mismatch')
    require(all(metadata.get(key) == value for key, value in current.items()),
            'Image bundle belongs to a different run, checkout or event')
    require(re.fullmatch(r'[1-9][0-9]*', producer_attempt or '') is not None
            and metadata.get('producer_attempt') == producer_attempt,
            'Image producer attempt mismatch')
    require(metadata.get('archive_sha256') == expected_archive_sha, 'Image metadata archive digest mismatch')
    archive = directory / 'postgres.tar.gz'
    require(type(metadata.get('archive_bytes')) is int and metadata['archive_bytes'] == archive.stat().st_size
            and metadata['archive_bytes'] > 0, 'Image archive size mismatch')
    require(sha256_file(archive) == expected_archive_sha, 'Image archive SHA256 mismatch before docker load')
    return metadata


def inspect_loaded(run=subprocess.run):
    result = run(['docker', 'image', 'inspect', IMAGE_ID], check=True, capture_output=True, text=True, timeout=15)
    try:
        images = json.loads(result.stdout)
        require(isinstance(images, list) and len(images) == 1 and isinstance(images[0], dict),
                'Expected exactly one loaded image')
        image = images[0]
        require(image.get('Id') == IMAGE_ID and image.get('Os') == 'linux'
                and image.get('Architecture') == 'amd64', 'Loaded image identity/platform mismatch')
    except (ValueError, TypeError) as error:
        raise ImageTransferError('Invalid loaded image inspection') from error
    return image


def registry_bytes(reference, run=subprocess.run, pause=time.sleep):
    from prepare_ci_image import diagnostic, forbidden_failure, retryable, RETRY_DELAYS
    for attempt in range(len(RETRY_DELAYS) + 1):
        try:
            result = run(['docker', 'buildx', 'imagetools', 'inspect', '--raw', reference],
                         check=False, capture_output=True, timeout=60)
        except subprocess.TimeoutExpired as error:
            message = diagnostic(error)
            require(not forbidden_failure(message), 'Non-retryable registry metadata timeout')
        else:
            if result.returncode == 0:
                return result.stdout  # Hash original bytes; never JSON-reserialize.
            message = diagnostic(result)
            require(retryable(message), f'Non-retryable registry metadata error: {message}')
        require(attempt < len(RETRY_DELAYS), 'Registry metadata retries exhausted')
        pause(RETRY_DELAYS[attempt])
    raise AssertionError('unreachable registry request state')


def write_output(values, environ=os.environ):
    output = environ.get('GITHUB_OUTPUT')
    require(bool(output), 'GITHUB_OUTPUT is required for producer outputs')
    with open(output, 'a') as stream:
        for key, value in values.items():
            require(re.fullmatch(r'[a-z0-9_]+', key) is not None and '\n' not in str(value), 'Unsafe workflow output')
            stream.write(f'{key}={value}\n')


def produce(directory, environ=os.environ, run=subprocess.run):
    from prepare_ci_image import prepare_image
    started = time.monotonic()
    current = context(environ, run)
    prepare_image(run=run)
    pulled = time.monotonic()
    index = registry_bytes(REFERENCE, run=run)
    child = validate_index(index)
    manifest = registry_bytes(REFERENCE.split('@')[0].rsplit(':', 1)[0] + '@' + child['digest'], run=run)
    validate_chain(index, manifest)
    manifested = time.monotonic()
    directory = Path(directory)
    directory.mkdir(parents=True, exist_ok=False)
    (directory / 'index.json').write_bytes(index)
    (directory / 'manifest.json').write_bytes(manifest)
    # docker save's file is ephemeral and contains only the fixed public image.
    with tempfile.TemporaryDirectory(prefix='adtr-image-save-') as temporary:
        raw = Path(temporary) / 'postgres.tar'
        run(['docker', 'image', 'save', '--output', str(raw), IMAGE_ID], check=True, timeout=180)
        with raw.open('rb') as source, (directory / 'postgres.tar.gz').open('wb') as target:
            with gzip.GzipFile(fileobj=target, mode='wb', compresslevel=1, mtime=0) as compressed:
                shutil.copyfileobj(source, compressed)
    saved = time.monotonic()
    archive = directory / 'postgres.tar.gz'
    archive_sha = sha256_file(archive)
    metadata = {**current, 'schema': 1, 'producer_attempt': environ['GITHUB_RUN_ATTEMPT'],
                'reference': REFERENCE, 'platform': 'linux/amd64', 'image_id': IMAGE_ID,
                'archive_sha256': archive_sha, 'archive_bytes': archive.stat().st_size}
    (directory / 'metadata.json').write_text(json.dumps(metadata, indent=2, sort_keys=True) + '\n')
    validate_bundle(directory, archive_sha, current, environ['GITHUB_RUN_ATTEMPT'])
    write_output({'archive_sha256': archive_sha, 'producer_attempt': environ['GITHUB_RUN_ATTEMPT'],
                  'checkout_sha': current['checkout_sha']}, environ)
    print(json.dumps({'phase': 'prepare', 'seconds': round(time.monotonic() - started, 3),
                      'pull_seconds': round(pulled - started, 3),
                      'registry_metadata_seconds': round(manifested - pulled, 3),
                      'save_compress_seconds': round(saved - manifested, 3), **metadata}, sort_keys=True))


def consume(directory, expected_archive_sha, producer_attempt, environ=os.environ, run=subprocess.run):
    started = time.monotonic()
    current = context(environ, run)
    metadata = validate_bundle(directory, expected_archive_sha, current, producer_attempt)
    run(['docker', 'image', 'load', '--input', str(Path(directory) / 'postgres.tar.gz')], check=True, timeout=180)
    inspect_loaded(run)
    # The receipt binds the already-validated producer output to runtime setup.
    (Path(directory) / 'verified.json').write_text(json.dumps(metadata, sort_keys=True) + '\n')
    print(json.dumps({'phase': 'consume', 'seconds': round(time.monotonic() - started, 3),
                      'archive_bytes': metadata['archive_bytes'], 'image_id': IMAGE_ID}, sort_keys=True))


def postgres_image_args(original, environ=os.environ, run=subprocess.run):
    require(original == REFERENCE, 'Runtime PostgreSQL source pin mismatch')
    if 'ADTR_CI_POSTGRES_BUNDLE' not in environ:
        return [original]  # Local developer flow preserves the existing pin.
    directory = Path(environ['ADTR_CI_POSTGRES_BUNDLE'])
    receipt_path = directory / 'verified.json'
    require(receipt_path.is_file() and not receipt_path.is_symlink(), 'Verified image receipt is required')
    receipt = read_json(receipt_path.read_bytes(), 'verified image receipt')
    metadata = validate_bundle(directory, environ.get('ADTR_CI_POSTGRES_ARCHIVE_SHA', ''), context(environ, run),
                               environ.get('ADTR_CI_POSTGRES_PRODUCER_ATTEMPT', ''))
    require(receipt == metadata, 'Verified image receipt differs from checked bundle')
    inspect_loaded(run)
    return ['--pull=never', IMAGE_ID]


@contextmanager
def postgres_compose_files(environ=os.environ, run=subprocess.run):
    args = postgres_image_args(REFERENCE, environ, run)
    if args == [REFERENCE]:
        yield []
        return
    with tempfile.TemporaryDirectory(prefix='adtr-postgres-compose-') as directory:
        path = Path(directory) / 'postgres.json'
        path.write_text(json.dumps({'services': {'db': {'image': IMAGE_ID, 'pull_policy': 'never'}}}))
        yield ['-f', str(Path('compose.yaml').resolve()), '-f', str(path)]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('operation', choices=['produce', 'consume'])
    parser.add_argument('--directory', required=True)
    args = parser.parse_args()
    try:
        if args.operation == 'produce':
            produce(args.directory)
        else:
            consume(args.directory, os.environ.get('ADTR_CI_POSTGRES_ARCHIVE_SHA', ''),
                    os.environ.get('ADTR_CI_POSTGRES_PRODUCER_ATTEMPT', ''))
    except (ImageTransferError, OSError, subprocess.SubprocessError) as error:
        print(f'PostgreSQL image preparation/transfer failed: {error}', file=sys.stderr)
        return 1
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
