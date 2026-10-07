#!/usr/bin/env python3
"""Evaluate randomized session usefulness with a viewer-cluster bootstrap.

This is the promotion gate, not a trainer. Input must come from a consented,
randomly sampled session study; content text and private records are rejected.
"""
import argparse
from collections import defaultdict
from datetime import datetime, timedelta, timezone
import hashlib
import json
import math
from pathlib import Path
import random

FIELDS = {'userRef', 'sessionId', 'experimentId', 'arm', 'assignmentProbability',
          'occurredAt', 'satisfaction', 'negativeFeedback', 'retention7', 'retention28',
          'language', 'locality', 'creatorBucket', 'userCohort'}
METRICS = ('satisfaction', 'negativeFeedback', 'retention7', 'retention28')


def load(path, as_of=None):
    as_of = as_of or datetime.now(timezone.utc).replace(hour=0, minute=0, second=0, microsecond=0)
    if as_of.tzinfo is None or as_of.utcoffset() is None:
        raise ValueError('Evaluation cutoff requires a timezone')
    rows, sessions, users = [], set(), {}
    for line in Path(path).read_text().splitlines():
        row = json.loads(line)
        if set(row) != FIELDS:
            raise ValueError('Dataset has missing or unapproved fields')
        if row['arm'] not in ('control', 'treatment'):
            raise ValueError('Unknown experiment arm')
        for key in ('assignmentProbability', 'satisfaction'):
            value = row[key]
            if isinstance(value, bool) or not isinstance(value, (int, float)) or not math.isfinite(value):
                raise ValueError('Non-finite or invalid value')
        if not 0 < row['assignmentProbability'] < 1 or not 0 <= row['satisfaction'] <= 1:
            raise ValueError('Invalid randomization or satisfaction')
        for key in ('negativeFeedback', 'retention7', 'retention28'):
            if not isinstance(row[key], bool):
                raise ValueError('Guardrails require mature boolean outcomes')
        for key in FIELDS - set(METRICS) - {'assignmentProbability'}:
            if not isinstance(row[key], str) or not row[key] or len(row[key]) > 160:
                raise ValueError('Invalid dimension or identity')
        occurred = datetime.fromisoformat(row['occurredAt'].replace('Z', '+00:00'))
        if occurred.tzinfo is None or occurred.utcoffset() is None or occurred + timedelta(days=28) > as_of:
            raise ValueError('Session requires a timezone and mature 28-day retention')
        if row['sessionId'] in sessions:
            raise ValueError('Duplicate session')
        sessions.add(row['sessionId'])
        if row['userRef'] in users and users[row['userRef']] != row['arm']:
            raise ValueError('Sticky assignment changed for a viewer')
        users[row['userRef']] = row['arm']
        rows.append(row)
    if len({r['experimentId'] for r in rows}) != 1:
        raise ValueError('Evaluate exactly one experiment')
    return rows


def compare(rows, seed=20261007, replicates=2000):
    arms = {arm: defaultdict(list) for arm in ('control', 'treatment')}
    for row in rows:
        arms[row['arm']][row['userRef']].append(row)
    counts = {arm: len(groups) for arm, groups in arms.items()}
    result = {'viewers': counts, 'metrics': {}, 'eligibleForPromotion': False}
    if min(counts.values()) < 50:
        result['reason'] = 'Fewer than 50 viewers in an arm; evidence is insufficient'
        return result
    rng = random.Random(seed)
    estimates = {metric: [] for metric in METRICS}
    def mean(groups, metric):
        # Balance unequal allocation; preserve sessions within viewer clusters.
        numerator = denominator = 0
        for group in groups:
            for row in group:
                weight = 1 / row['assignmentProbability']
                numerator += weight * row[metric]
                denominator += weight
        return numerator / denominator
    groups = {arm: list(arms[arm].values()) for arm in arms}
    for _ in range(replicates):
        sampled = {arm: rng.choices(value, k=len(value)) for arm, value in groups.items()}
        for metric in METRICS:
            estimates[metric].append(mean(sampled['treatment'], metric) - mean(sampled['control'], metric))
    for metric, values in estimates.items():
        values.sort()
        result['metrics'][metric] = {
            'difference': mean(groups['treatment'], metric) - mean(groups['control'], metric),
            'ci95': [values[int(replicates * .025)], values[min(replicates - 1, int(replicates * .975))]],
        }
    m = result['metrics']
    result['eligibleForPromotion'] = (m['satisfaction']['ci95'][0] > 0
        and m['negativeFeedback']['ci95'][1] <= .02
        and m['retention7']['ci95'][0] >= -.02
        and m['retention28']['ci95'][0] >= -.02)
    result['reason'] = 'Statistical gate only; cohort, safety, capacity and operational review remain required'
    return result


def evaluate(path, seed=20261007, replicates=2000, as_of=None):
    as_of = as_of or datetime.now(timezone.utc).replace(hour=0, minute=0, second=0, microsecond=0)
    rows = load(path, as_of)
    report = {'schemaVersion': 1, 'datasetSha256': hashlib.sha256(Path(path).read_bytes()).hexdigest(),
              'evaluationAsOf': as_of.isoformat(), 'seed': seed, 'bootstrapReplicates': replicates, 'overall': compare(rows, seed, replicates),
              'cohorts': {}}
    for dimension in ('userCohort', 'language', 'locality', 'creatorBucket'):
        report['cohorts'][dimension] = {value: compare([r for r in rows if r[dimension] == value], seed, replicates)
                                      for value in sorted({r[dimension] for r in rows})}
    # A strong overall result never conceals an unmeasured or regressing cohort.
    report['eligibleForPromotion'] = report['overall']['eligibleForPromotion'] and all(
        cohort['eligibleForPromotion'] for dimension in report['cohorts'].values() for cohort in dimension.values())
    return report


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('dataset', type=Path)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--seed', type=int, default=20261007)
    parser.add_argument('--as-of', type=datetime.fromisoformat, help='Timezone-aware evaluation cutoff for reproducible retention maturity')
    args = parser.parse_args()
    args.output.write_text(json.dumps(evaluate(args.dataset, args.seed, as_of=args.as_of), indent=2) + '\n')
