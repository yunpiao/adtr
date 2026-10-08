# Pinned public evaluation-media download

`Get-EvaluationMedia.ps1` is a standalone Windows PowerShell 5.1 runtime entrypoint.
This remains an unexecuted local implementation draft. Source-string checks do
not establish successful download, Windows parsing, or cleanup behavior.

The helper fetches only the public Microsoft ISO already independently inspected:

- Official entry URL: `https://go.microsoft.com/fwlink/?linkid=2345730&clcid=0x409&culture=en-us&country=us`
- Observed intermediary: `https://aka.ms/WinServ2025iso-enus`
- Exact inspected CDN URL: `https://software-static.download.prss.microsoft.com/dbazure/998969d5-f34g-4e03-ac9d-1f9786c66749/26100.32230.260111-0550.lt_release_svc_refresh_SERVER_EVAL_x64FRE_en-us.iso`
- Exact length: 8,152,356,864 bytes
- SHA-256: `7b052573ba7894c9924e3e87ba732ccd354d18cb75a883efa9b900ea125bfd51`

The hash is a fixed pin of the inspected bytes, not a claim that a separate vendor
checksum was published. If aliases point to new content, stop for new inspection
instead of accepting the replacement. URLs, size, hash and deadline cannot be
overridden by the caller. No login, cookies, authentication, registration, legal
acceptance, mounting, installation or guest boot occurs here.

## Caller contract

Use a newly generated GUID and derive the root from that GUID in the caller.
Do not take a path from untrusted command output. For example, keep the ordinary
nonsecret GUID in the workflow's own `ADTR_DOWNLOAD_ID` environment value:

- `DownloadId`: nonempty, fresh GUID
- `DownloadRoot`: exactly `RUNNER_TEMP\adtr-eval-download-<normalized GUID>`
- `SourceSha`: clean checked-out HEAD, matching `ADTR_SOURCE_SHA`

Invoke the script with those three named parameters in the authorized
GitHub-hosted Windows `yunpiao/adtr` job. No existing root, ownership record,
partial file or ISO is adopted or overwritten. The caller must provide at least
25 GiB free temporary space. This helper does not need the Hyper-V lab state to
exist, does not create it, and does not import or execute the media inspector.

Success emits exactly one object containing `IsoPath`, `IsoSha256`, `Bytes`,
`DownloadId` and `DownloadRoot`. Pass the verified path and hash to the image
builder only after the separately required full-license approval. Never upload,
cache, publish or persist the ISO, partial file, whole download directory, or
whole runner temporary directory as an artifact. The nonsecret ownership record
is for cleanup, not a license receipt or installation result.

The download keeps the inspector's reviewed HTTPS/origin, manual-redirect,
response-header and streaming-size checks and tightens them to the three exact
observed public URLs and exact inspected length/hash. At most four redirects are
followed. Both HEAD and GET must resolve to the exact CDN URL with the expected
length and binary/ISO content type; encoded bodies and HTML/form responses fail.
Credentials, proxy use, cookies, automatic redirects and decompression are off.
Certificate validation is never bypassed. TLS 1.2 selection is process-local and
its former value is restored afterward.

One cancellation source and stopwatch bound acquisition to 600 seconds. Header,
stream-read, asynchronous file-write and flush waits share the remaining budget.
The absolute streaming ceiling is 8,500,000,000 bytes; the exact smaller expected
length is enforced too. SHA-256 is computed while streaming to an exclusively
created partial file. Only a complete hash match can rename it to the final ISO
name or produce the success object. File-system cleanup and state bookkeeping
can extend process exit beyond the acquisition deadline.

## Exact cleanup contract

Always invoke the same script with the same GUID, derived root, source SHA and
`-CleanupOnly`, after the host lab's independent cleanup step. Run it even when
download, installation, or the preceding cleanup step failed.

A separate `RUNNER_TEMP\adtr-eval-download-<GUID>.json` reservation and exact
run/GUID mutex are used. Reservation precedes directory creation. Confirmed
creation is recorded before writing media bytes. The journal binds run ID and
attempt, source SHA, paths, PID/start identity, stages and creation flags.
The ISO directory's ACL is restricted to SYSTEM and Administrators. All host
ancestors and entries reject reparse points. Existing foreign resources or
unconfirmed creations are preserved and reported as cleanup failures.

The cleanup operation:

1. Reads only the matching bounded ownership journal. Refuses to race the exact
   active downloader process; a matching PID and start time must be gone or the
   closed download must already be recorded inactive.
2. Reads any host journal with a 128 KiB bound and exact same-run/source/root
   identity. For the matching ISO, requires verified host cleanup and no unresolved
   native deployment, ISO, WIM or base-mount intent. An unreadable or inconsistent
   journal fails closed. It never modifies host state.
3. Requires the final ISO's `Get-DiskImage` identity to match and the image to be
   detached. It never independently dismounts an ISO or a WIM.
4. Deletes only its confirmed-created, fixed partial/final filenames and then
   the empty exact root. There is no recursive deletion or arbitrary file list.
   Unknown files, directories, links or ownership ambiguity preserve the root.
5. Persists `CleanupVerified=true` in its own journal and emits one cleanup-result
   object. The journal remains until runner disposal; its existence forbids reuse
   of that GUID. If cleanup cannot be proved, the caller must keep the overall
   result failed and rely on complete fresh-runner disposal, never claim success.

Caught download failures attempt the same exact cleanup after closing the HTTP
and file handles. Abrupt termination still requires the independent caller step.
No cleanup evidence is inferred from elapsed time or an attempted deletion.

## Validation

`Test-HostScriptSyntax.ps1` includes this file and parses it without executing it.
Run that separately with Windows PowerShell 5.1. Portable checks:
`python3 -B -m unittest discover -s scripts/ad_lab/host -p test_media_download_contract.py -v`.
They inspect source only; they do not send requests or exercise cancellation,
streaming, disk-image state or fault paths. Native parse/runtime and fault-injected
cleanup verification remain pending. No download or agreement acceptance was
performed while authoring this helper.
