"""Offline tests of the closed diagnostic; no process or database is executed."""
import io
import json
from pathlib import Path
import re
import unittest
from unittest.mock import Mock, patch

import owned_rescue_runner as r


class JournalFailureDiagnostic(unittest.TestCase):
    def record(self):
        return dict(event=r.FIRST_JOURNAL_FAILURE_EVENT, stage='report',
                    errorCategory='deadline_exceeded', contextState='deadline_exceeded',
                    recorderContextState='active', connectPhase='none')

    def test_round_trip_restores_position_and_closed_enums(self):
        for stage in r.JOURNAL_FAILURE_STAGES - {'none'}:
            for category in r.JOURNAL_FAILURE_ERRORS - {'none'}:
                frame = self.record() | dict(stage=stage, errorCategory=category, connectPhase="unknown" if stage=="connect" else "none")
                logs = io.StringIO(json.dumps(frame) + '\n')
                logs.seek(3)
                self.assertEqual(r.first_journal_failure_projection(logs), frame)
                self.assertEqual(logs.tell(), 3)
        frame = self.record() | dict(stage='none', errorCategory='none', contextState='unknown', recorderContextState='unknown')
        self.assertEqual(r.first_journal_failure_projection(io.StringIO(json.dumps(frame)+'\n')), frame)

    def test_connect_phase_is_closed_and_only_describes_connect(self):
        for phase in ('admission', 'native', 'unknown'):
            frame = self.record() | dict(stage='connect', connectPhase=phase)
            self.assertEqual(r.first_journal_failure_projection(io.StringIO(json.dumps(frame)+'\n')), frame)
        for stage, phase in [('report', 'native'), ('none', 'admission'), ('connect', 'none'), ('connect', 'private'), ('connect', True)]:
            frame = self.record() | dict(stage=stage, connectPhase=phase)
            result = r.first_journal_failure_projection(io.StringIO(json.dumps(frame)+'\n'))
            self.assertEqual(result['connectPhase'], 'unknown')
            self.assertEqual(result['stage'], 'unknown')
            self.assertNotIn('private', json.dumps(result))
        frame = self.record(); del frame['connectPhase']
        self.assertEqual(r.first_journal_failure_projection(io.StringIO(json.dumps(frame)+'\n'))['stage'], 'unknown')

    def test_malformed_duplicate_unknown_and_secret_fields_never_escape(self):
        frame = self.record()
        line = json.dumps(frame) + '\n'
        cases = ['', line+line, line.rstrip(), 'private\n',
                 json.dumps(frame | {'secret': 'private-value'})+'\n',
                 json.dumps(frame | {'stage': 'private-value'})+'\n',
                 json.dumps(frame | {'contextState': True})+'\n',
                 json.dumps(frame | {'errorCategory': 'none'})+'\n',
                 json.dumps(frame | {'stage': 'none', 'errorCategory': 'none'})+'\n',
                 line.replace('"stage":', '"stage":"private-value","stage":'),
                 ' '*1024+line]
        for text in cases:
            result = r.first_journal_failure_projection(io.StringIO(text))
            self.assertEqual(set(result), set(frame))
            self.assertTrue(all(v == 'unknown' for k,v in result.items() if k != 'event'))
            self.assertNotIn('private', json.dumps(result))
        with patch.object(r, 'MAX_FINAL_LOG_BYTES', 2):
            self.assertEqual(r.first_journal_failure_projection(io.StringIO(line))['stage'], 'unknown')
        broken = Mock(); broken.tell.return_value=7
        broken.read.side_effect=RuntimeError('private-value')
        self.assertEqual(r.first_journal_failure_projection(broken)['stage'], 'unknown')
        self.assertEqual(broken.seek.call_args.args, (7,))

    def test_wait_required_once_without_qualification_changes(self):
        owner = r.OwnedCIWorker(); owner.process=Mock(returncode=None)
        logs = io.StringIO(json.dumps(self.record())+'\n')
        with patch.object(r.sys,'stderr',io.StringIO()) as output:
            owner.report_first_invalidation_after_wait(logs)
            self.assertEqual(output.getvalue(),'')
            owner.process.returncode=0
            owner.report_first_invalidation_after_wait(logs)
            owner.report_first_invalidation_after_wait(logs)
            matches=[json.loads(line) for line in output.getvalue().splitlines()
                     if r.FIRST_JOURNAL_FAILURE_EVENT in line]
            self.assertEqual(matches,[self.record()])
        source=Path('scripts/asset_export_v3_running_cancel_guard.py').read_text()
        self.assertNotIn('first_journal_failure',source)

    def test_tagged_tests_are_selected_without_losing_original_packages(self):
        source=Path('scripts/run_integration.py').read_text()
        self.assertIn('"./internal/systemhealth", "./cmd/adtr", "./internal/operationallogs"',source)
        tests=Path('internal/operationallogs/append_diagnostic_ci_test.go').read_text()
        names=re.findall(r'^func (Test\w+)\(',tests,re.M)
        self.assertGreaterEqual(len(names),5)
        self.assertTrue(all(name.startswith('TestActivityCandidate') for name in names))


if __name__ == '__main__':
    unittest.main()
