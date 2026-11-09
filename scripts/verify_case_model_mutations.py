#!/usr/bin/env python3
"""Compile offline negative controls without modifying the source checkout.

Each isolated module contains only the existing stdlib-only case core and the
new external-package model tests. Compiler failures are never a detected mutant.
All generated sources, test binaries and logs are retained in a fresh directory.
"""

import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess


def checked_run(command, cwd, env, log):
    result = subprocess.run(
        command, cwd=cwd, env=env, stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT, text=True, timeout=180, check=False,
    )
    log.write_text(result.stdout, encoding="utf-8")
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, required=True,
                        help="new evidence directory; existing paths are rejected")
    args = parser.parse_args()
    root = Path(__file__).resolve().parents[1]
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=False)
    go = shutil.which("go")
    if go is None:
        raise SystemExit("Go 1.27.1 is required")
    env = {**os.environ, "GOTOOLCHAIN": "local", "GOPROXY": "off", "GOSUMDB": "off", "GOWORK": "off", "GOFLAGS": ""}
    version = checked_run([go, "version"], root, env, output / "go-version.log")
    if version.returncode or not version.stdout.startswith("go version go1.27.1 "):
        raise SystemExit("Use the repository's Go 1.27.1 toolchain")
    source = (root / "internal/securityevents/events.go").read_text(encoding="utf-8")
    test = (root / "internal/securityevents/case_model_test.go").read_text(encoding="utf-8")
    # Each anchor must occur exactly once. Production changes demand a new review
    # rather than silently mutating another statement or accepting a no-op patch.
    mutations = [
        ("stale-revision", "if ch.ExpectedRevision != c.revision {", "if false {"),
        ("noop-accepted", "if next.state == c.state && next.mark == c.mark {", "if false {"),
        ("close-erases-mark", 'next.state = "closed"', 'next.state = "closed"\n\t\tnext.mark = ""'),
        ("closed-mutable", ' || c.state != "open"', ""),
        ("missing-manage", "err != nil || !s.Manage", "err != nil"),
        ("cross-tenant", " || c.tenant != s.Tenant", ""),
        ("cross-domain", " || c.domain != s.Domain", ""),
        ("revision-not-advanced", "next.revision++", "next.revision += 0"),
        ("backward-time", " || ch.At.Before(c.updatedAt)", ""),
        ("timestamp-not-retained", "next.updatedAt = ch.At.UTC()", "next.updatedAt = c.updatedAt"),
        ("audit-prior-mark", "c.state, next.state, c.mark, next.mark", "c.state, next.state, next.mark, next.mark"),
        ("audit-actor", "c.id, ch.Action, ch.Actor, ch.Reason", 'c.id, ch.Action, "other-actor", ch.Reason'),
        ("audit-not-utc", "next.mark, ch.At.UTC(), next.revision", "next.mark, ch.At, next.revision"),
        ("failure-revision-changed", "return c, Audit{}, err", "c.revision++; return c, Audit{}, err"),
        ("failure-audit-emitted", "return c, Audit{}, err", 'return c, Audit{Action: "failed"}, err'),
    ]
    cases = [("baseline", source)]
    for name, before, after in mutations:
        if source.count(before) != 1:
            raise SystemExit(f"Mutation {name} anchor count changed")
        cases.append((name, source.replace(before, after, 1)))
    report = {"source_sha256": hashlib.sha256(source.encode()).hexdigest(),
              "test_sha256": hashlib.sha256(test.encode()).hexdigest(), "cases": []}
    for name, content in cases:
        work = output / name
        package = work / "internal/securityevents"
        package.mkdir(parents=True)
        (work / "go.mod").write_text("module github.com/yunpiao/adtr\n\ngo 1.27.1\n", encoding="utf-8")
        (package / "events.go").write_text(content, encoding="utf-8")
        (package / "case_model_test.go").write_text(test, encoding="utf-8")
        binary = work / "case-model.test"
        build = checked_run([go, "test", "-c", "-o", str(binary), "./internal/securityevents"], work, env, work / "compile.log")
        if build.returncode:
            raise SystemExit(f"{name}: compile failed, not a detected mutation")
        run = checked_run([str(binary), "-test.run=^TestCaseModel", "-test.count=1"], work, env, work / "test.log")
        if name == "baseline":
            if run.returncode != 0:
                raise SystemExit("Unchanged baseline failed")
        elif run.returncode != 1 or "--- FAIL: TestCaseModel" not in run.stdout:
            raise SystemExit(f"{name}: did not produce an expected model assertion failure")
        report["cases"].append({"name": name, "compiled": True, "exit_code": run.returncode,
                                "source_sha256": hashlib.sha256(content.encode()).hexdigest(),
                                "log_sha256": hashlib.sha256(run.stdout.encode()).hexdigest()})
        print(f"{name}: compiled; {'PASS' if name == 'baseline' else 'detected by model'}", flush=True)
    (output / "report.json").write_text(json.dumps(report, indent=2) + "\n", encoding="utf-8")
    print(f"PASS: baseline and {len(mutations)} compiled negative controls; evidence {output}")


if __name__ == "__main__":
    main()
