"""Run every integration-tagged Go test with bounded auth-package shards."""
import argparse
import os
import re
import subprocess

AUTH_PACKAGE = "github.com/yunpiao/adtr/internal/auth"
AUTH_SHARDS = 4
TEST_FLAGS = ["-race", "-count=1", "-tags=integration", "-timeout=10m"]


def parse_args(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--suite", choices=("all", "non-auth", "auth"), default="all")
    parser.add_argument("--shard", type=int, choices=range(1, AUTH_SHARDS + 1))
    args = parser.parse_args(argv)
    if (args.suite == "auth") != (args.shard is not None):
        parser.error("--suite auth requires --shard; other suites must not select a shard")
    return args


def partition_auth_tests(output):
    names = []
    for line in output.splitlines():
        if re.fullmatch(r"(?:Test|Example|Fuzz)\w*", line):
            names.append(line)
        elif (re.fullmatch(r"Benchmark\w*", line)
              or re.fullmatch(r"ok[ \t]+\S+[ \t]+\S+", line)):
            # Benchmarks are not executed by go test without -bench.
            continue
        else:
            raise ValueError(f"unexpected Go test discovery output: {line!r}")
    if not names or len(names) != len(set(names)):
        raise ValueError("auth test discovery must be nonempty and duplicate-free")
    names.sort()
    shards = [names[index::AUTH_SHARDS] for index in range(AUTH_SHARDS)]
    if any(not shard for shard in shards):
        raise ValueError("auth test discovery produced an empty shard")
    return shards


def test_commands(args, env):
    go = env.get("GO", "go")
    commands = []
    if args.suite in ("all", "non-auth"):
        output = subprocess.check_output(
            [go, "list", "-tags=integration", "./..."], env=env, text=True)
        packages = output.splitlines()
        if (packages.count(AUTH_PACKAGE) != 1 or len(packages) != len(set(packages))
                or any(not package or any(c.isspace() for c in package) for package in packages)):
            raise ValueError("integration package discovery is incomplete or ambiguous")
        packages = [package for package in packages if package != AUTH_PACKAGE]
        if not packages:
            raise ValueError("non-auth package discovery must be nonempty")
        commands.append([go, "test", *TEST_FLAGS, "-v", *packages])
        print(f"Non-auth integration: {len(packages)} packages", flush=True)
    if args.suite in ("all", "auth"):
        output = subprocess.check_output(
            [go, "test", *TEST_FLAGS, "-list", ".", AUTH_PACKAGE], env=env, text=True)
        shards = partition_auth_tests(output)
        print(f"Auth integration: {sum(map(len, shards))} discovered tests; "
              f"shard sizes {[len(shard) for shard in shards]}", flush=True)
        indexes = range(AUTH_SHARDS) if args.suite == "all" else [args.shard - 1]
        for index in indexes:
            print(f"Selected auth shard {index + 1}/{AUTH_SHARDS}: {len(shards[index])} tests", flush=True)
            pattern = "^(" + "|".join(re.escape(name) for name in shards[index]) + ")$"
            commands.append([go, "test", *TEST_FLAGS, "-v", "-run", pattern, AUTH_PACKAGE])
    return commands


def run_tests(args, env):
    if not env.get("ADTR_TEST_DATABASE_URL"):
        raise ValueError("ADTR_TEST_DATABASE_URL required; missing PostgreSQL is not a pass")
    commands = test_commands(args, env)
    failed = False
    for index, command in enumerate(commands, start=1):
        print(f"Running integration group {index}/{len(commands)}", flush=True)
        if subprocess.run(command, env=env, check=False).returncode != 0:
            failed = True
    return int(failed)


if __name__ == "__main__":
    raise SystemExit(run_tests(parse_args(), dict(os.environ)))
