# CI event coverage

The same `engineering-ci / verify` job and all checks run for:

- Every opened, synchronized or reopened pull request, including drafts and stacked
  pull requests targeting another feature branch. There is no draft/base filter
- Pushes to main, preserving post-merge validation
- Merge-queue `checks_requested` events, if a queue is configured

Feature/docs branch pushes do not independently launch a second identical workflow.
Open a draft PR to validate an unpublished feature branch. Pull-request checkout
keeps the default merge ref, so tests validate the proposed merge result. No test,
permission limit, timeout, required-check name or failure condition is removed.
The event contract test also guards all real PostgreSQL/lifecycle/browser stages.

Repository branch rules are not modified by this change. Actual required-check
configuration and any merge queue settings remain repository-owner controls.
