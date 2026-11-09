#!/usr/bin/env python3
"""Compile and execute isolated group-model negative controls, never edit production."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys


def sha(data):
    return hashlib.sha256(data).hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, required=True,
                        help="New evidence directory; existing paths are rejected and never deleted")
    args = parser.parse_args()
    root = Path(__file__).resolve().parents[1]
    output = args.output.resolve()
    if output.exists():
        parser.error("output already exists; choose a new evidence directory")
    source = (root / "internal/groupgraph/graph.go").read_text()
    test = (root / "internal/groupgraph/model_test.go").read_bytes()
    # Each anchored change must compile; an assertion failure, not a build error,
    # demonstrates detection. All binaries/copies/logs remain in the output.
    changes = [
        ("distance", "Relationship{target, cur.depth + 1, cur.depth == 0}", "Relationship{target, cur.depth + 2, cur.depth == 0}", 1),
        ("direct-flag", "Relationship{target, cur.depth + 1, cur.depth == 0}", "Relationship{target, cur.depth + 1, false}", 1),
        ("root-inclusion", "visited := map[string]bool{q.ObjectID: true}", "visited := map[string]bool{}", 1),
        ("reverse-direction", "adjacency = g.reverse", "adjacency = g.forward", 1),
        ("unknown-reference", "r.UnknownReferences = append(r.UnknownReferences, Edge{cur.id, id})", 'r.UnknownReferences = append(r.UnknownReferences, Edge{cur.id, "wrong"})', 1),
        ("unknown-forward-list", "r.UnknownMembershipGroups = append(r.UnknownMembershipGroups, cur.id)", 'r.UnknownMembershipGroups = append(r.UnknownMembershipGroups, "wrong")', 1),
        ("unknown-reverse-list", "if n.Kind == Group && !n.MembersKnown {", "if false && n.Kind == Group && !n.MembersKnown {", 1),
        ("completeness", "r.CompleteWithinSnapshot = len(r.UnknownMembershipGroups) == 0 && len(r.UnknownReferences) == 0", "r.CompleteWithinSnapshot = true", 1),
        ("depth-off-by-one", "if cur.depth+1 > q.MaxDepth {", "if cur.depth > q.MaxDepth {", 1),
        ("results-off-by-one", "if len(r.Relationships) >= q.MaxResults {", "if len(r.Relationships) > q.MaxResults {", 1),
        ("truncated-success", "return empty, ErrLimit", "return r, nil", 2),
        ("self-cycle", "if len(component) > 1 || self {", "if len(component) > 1 || (self && false) {", 1),
        ("cycle-omission", "if len(component) > 1 || self {", "if false && (len(component) > 1 || self) {", 1),
        ("result-order", "return r.Relationships[i].Object.ID < r.Relationships[j].Object.ID", "return r.Relationships[i].Object.ID > r.Relationships[j].Object.ID", 1),
        ("diagnostic-cycle-alias", "d.Cycles[i] = append([]string{}, c...)", "d.Cycles[i] = c", 1),
        ("scope-bypass", "if q.Scope != g.scope {", "if false && q.Scope != g.scope {", 1),
    ]
    variants = [("baseline", source)]
    for name, old, new, count in changes:
        if source.count(old) != count:
            raise RuntimeError(f"mutation anchor changed: {name}")
        variants.append((name, source.replace(old, new)))
    env = dict(os.environ, GOTOOLCHAIN="local", GOPROXY="off", GOSUMDB="off", GOWORK="off")
    version = subprocess.check_output(["go", "version"], env=env, text=True).strip()
    if "go1.27.1 " not in version:
        raise RuntimeError("requires the repository's Go 1.27.1 toolchain")
    output.mkdir(parents=True)
    manifest = {"source_sha256": sha(source.encode()), "test_sha256": sha(test),
                "go": version, "network_dependency_resolution": "off", "variants": []}
    try:
        for name, code in variants:
            directory = output / name
            package = directory / "internal/groupgraph"
            package.mkdir(parents=True)
            (directory / "go.mod").write_text("module github.com/yunpiao/adtr\n\ngo 1.27.1\n")
            (package / "graph.go").write_text(code)
            (package / "model_test.go").write_bytes(test)
            binary = directory / "groupgraph.test"
            entry = {"name": name, "source_sha256": sha(code.encode())}
            manifest["variants"].append(entry)
            build_command = ["go", "test", "-c", "-o", str(binary), "./internal/groupgraph"]
            with (directory / "build.log").open("w") as log:
                build = subprocess.run(build_command, cwd=directory, env=env, stdout=log, stderr=subprocess.STDOUT, timeout=120)
            entry.update(build_command=build_command, build_exit=build.returncode)
            if build.returncode != 0:
                raise RuntimeError(f"{name}: build failure is not mutation detection")
            run_command = [str(binary), "-test.run=^TestGroupModel", "-test.v", "-test.failfast", "-test.timeout=60s"]
            with (directory / "test.log").open("w") as log:
                run = subprocess.run(run_command, cwd=directory, env=env, stdout=log, stderr=subprocess.STDOUT, timeout=70)
            result = (directory / "test.log").read_text()
            entry.update(run_command=run_command, test_exit=run.returncode, binary_sha256=sha(binary.read_bytes()), test_log_sha256=sha(result.encode()))
            if name == "baseline":
                if run.returncode != 0 or not result.endswith("PASS\n"):
                    raise RuntimeError("baseline did not pass")
                entry["verdict"] = "pass"
            else:
                if run.returncode != 1 or "--- FAIL: TestGroupModel" not in result or not result.endswith("FAIL\n") or "panic:" in result or "fatal error:" in result:
                    raise RuntimeError(f"{name}: missing normal assertion-failure detection")
                entry["verdict"] = "detected"
            print(f"{name}: compiled; {entry['verdict']}", flush=True)
        manifest["status"] = "pass"
    except BaseException as error:
        manifest["status"] = "failed"
        manifest["failure_type"] = type(error).__name__
        raise
    finally:
        (output / "manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")
    print(f"baseline passed; {len(changes)} independently compiled mutations detected")


if __name__ == "__main__":
    main()
