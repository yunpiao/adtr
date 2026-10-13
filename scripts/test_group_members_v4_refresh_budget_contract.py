"""Static actor budgets for the real refresh suite; never execute/retry requests."""
from collections import Counter
from pathlib import Path
import re
import unittest

ROOT = Path(__file__).resolve().parents[1]
SPEC = 'web/e2e/group-members-v4-refresh.spec.ts'


def phases(source):
    body = source[source.index('test("group members dictionary4 refresh'):]
    bootstrap, producer = body.split('  await login(producerPage, producerUsername, producerInitial);', 1)
    producer, reader = producer.split('  const context = await browser.newContext(', 1)
    reader, continuation = reader.split('  const { ui, transition } = await captureRefreshMembers(', 1)
    return bootstrap, producer + continuation, reader


def budget(source, page):
    # Count actual API call sites, not the explanatory budget comment. An enroll
    # helper performs two auth writes, while updating the password performs one.
    routes = re.findall(r'\bsubmit\(\s*' + page + r',\s*"([^"]+)"', source)
    routes += re.findall(r'(?<![.\w])post\(\s*"([^"]+)"', source)
    routes += [prefix + '/grant' for prefix in re.findall(
        r'\bgrant\(\s*' + page + r',\s*password,\s*auth,\s*"([^"]+)"', source)]
    password = len(re.findall(r'name: "更新密码", exact: true', source))
    enrollment = len(re.findall(r'\benrollMfa\(\s*' + page + r',', source))
    return Counter(routes), len(routes) + password + 2 * enrollment


class RefreshBudgetContract(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.source = (ROOT / SPEC).read_text()

    def test_actual_actor_request_routes_fit_unchanged_sensitive_budget(self):
        bootstrap, producer, reader = phases(self.source)
        self.assertEqual(budget(bootstrap, 'producerPage'), (Counter({
            '/api/domains/create-unconfigured': 1,
            '/api/resources/groups/create': 1,
            '/api/resources/groups/assign': 2,
            '/api/access/roles/save': 1,
            '/api/access/users/create': 2,
        }), 10))
        self.assertEqual(budget(producer, 'producerPage'), (Counter({
            '/api/operation-accounts/create': 1,
            '/api/credential-use/grant': 1,
            '/api/domains/credential-source/reference': 1,
            '/api/directory-credential-use/v4/grant': 1,
            '/api/directory/v4/sync': 2,
            '/api/directory-credential-use/v4/revoke': 1,
        }), 10))
        self.assertEqual(budget(reader, 'page'), (Counter(), 3))
        # Auth/me/access/check are reads or non-sensitive authorization queries.
        self.assertEqual(self.source.count('const post = async ('), 1)
        self.assertEqual(self.source.count('producerContext.request.post(path,'), 1)
        self.assertEqual(len(re.findall(r'\bpost\(\s*"/api/directory/v4/sync"', producer)), 2)
        self.assertEqual(len(re.findall(r'\bpost\(\s*"/api/directory-credential-use/v4/revoke"', producer)), 1)

    def test_original_refusal_suites_still_execute_real_denied_sync(self):
        for name in ('group-members-v4', 'group-members-v4-empty', 'group-members-v4-pagination'):
            with self.subTest(suite=name):
                text = (ROOT / f'web/e2e/{name}.spec.ts').read_text()
                self.assertRegex(text, r'const denied = await post\("/api/directory/v4/sync", sync, 403\);')
                self.assertIn('expect(denied).toEqual({ error: "forbidden" });', text)
                self.assertIn('purpose: "domain.connection_test",', text)

    def test_production_rate_and_mfa_order_remain_authoritative(self):
        service = (ROOT / 'internal/auth/service.go').read_text()
        access = (ROOT / 'internal/auth/access_http.go').read_text()
        directory = (ROOT / 'internal/auth/directory_http.go').read_text()
        credential = (ROOT / 'internal/auth/credential_use_http.go').read_text()
        self.assertIn('s.now().Add(-15*time.Minute)', service)
        self.assertIn('if count > limit {', service)
        self.assertIn('return fail(429, "rate_limited")', service)
        for text in (service, access):
            self.assertIn('s.rate(ctx, "sensitive:"+strconv.FormatInt(u.ID, 10), 10)', text)
        self.assertLess(directory.index('s.requireAccessProof('), directory.index('store.SubmitDirectoryV4Tx('))
        self.assertIn('s.requireAccessProof(ctx, tx, &actor, in.ActorPassword, in.TOTPCode)', credential)
        self.assertIn('w.Header().Set("Retry-After", "900")', credential)
        self.assertIn('const code = await freshCode(auth);', self.source)
        self.assertIn('data: { ...data, actorPassword: password, totpCode: code }', self.source)
        self.assertIn('test.setTimeout(630_000);', self.source)
        self.assertNotIn('auth_attempts', self.source)
        self.assertNotRegex(self.source, r'(?i)\b(?:DELETE|TRUNCATE|UPDATE)\s+(?:adtr\.)?auth_attempts')
        self.assertNotIn('clock.install', self.source)
        self.assertNotIn('clock.set', self.source)
        # Exactly the existing bootstrap-created reader and producer, with their
        # existing role assignment; no extra account to evade a per-user bucket.
        self.assertEqual(self.source.count('"/api/access/users/create"'), 2)
        self.assertIn('expect(producer.value.ID).not.toBe(actorId);', self.source)
        self.assertIn('expect(readerId).not.toBe(actorId);', self.source)

    def test_budget_regression_detects_extra_sync_or_missing_revoke(self):
        _, producer, _ = phases(self.source)
        routes, count = budget(producer + '\nawait post("/api/directory/v4/sync", sync, 403);', 'producerPage')
        self.assertEqual(count, 11)
        self.assertEqual(routes['/api/directory/v4/sync'], 3)
        without_revoke = producer.replace('post("/api/directory-credential-use/v4/revoke",', 'removedRevoke(')
        routes, count = budget(without_revoke, 'producerPage')
        self.assertEqual(count, 9)
        self.assertEqual(routes['/api/directory-credential-use/v4/revoke'], 0)


if __name__ == '__main__':
    unittest.main()
