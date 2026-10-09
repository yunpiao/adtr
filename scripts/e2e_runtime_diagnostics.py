"""Fail browser acceptance on recovered HTTP panics without disclosing logs."""
import re


# The owned API/worker use Go's unchanged default logger and loopback listener.
# Match its complete line prefix, not an incidental phrase in a log message.
HTTP_PANIC_HEADER = re.compile(
    r"^\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2} http: panic serving "
    r"(?:127\.0\.0\.1|\[::1\]):[1-9]\d{0,4}: "
)


def http_handler_panicked(logs):
    # net/http recovers panics per connection, so process exit alone cannot
    # establish health. Scan after owned processes stop; do not emit panic
    # values, stack arguments, request headers, or any other raw diagnostics.
    logs.flush()
    logs.seek(0)
    return any(HTTP_PANIC_HEADER.match(line) for line in logs)
