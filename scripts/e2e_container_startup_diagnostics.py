"""Failure-only Docker startup stderr, without commands or environment dumps."""
import json
import re
import sys


MAX_STDERR_INPUT = 16384
MAX_STDERR_OUTPUT = 4096
TRUNCATED = "...[truncated]"
URL = re.compile(r"(?<![a-z0-9+.-])[a-z][a-z0-9+.-]*://[^\s<>\"']*", re.IGNORECASE)
# Withhold stderr containing credential fields or environment entries: values
# may continue on another line, so value-only or line-only redaction is unsafe.
SENSITIVE_LINE = re.compile(
    r"\b[A-Z][A-Z0-9_]*\s*=|"
    r"(?<![\w.-])(?:[\w.-]*(?:password|passwd|secret|token|credential|authorization|"
    r"private[_-]?key|access[_-]?key|api[_-]?key)[\w.-]*|pwd|auth|auths|credsStore|"
    r"credHelpers|Config\.Env|Env)\b[\"']?\s*[:=]",
    re.IGNORECASE,
)


def sanitized_stderr(stderr, password):
    if stderr is None or stderr == b"" or stderr == "":
        return "[no stderr captured]"
    truncated = len(stderr) > MAX_STDERR_INPUT
    prefix = stderr[:MAX_STDERR_INPUT]
    if truncated:
        # Drop the incomplete final line before redaction: a URL or secret may
        # straddle the input limit, so never publish a partial raw line.
        newline = b"\n" if isinstance(prefix, bytes) else "\n"
        prefix = prefix.rpartition(newline)[0]
    if isinstance(prefix, bytes):
        prefix = prefix.decode("utf-8", errors="replace")
    if password:
        prefix = prefix.replace(password, "[redacted]")
    lines = []
    for line in prefix.splitlines():
        line = URL.sub("[redacted URL]", line)
        if SENSITIVE_LINE.search(line):
            return "[stderr withheld: sensitive diagnostic field]"
        lines.append(line)
    # Quote controls/newlines/ANSI and explicitly neutralize both Actions
    # command syntaxes. Legacy ##[ commands are parsed even after a prefix.
    rendered = json.dumps("\n".join(lines), ensure_ascii=True)
    rendered = rendered.replace("##[", r"\u0023\u0023[").replace("::", r"\u003a\u003a")
    if truncated or len(rendered) > MAX_STDERR_OUTPUT:
        return rendered[:MAX_STDERR_OUTPUT - len(TRUNCATED)] + TRUNCATED
    return rendered


def report_container_startup_failure(error, password):
    # Only captured Docker stderr and its numeric exit code are inspected. Do
    # not print the exception/command, stdout, env, Docker config, or app logs.
    diagnostic = sanitized_stderr(error.stderr, password)
    try:
        print(f"test database Docker startup failed (exit code {error.returncode}); "
              f"stderr (sanitized, bounded): {diagnostic}", file=sys.stderr)
    except (OSError, ValueError):
        # Broken/closed diagnostic streams must not replace the startup error.
        pass
