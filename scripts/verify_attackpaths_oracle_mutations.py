#!/usr/bin/env python3
"""Offline negative controls for the synthetic path oracle; never edits sources."""

import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile


ROOT = Path(__file__).resolve().parents[1]
SOURCE = ROOT / "internal/attackpaths/graph.go"
MUTATIONS = (
    ("ignore_rule_filter", "!rules[e.RuleID] || ", "false || "),
    (
        "include_exclusive_end",
        "!e.OccurredAt.Before(q.End)",
        "e.OccurredAt.After(q.End)",
    ),
    (
        "admit_one_extra_depth",
        "if depths[current] == q.MaxDepth {",
        "if depths[current] > q.MaxDepth {",
    ),
    (
        "report_budget_as_no_path",
        "if limited {\n\t\treturn empty, ErrLimit\n\t}",
        "if limited {\n\t\treturn result, nil\n\t}",
    ),
    ("reverse_target_tie", "return a.To < b.To", "return a.To > b.To"),
    ("reverse_parallel_edge_tie", "return a.ID < b.ID", "return a.ID > b.ID"),
    (
        "replace_fifo_with_stack",
        "queue = append(queue, e.To)",
        "queue = append(queue[:i+1], append([]string{e.To}, queue[i+1:]...)...)",
    ),
)


def check_mutations():
    original = SOURCE.read_bytes()
    text = original.decode("utf-8")
    go = os.environ.get("GO", "go")
    environment = dict(os.environ, GOTOOLCHAIN="local", GOPROXY="off", GOSUMDB="off")
    baseline = subprocess.run(
        [go, "test", "-count=1", "-run=^TestShortest.*Oracle", "./internal/attackpaths"],
        cwd=ROOT,
        env=environment,
        capture_output=True,
        text=True,
        timeout=120,
    )
    if baseline.returncode:
        raise RuntimeError(f"unmodified oracle tests must pass first\n{baseline.stdout}{baseline.stderr}")
    results = []
    with tempfile.TemporaryDirectory(prefix="adtr-path-oracle-") as directory:
        temporary = Path(directory)
        for name, before, after in MUTATIONS:
            if text.count(before) != 1:
                raise RuntimeError(f"{name}: source marker is no longer unique")
            mutant = temporary / f"{name}.go"
            mutant.write_text(text.replace(before, after), encoding="utf-8")
            overlay = temporary / f"{name}.json"
            overlay.write_text(
                json.dumps({"Replace": {str(SOURCE): str(mutant)}}), encoding="utf-8"
            )
            common = [go, "test", f"-overlay={overlay}", "-count=1"]
            compiled = subprocess.run(
                [*common, "-run=^$", "./internal/attackpaths"],
                cwd=ROOT,
                env=environment,
                capture_output=True,
                text=True,
                timeout=120,
            )
            if compiled.returncode:
                raise RuntimeError(f"{name}: mutation did not compile\n{compiled.stdout}{compiled.stderr}")
            checked = subprocess.run(
                [*common, "-run=^TestShortest.*Oracle", "./internal/attackpaths"],
                cwd=ROOT,
                env=environment,
                capture_output=True,
                text=True,
                timeout=120,
            )
            output = checked.stdout + checked.stderr
            if (
                checked.returncode == 0
                or "--- FAIL: TestShortest" not in output
                or "[build failed]" in output
                or "case=" not in output
                or "got=" not in output
                or "want=" not in output
            ):
                raise RuntimeError(f"{name}: oracle did not detect the compiled mutation\n{output}")
            failures = [line.strip() for line in output.splitlines() if "--- FAIL: TestShortest" in line]
            counterexample = next(line.strip() for line in output.splitlines() if "case=" in line)
            results.append({"mutation": name, "compiled": True, "detected": True, "failures": failures, "counterexample": counterexample})
    if SOURCE.read_bytes() != original:
        raise RuntimeError("source changed while mutation controls were running")
    return {
        "scope": "offline synthetic algorithm negative controls",
        "sourceSHA256": hashlib.sha256(original).hexdigest(),
        "sourceUnchanged": True,
        "unmodifiedTestsPassed": True,
        "mutations": results,
    }


if __name__ == "__main__":
    print(json.dumps(check_mutations(), indent=2))
