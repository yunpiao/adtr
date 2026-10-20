"""Offline same-sample diagnostics; no database, signal, or external state."""
import copy
import json
import unittest
from unittest.mock import patch
import asset_export_v3_running_cancel_guard as g
from test_asset_export_v3_running_cancel_guard_contract import session, lock


class TupleDiagnosticTests(unittest.TestCase):
    def target(self):
        row = session()
        row['locks'] = [lock('tuple', 'ExclusiveLock', relation='adtr.system_worker_activity')]
        return row

    def reject(self, row):
        before = copy.deepcopy(row)
        self.assertTrue(g.activity_direct(row, 99))
        self.assertIsNone(g.validate_session_observation([row], 99))
        with self.assertRaisesRegex(RuntimeError, '^unsafe tuple lock$') as caught:
            g.validate_candidate_strict(row, 99)
        self.assertEqual(row, before)
        error = caught.exception
        self.assertEqual(error.args, ('unsafe tuple lock',))
        self.assertEqual(set(error.tuple_diagnostic), set(g.TUPLE_FAILURE_VALUES))
        return error, g.failure_event('candidate_strict', error)

    def test_three_remaining_predicates_are_independently_distinguished(self):
        for failed in ('tupleWaitCheck', 'tupleRelationCheck', 'tupleModeCheck'):
            with self.subTest(failed=failed):
                row = self.target()
                if failed == 'tupleWaitCheck': row['waitType'] = None
                if failed == 'tupleRelationCheck': row['locks'][0]['relation'] = 'PRIVATE_RELATION_SENTINEL'
                if failed == 'tupleModeCheck': row['locks'][0]['mode'] = 'ShareLock'
                _, event = self.reject(row)
                self.assertEqual(event['reason'], 'unsafe_tuple_lock')
                for key in g.TUPLE_FAILURE_VALUES:
                    rejected = key == failed or (failed == 'tupleWaitCheck' and key == 'tupleBarrierCheck')
                    self.assertEqual(event[key], 'rejected' if rejected else 'matched')
                self.assertNotIn('PRIVATE', json.dumps(event))
                self.assertNotIn('99', json.dumps(event))

    def test_valid_control_unchanged(self):
        row = self.target()
        self.assertEqual(g.validate_candidate_strict(row, 99), g.validate_sessions([row], 99))

    def test_exact_first_rejected_tuple_and_simultaneous_failures(self):
        row = self.target()
        row['waitType'] = 'IO'
        row['locks'] = [lock('tuple', 'ShareLock', relation='PRIVATE_RELATION'),
                        lock('tuple', 'ExclusiveLock', relation='adtr.system_worker_activity')]
        _, event = self.reject(row)
        for key in ('tupleWaitCheck', 'tupleRelationCheck', 'tupleModeCheck', 'tupleBarrierCheck'):
            self.assertEqual(event[key], 'rejected')
        self.assertEqual(event['tupleQueryCheck'], 'matched')

    def test_original_exception_identity_preserved(self):
        row = self.target(); row['waitType'] = None
        original = RuntimeError('unsafe tuple lock')
        def require(ok, message):
            if not ok:
                if message == 'unsafe tuple lock': raise original
                raise RuntimeError(message)
        with patch.object(g, 'require', side_effect=require):
            with self.assertRaises(RuntimeError) as caught:
                g.validate_candidate_strict(row, 99)
        self.assertIs(caught.exception, original)

    def test_diagnostic_failure_cannot_replace_rejection(self):
        row = self.target(); row['waitType'] = None
        with patch.object(g, 'TUPLE_FAILURE_VALUES', None):
            with self.assertRaisesRegex(RuntimeError, '^unsafe tuple lock$'):
                g.validate_candidate_strict(row, 99)

    def test_closed_projection_rejects_malformed_details_and_wrong_scope(self):
        row = self.target(); row['waitType'] = None
        error, event = self.reject(row)
        valid = error.tuple_diagnostic.copy()
        bad = [None, [], {}, valid | {'private': 'SECRET'},
               valid | {'tupleModeCheck': 'SECRET'}, valid | {'tupleModeCheck': True}]
        for key in valid:
            for value in ('SECRET', '', True, 1, None, [], {}):
                bad.append(valid | {key: value})
            bad.append({k: v for k, v in valid.items() if k != key})
        for detail in bad:
            error.tuple_diagnostic = detail
            self.assertEqual(set(g.failure_event('candidate_strict', error)), {'event', 'stage', 'reason'})
        error.tuple_diagnostic = valid
        self.assertEqual(set(g.failure_event('capture_readiness', error)), {'event', 'stage', 'reason'})
        other = RuntimeError('unsafe transaction lock'); other.tuple_diagnostic = valid
        self.assertEqual(set(g.failure_event('candidate_strict', other)), {'event', 'stage', 'reason'})


if __name__ == '__main__': unittest.main()
