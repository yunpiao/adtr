"""Ephemeral inputs for owned directory browser acceptance runs.

Never read stored credentials, print values, persist them, or put them in argv.
The harness passes this private environment directly to its child processes.
"""
import secrets


PASSWORD_NAMES = (
    "ADTR_E2E_DIRECTORY_PASSWORD",
    "ADTR_E2E_DIRECTORY_PRODUCER_INITIAL_PASSWORD",
    "ADTR_E2E_DIRECTORY_PRODUCER_PASSWORD",
    "ADTR_E2E_DIRECTORY_DRAFT_PASSWORD",
)
DRAFT_CODE_NAME = "ADTR_E2E_DIRECTORY_DRAFT_CODE"
MEMBERSHIP_PASSWORD_NAMES = (
    "ADTR_E2E_MEMBERSHIP_PASSWORD",
    "ADTR_E2E_MEMBERSHIP_READER_INITIAL_PASSWORD",
    "ADTR_E2E_MEMBERSHIP_READER_PASSWORD",
    "ADTR_E2E_MEMBERSHIP_PRODUCER_INITIAL_PASSWORD",
    "ADTR_E2E_MEMBERSHIP_PRODUCER_PASSWORD",
)
V4_PASSWORD_NAMES = (
    "ADTR_E2E_V4_CONTROLS_PASSWORD",
    "ADTR_E2E_V4_CONTROLS_PRODUCER_INITIAL_PASSWORD",
    "ADTR_E2E_V4_CONTROLS_PRODUCER_PASSWORD",
    "ADTR_E2E_V4_CONTROLS_DRAFT_PASSWORD",
)
V4_READER_PASSWORD_NAMES = V4_PASSWORD_NAMES + (
    "ADTR_E2E_V4_CONTROLS_READER_INITIAL_PASSWORD",
    "ADTR_E2E_V4_CONTROLS_READER_PASSWORD",
)
V4_DRAFT_CODE_NAME = "ADTR_E2E_V4_CONTROLS_DRAFT_CODE"
SUITE_PASSWORD_NAMES = {
    "directory-v3": PASSWORD_NAMES,
    "group-members-v4": MEMBERSHIP_PASSWORD_NAMES,
    "directory-v4-controls": V4_PASSWORD_NAMES,
    "directory-v4-cancel": V4_PASSWORD_NAMES,
    "directory-v4-readers": V4_READER_PASSWORD_NAMES,
}


def configure_directory_credentials(env, suite):
    names = SUITE_PASSWORD_NAMES.get(suite)
    if names is None:
        return
    # Always replace inherited values: this suite owns disposable test users.
    # 24 random bytes encode to 32 ASCII characters, within the 12–64 policy.
    for name in names:
        env[name] = secrets.token_urlsafe(24)
    # A never-submitted draft input, not a valid authenticator or bypass code.
    if suite == "directory-v3":
        env[DRAFT_CODE_NAME] = f"{secrets.randbelow(1_000_000):06d}"
    if suite in {"directory-v4-controls", "directory-v4-cancel", "directory-v4-readers"}:
        env[V4_DRAFT_CODE_NAME] = f"{secrets.randbelow(1_000_000):06d}"
