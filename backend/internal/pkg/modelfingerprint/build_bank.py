"""Build bank.json from xqy2006/ModelTrace data/gpt_reference.jsonl (MIT).

Pinned enrollment revision 09a0ae5009d943aa6cb5dd3c914b83b7e70826ac.
Usage: python3 build_bank.py gpt_reference.jsonl > bank.json
"""

import collections
import json
import math
import re
import sys

import numpy as np


def features(numbers):
    transitions = list(zip(numbers, numbers[1:]))
    distributions = [
        ([n - 1 for n in numbers], 355),
        ([((a - 1) * 8 // 355) * 8 + (b - 1) * 8 // 355 for a, b in transitions], 64),
        ([(b - a + 354) * 48 // 709 for a, b in transitions], 48),
        ([n % 10 for n in numbers], 10),
    ]
    result = []
    for samples, size in distributions:
        counts = collections.Counter(samples)
        result.extend(
            math.sqrt((counts[i] + 0.5) / (len(samples) + 0.5 * size))
            for i in range(size)
        )
    return result


def read_samples(path):
    rows = []
    seen = set()
    with open(path, encoding="utf-8") as source:
        for line in source:
            row = json.loads(line)
            if not row.get("strict_valid") or row.get("pattern_valid") is False or row.get("error"):
                continue
            numbers = row.get("numbers") or [int(v) for v in re.findall(r"\d+", row["text"])]
            if not 180 <= len(numbers) <= 420 or min(numbers) < 1 or max(numbers) > 355:
                continue
            sequence = tuple(numbers)
            if sequence in seen:
                continue
            seen.add(sequence)
            rows.append((row["model_id"], row.get("reasoning_effort") or "",
                         row["condition_id"], features(numbers)))
    return rows


def fit(samples, labels, class_count):
    # Balance model classes so enrollment frequency doesn't determine the prior.
    frequency = np.bincount(labels, minlength=class_count)
    weights = 1 / np.maximum(1, frequency[labels])
    weights *= len(weights) / sum(weights)
    mean = np.average(samples, axis=0, weights=weights)
    scale = np.sqrt(np.average((samples - mean) ** 2, axis=0, weights=weights)).clip(0.0001)
    normalized = (samples - mean) / scale
    targets = np.eye(class_count)[labels]
    penalty = np.eye(samples.shape[1]) * len(weights) * 0.65
    slopes = np.linalg.solve(
        normalized.T @ (normalized * weights[:, None]) + penalty,
        normalized.T @ (targets * weights[:, None]),
    ) / scale[:, None]
    biases = np.average(targets, axis=0, weights=weights) - mean @ slopes
    return slopes, biases


def rejection_gates(correct, wrong):
    # Maximize held-out coverage subject to rejecting every observed false
    # positive for this predicted class. Unknown is a valid outcome.
    options = []
    for threshold in sorted({r[3] for r in correct}):
        margin = max([0.06] + [r[4] + 0.001 for r in wrong if r[3] >= threshold])
        accepted = [r for r in correct if r[3] >= threshold and r[4] >= margin]
        options.append((len(accepted), threshold, margin))
    _, score, margin = max(options, default=(0, 1.0, 1.0))
    efforts = sorted({r[1] for r in correct if r[3] >= score and r[4] >= margin})
    return score, margin, efforts


def build(rows):
    models = sorted({r[0] for r in rows})
    samples = np.array([r[3] for r in rows])
    labels = np.array([models.index(r[0]) for r in rows])
    environments = np.array([r[2] for r in rows])

    outcomes = []
    for environment in sorted(set(environments)):
        train = environments != environment
        slopes, biases = fit(samples[train], labels[train], len(models))
        for i in np.where(~train)[0]:
            scores = samples[i] @ slopes + biases
            second, first = np.argsort(scores)[-2:]
            outcomes.append((rows[i][0], rows[i][1], models[first],
                             scores[first], scores[first] - scores[second]))

    slopes, biases = fit(samples, labels, len(models))
    bank = []
    for i, name in enumerate(models):
        correct = [r for r in outcomes if r[0] == r[2] == name]
        wrong = [r for r in outcomes if r[2] == name and r[0] != name]
        score, margin, efforts = rejection_gates(correct, wrong)
        bank.append(dict(id=name, efforts=efforts, min_score=float(score),
                         min_margin=float(margin), bias=float(biases[i]),
                         weights=slopes[:, i].round(8).tolist()))
    return bank


if __name__ == "__main__":
    json.dump(build(read_samples(sys.argv[1])), sys.stdout, separators=(",", ":"))
