"""Exact test expectation from the checked-out application contract, never runtime data."""
from pathlib import Path
import re

MAX_SOURCE_BYTES = 4096


def parse_current_schema(source):
    if not isinstance(source, bytes) or len(source) > MAX_SOURCE_BYTES:
        raise ValueError('invalid application schema source size')
    text = source.decode('ascii')
    # Only whole-line Go comments are accepted; expressions and extra declarations fail closed.
    text = re.sub(r'(?m)^[ \t]*//[^\r\n]*', '', text)
    match = re.fullmatch(r'\s*package schemaversion\s+const Current = ([1-9][0-9]{0,5})\s*', text)
    if match is None:
        raise ValueError('invalid application schema declaration')
    return int(match[1])


def current_schema():
    source = Path(__file__).resolve().parents[1] / 'internal/schemaversion/version.go'
    with source.open('rb') as stream:
        return parse_current_schema(stream.read(MAX_SOURCE_BYTES + 1))


CURRENT_SCHEMA = current_schema()
