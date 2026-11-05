"""Current-only exact inverse of Issue 286 evidence wiring, never runtime policy.

Full-file and fixture pins are independent of mutable fixture metadata. Historical
contracts opt into explicit predecessor fixtures; current entrypoints cannot.
"""
import hashlib
import json
from pathlib import Path

DELTA = 'scripts/fixtures/role_users_read_ci_delta.json'
DELTA_SHA256 = 'f0d56c5cd88ffe54f82ddb653d52d70728639920900cc3b45f140b8503dc42f9'
ANCHORS = {'workflow': ('a3b5ec391fb478352d289c032e19e52d617d9b08d769403562b10a63219e7a3b', '90091e86fc5da2d73f931b417a411725555a61608bb7117020c3bf88acc1b9e9'), 'helper': ('ba9c22970570006d3bf720cae5c8bd0512ce32be9f9b7d06d9047eecbbce5484', '35dddef9007222fa2bcacd954a24d06ef7ef7f8808f2af4299ddf9d5527788fc')}


def restore_role_users_read(text, domain='workflow'):
    """Recover exactly current-main predecessor bytes or fail closed."""
    try:
        if type(text) is not str or type(domain) is not str or domain not in ANCHORS:
            raise ValueError
        baseline, current = ANCHORS[domain]
        if hashlib.sha256(text.encode('utf-8', errors='strict')).hexdigest() != current:
            raise ValueError
        raw = Path(DELTA).read_bytes()
        # Authenticate the complete bounded bytes before parsing: duplicate keys,
        # altered metadata, noncanonical encodings and rehashed deltas all fail.
        if not 0 < len(raw) <= 16384 or hashlib.sha256(raw).hexdigest() != DELTA_SHA256:
            raise ValueError
        delta = json.loads(raw.decode('utf-8', errors='strict'))
        for change in reversed(delta['files'][domain]['replacements']):
            if text.count(change['new']) != 1:
                raise ValueError
            text = text.replace(change['new'], change['old'], 1)
        if hashlib.sha256(text.encode('utf-8')).hexdigest() != baseline:
            raise ValueError
        return text
    except (OSError, UnicodeError, ValueError, TypeError, KeyError, RecursionError):
        raise ValueError('role_users_read_workflow_invalid') from None
