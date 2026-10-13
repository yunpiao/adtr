"""Evidence validator must reject plausible but wrong-head/member results."""
import base64
import copy
import hashlib
import json
from pathlib import Path
import unittest
from unittest.mock import patch
import validate_directory_v4_evidence as validator


def evidence():
    provenance = {'checkoutCommit': 'a' * 40, 'checkoutTree': 'b' * 40,
                  'eventHeadCommit': 'c' * 40, 'eventBaseCommit': 'd' * 40,
                  'workflowCommit': 'a' * 40, 'runId': '123', 'runAttempt': '2', 'eventName': 'pull_request',
                  'sourceDigests': {p: 'e' * 64 for p in validator.SOURCES}}
    rows, members, identities = [], [], []
    for i in range(1, 31):
        raw = None if i <= 13 or i > 24 else [] if i == 14 else [
            'CN=user-01,OU=Users,DC=synthetic,DC=invalid', 'CN=group-02,OU=Groups,DC=synthetic,DC=invalid',
            'CN=S-1-5-21-foreign,CN=ForeignSecurityPrincipals,DC=elsewhere,DC=invalid', 'CN=raw\x00\n😀<script>,DC=synthetic,DC=invalid',
        ] if i == 15 else [f'CN=member-{i:02d},OU=Users,DC=synthetic,DC=invalid']
        row = dict.fromkeys(['distinguishedName', 'objectClass', 'samAccountName', 'userAccountControl', 'objectSid', 'mail', 'description', 'whenCreated', 'primaryGroupID', 'servicePrincipalName'])
        row.update(objectGUID=f'{i:08x}-0000-0000-0000-000000000000', kind='group' if 13 <= i <= 24 else 'user',
                   rawMember=raw, returnedMemberCount=None if raw is None else len(raw), memberReadState='not_returned' if raw is None else 'returned')
        rows.append(row)
        members.append({'memberBytes': None if raw is None else [base64.b64encode(v.encode()).decode() for v in raw]})
        identities.append({'primaryGroupIDBytes': None, 'servicePrincipalNameBytes': None})
    data = {'format': 1, 'taskUUID': 'task', 'sha256': 'f' * 64,
            'source': {'server_name': 'dc.synthetic.invalid', 'dc_host_name': 'dc.synthetic.invalid', 'domain': 'synthetic.invalid',
                       'naming_context': 'dc=synthetic,dc=invalid', 'started_at': '2026-10-12T00:00:00Z', 'completed_at': '2026-10-12T00:00:01Z', 'elapsed_milliseconds': 1000, 'pages': 2},
            'purpose': 'domain.directory_read.v4', 'reason': 'executor_returned', 'dependencies': 0,
            'objects': rows, 'storedIdentity': identities, 'storedMembership': members, 'provenance': provenance}
    body = {'dictionaryVersion': 4, 'source': data['source'], 'objects': [
        {'base': {key: row[key] for key in ['objectGUID', 'distinguishedName', 'kind', 'objectClass', 'samAccountName', 'userAccountControl']},
         'supplemental': {}, 'identity': identity, 'membership': member}
        for row, identity, member in zip(rows, identities, members)]}
    raw = json.dumps(body).encode()
    data['bodyBase64'] = base64.b64encode(raw).decode()
    data['sha256'] = hashlib.sha256(raw).hexdigest()
    return data


class DirectoryV4EvidenceContract(unittest.TestCase):
    def test_exact_head_and_distinct_merge_head_provenance(self):
        data = evidence()
        validator.validate(data, data['provenance'])
        for field in data['provenance']:
            altered = copy.deepcopy(data)
            altered['provenance'][field] = {} if field == 'sourceDigests' else 'wrong'
            with self.subTest(field=field), self.assertRaises(ValueError):
                validator.validate(altered, data['provenance'])
        altered = copy.deepcopy(data)
        altered['provenance']['checkoutCommit'] = altered['provenance']['eventHeadCommit']
        with self.assertRaises(ValueError):
            validator.validate(altered, data['provenance'])

    def test_missing_empty_partial_and_wrong_count_fail(self):
        data = evidence()
        mutations = [lambda d: d.pop('provenance'), lambda d: d.update(sha256='0' * 64), lambda d: d.update(bodyBase64=base64.b64encode(b'{}').decode()), lambda d: d['objects'].pop(),
                     lambda d: d.update(dependencies=1), lambda d: d.update(purpose='domain.directory_read.v3'),
                     lambda d: d['objects'][12].update(rawMember=[], returnedMemberCount=0, memberReadState='returned'),
                     lambda d: d['objects'][13].update(rawMember=None, returnedMemberCount=None, memberReadState='not_returned'),
                     lambda d: d['objects'][14].update(returnedMemberCount=3),
                     lambda d: d['storedMembership'][14].update(memberBytes=[]),
                     lambda d: d['objects'][14].update(memberCount=4),
                     lambda d: d['objects'][14].update(kind='user')]
        for mutate in mutations:
            altered = copy.deepcopy(data); mutate(altered)
            with self.assertRaises(ValueError):
                validator.validate(altered, data['provenance'])

    def test_shallow_checkout_uses_supplied_event_base_without_parent_lookup(self):
        env = {'GITHUB_ACTIONS': 'true', 'GITHUB_SHA': 'a' * 40, 'ADTR_EVENT_HEAD_SHA': 'c' * 40,
               'ADTR_EVENT_BASE_SHA': 'd' * 40, 'GITHUB_RUN_ID': '123', 'GITHUB_RUN_ATTEMPT': '2', 'GITHUB_EVENT_NAME': 'pull_request'}
        def shallow(root, *args):
            if args == ('rev-parse', 'HEAD'): return 'a' * 40
            if args == ('rev-parse', 'HEAD^{tree}'): return 'b' * 40
            if args[:2] == ('diff', 'HEAD'): return ''
            raise AssertionError('unavailable shallow parent was read')
        with patch.object(validator, 'git', side_effect=shallow), patch.object(Path, 'read_bytes', return_value=b'source'):
            actual = validator.expected_provenance(Path('.'), env)
        self.assertEqual(actual['eventBaseCommit'], 'd' * 40)
        self.assertEqual(actual['checkoutCommit'], 'a' * 40)
        self.assertEqual(actual['eventHeadCommit'], 'c' * 40)

    def test_ci_context_and_dirty_sources_fail_closed(self):
        with patch.object(validator, 'git', side_effect=['a' * 40, 'b' * 40, '']):
            with self.assertRaisesRegex(ValueError, 'missing CI provenance'):
                validator.expected_provenance(Path('.'), {'GITHUB_ACTIONS': 'true'})
        with patch.object(validator, 'git', side_effect=['a' * 40, 'b' * 40, 'diff']):
            with self.assertRaisesRegex(ValueError, 'modified evidence sources'):
                validator.expected_provenance(Path('.'), {})


if __name__ == '__main__':
    unittest.main()
