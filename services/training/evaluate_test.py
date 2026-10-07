import json
from datetime import datetime, timedelta, timezone
from pathlib import Path
import tempfile
import unittest
from evaluate import compare, evaluate, load


def session(user, arm, satisfaction=.5, **extra):
    return dict(userRef=user, sessionId='session-'+user, experimentId='experiment-v1', arm=arm,
                assignmentProbability=.5, occurredAt=(datetime.now(timezone.utc)-timedelta(days=40)).isoformat(), satisfaction=satisfaction,
                negativeFeedback=False, retention7=True, retention28=True,
                language='en-IN', locality='pilot', creatorBucket='new', userCohort='new', **extra)


class EvaluationTest(unittest.TestCase):
    def test_user_clusters_and_guardrail_gate(self):
        rows = [session(str(i), 'control' if i < 60 else 'treatment', .2 if i < 60 else .8) for i in range(120)]
        report = compare(rows, replicates=100)
        self.assertTrue(report['eligibleForPromotion'])
        for row in rows[60:]:
            row['negativeFeedback'] = True
        self.assertFalse(compare(rows, replicates=100)['eligibleForPromotion'])
        self.assertFalse(compare(rows[:30], replicates=100)['eligibleForPromotion'])

    def test_rejects_private_duplicate_and_invalid_data(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'data.jsonl'
            base = session('u', 'control')
            for rows in ([dict(base, ocr='private')], [base, base],
                         [dict(base, assignmentProbability=1)], [dict(base, satisfaction=float('nan'))],
                         [dict(base, occurredAt=datetime.now(timezone.utc).isoformat())],
                         [dict(base, occurredAt=(datetime.now(timezone.utc)+timedelta(days=40)).isoformat())],
                         [dict(base, occurredAt='2020-01-01T00:00:00')],
                         [base, dict(session('u', 'treatment'), sessionId='s2')]):
                path.write_text('\n'.join(json.dumps(r) for r in rows))
                with self.assertRaises(ValueError):
                    load(path)

    def test_cohorts_gate_even_when_overall_improves(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'data.jsonl'
            rows = [session(str(i), 'control' if i < 60 else 'treatment', .2 if i < 60 else .8) for i in range(120)]
            rows[-1]['language'] = 'hi-IN'
            path.write_text('\n'.join(json.dumps(r) for r in rows))
            report = evaluate(path, replicates=100)
            self.assertTrue(report['overall']['eligibleForPromotion'])
            self.assertFalse(report['eligibleForPromotion'])
            self.assertEqual(report, evaluate(path, replicates=100))


if __name__ == '__main__':
    unittest.main()
