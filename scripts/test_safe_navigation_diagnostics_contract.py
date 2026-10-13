"""Pure Python safe-navigation schema and static wiring contracts; no browser."""
import copy
import json
from pathlib import Path
import re
import unittest

import e2e_runtime_diagnostics as d


ROOT = Path(__file__).resolve().parents[1]


def event():
    snapshot = dict.fromkeys(d.NAVIGATION_BOOLEANS, False)
    snapshot.update(width=390, height=844, compact=True, panelHidden=True,
                    panelInert=True, toggleVisible=True, toggleEnabled=True,
                    groupOpen=True)
    return dict(origin='navigation', stage='start', operation=1, at_ms=0,
                elapsed_ms=0, snapshot=snapshot)


class SafeNavigationDiagnosticsContract(unittest.TestCase):
    def test_every_stage_and_unavailable_state_is_closed_and_valid(self):
        for stage in d.NAVIGATION_STAGES:
            value = event()
            value['stage'] = stage
            d.validate_event(value)
        for closed in [False, True]:
            value = event()
            value['snapshot'] = dict.fromkeys(d.NAVIGATION_BOOLEANS, False)
            value['snapshot'].update(width=0, height=0, unavailable=True, pageClosed=closed)
            d.validate_event(value)

    def test_missing_extra_fields_and_sensitive_canary_rejected(self):
        for nested in [False, True]:
            target = event()['snapshot'] if nested else event()
            for key in target:
                value = event()
                (value['snapshot'] if nested else value).pop(key)
                with self.subTest(nested=nested, key=key), self.assertRaises(ValueError):
                    d.validate_event(value)
            for key in ['html', 'url', 'hash', 'query', 'targetName', 'actorId', 'token', 'error', 'response']:
                value = event()
                (value['snapshot'] if nested else value)[key] = 'CANARY-secret-cookie-account'
                with self.subTest(nested=nested, key=key), self.assertRaises(ValueError):
                    d.validate_event(value)

    def test_integer_boolean_enum_and_dimension_boundaries(self):
        for key, maximum in [('operation', 1000000), ('at_ms', d.MAX_MS), ('elapsed_ms', d.MAX_MS)]:
            for bad in [None, True, False, '1', 1.2, -1, maximum + 1, float('nan'), float('inf')]:
                value = event(); value[key] = bad
                with self.subTest(key=key, bad=bad), self.assertRaises(ValueError): d.validate_event(value)
        value = event(); value['operation'] = 0
        with self.assertRaises(ValueError): d.validate_event(value)
        for key in ['width', 'height']:
            for bad in [None, True, False, '390', 0, -1, 16385, 390.5, float('nan'), float('inf')]:
                value = event(); value['snapshot'][key] = bad
                with self.subTest(key=key, bad=bad), self.assertRaises(ValueError): d.validate_event(value)
        for key in d.NAVIGATION_BOOLEANS:
            for bad in [0, 1, None, 'false', []]:
                value = event(); value['snapshot'][key] = bad
                with self.subTest(key=key, bad=bad), self.assertRaises(ValueError): d.validate_event(value)
        for bad in ['CANARY', None, 1, []]:
            value = event(); value['stage'] = bad
            with self.assertRaises(ValueError): d.validate_event(value)

    def test_unavailable_relations_fail_closed(self):
        value = event(); value['snapshot']['pageClosed'] = True
        with self.assertRaises(ValueError): d.validate_event(value)
        for key in ['width', 'height', 'compact', 'targetVisible']:
            value = event()
            value['snapshot'] = dict.fromkeys(d.NAVIGATION_BOOLEANS, False)
            value['snapshot'].update(width=0, height=0, unavailable=True)
            value['snapshot'][key] = True if key in d.NAVIGATION_BOOLEANS else 1
            with self.subTest(key=key), self.assertRaises(ValueError): d.validate_event(value)

    def test_legacy_report_and_budgets_unchanged(self):
        report = d.RuntimeDiagnostics().report
        report['browser_capture'] = 'present'
        legacy = dict(phase='read-json', method='GET', route='/api/auth/me',
                      elapsed_ms=0, at_ms=0, outcome='start', status=None)
        report['browser'] = [legacy, event()]
        decoded = json.loads(d.validate_report(report))
        self.assertEqual(decoded['browser'], [legacy, event()])
        self.assertEqual(d.MAX_EVENTS, 256)
        self.assertEqual(d.MAX_BYTES, 65536)
        self.assertEqual(d.BROWSER_PHASES, {'read-json', 'totp-wait', 'grant-v2-complete'})
        report['browser'] = [event()] * 257
        with self.assertRaises(ValueError): d.validate_report(report)
        legacy['snapshot'] = event()['snapshot']
        with self.assertRaises(ValueError): d.validate_event(legacy)
        value = event(); value['origin'] = 'page-request'
        with self.assertRaises(ValueError): d.validate_event(value)

    def test_typescript_writer_fields_and_breakpoint_are_pinned(self):
        source = (ROOT / 'web/e2e/navigation.ts').read_text()
        booleans = re.search(r'const snapshotBooleans = \[(.*?)\] as const;', source, re.S).group(1)
        self.assertEqual(set(re.findall(r'"([^"\n]+)"', booleans)), d.NAVIGATION_BOOLEANS)
        stages = re.search(r'type Stage = (.*?);', source, re.S).group(1)
        self.assertEqual(set(re.findall(r'"([^"\n]+)"', stages)), d.NAVIGATION_STAGES)
        self.assertIn('if (!validSnapshot(snapshot)) return;', source)
        self.assertIn('origin: "navigation"', source)
        self.assertIn('if (snapshot.unavailable)', source)
        for name in ['navigation.ts', '../src/SocNavigation.tsx']:
            self.assertIn('"(max-width: 760px)"', (ROOT / 'web/e2e' / name).read_text())
        appender = (ROOT / 'web/e2e/safe-runtime-diagnostics.ts').read_text()
        for fragment in ['records.length > 256', 'bytes > 61_440', 'mode: 0o600']:
            self.assertIn(fragment, appender)

    def test_real_controls_default_budget_single_target_action_and_failure_propagation(self):
        source = (ROOT / 'web/e2e/navigation.ts').read_text()
        executable = re.sub(r'//[^\n]*', '', source)
        for forbidden in ['force:', 'waitForTimeout(', 'setTimeout(', 'timeout:', 'location.hash', 'history.', 'innerHTML', '.fill(', 'request.post(', 'details.open =']:
            self.assertNotIn(forbidden, executable)
        self.assertIn('awaitexpect.poll(', re.sub(r'\s+', '', executable))
        self.assertIn('navigationLayout(snapshot) === "compact-closed"', executable)
        self.assertIn('await expect(target).toBeVisible()', executable)
        self.assertIn('await expect(target).toBeEnabled()', executable)
        target = executable.split('export async function navigateTo', 1)[1]
        self.assertEqual(target.count('.click()'), 1)
        self.assertEqual(target.count('throw error;'), 1)
        self.assertNotIn('while', target)
        self.assertNotIn('for (', target)
        self.assertIn('await navigationSnapshot(page, name)', target)
        self.assertIn('await summary.click()', executable)


if __name__ == '__main__':
    unittest.main()
