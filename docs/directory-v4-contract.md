# Dictionary4: bounded raw group member backend

Engineering [#151](https://github.com/yunpiao/adtr/issues/151), parent
[#30](https://github.com/yunpiao/adtr/issues/30), AD-F-043. Base main
013ba239e5a6af5061a252125a92779c33bf1725, schema19. Implementation in progress,
not product accepted; 0/209 remains unchanged. FLD-0509 is list[].member,
multi-text; FLD-0511 is list[].memberCount, integer. The Issue transcription
specifies no membership/count semantics. This is a new explicit local contract,
not old-system parity. No UI, real AD/Windows, production activation or deployment.

## Fixed wire and raw values

New purpose/kind domain.directory_read.v4, payload version1, dictionaryVersion4.
Request exactly the v3 ten attributes followed by member. Keep current principal
filter, source/domain boundary, TLS, paging and authorization checkpoints. Never
request memberOf, tokenGroups, arbitrary attributes or recursive controls.

StoredObjectV4 fields, in canonical order: base, supplemental, identity,
membership. First three components retain v3 meanings and validation. Membership
is RawMembershipV4 with the sole key memberBytes ([]byte array, nullable).
Go API: DecodeV4(Entry), ProjectV4(StoredObjectV4), EncodeStoredObjectV4,
DecodeStoredObjectV4, ClearStoredObjectsV4; corresponding V4 accumulator follows
V3 interfaces. PublicObjectV4 embeds PublicObjectV3 and appends rawMember
([]string), returnedMemberCount (*int), memberReadState (string), in that order.
Missing member descriptor means nil/null rawMember and count, not_returned.
Present empty descriptor means nonnil empty array, count0, returned. Present
nonempty descriptor means raw values, actual returned count, returned. Preserve
nil versus empty throughout wire ownership copies, storage, SQL and projection.
Non-group objects must have nil memberBytes; any member descriptor on a non-group
is unsupported_directory_profile, even an empty descriptor. No memberCount alias.

Preserve original byte values and receive order; no trim, normalization, sorting,
DN equality, target lookup, business classification or relationship expansion.
Each value must be nonempty valid UTF-8, maximum4096 source bytes. Preserve valid
UTF-8 control/NUL text as raw data, not certification of a valid AD DN. Stored
base64 avoids PostgreSQL JSONB NUL restrictions. Reject exact byte duplicates.
Maximum256 values and65536 raw bytes per object. MaxStoredObjectV4Bytes=262144.
These are local protection limits, not AD maximums or accepted capacity. Keep
10000 rows/100 pages/1000 rows per page/64KiB cookie/16MiB observation/125sec task
and8MiB whole HTTP response. Limits can tighten only. Removing them permits
unbounded resource use; their cost is rejection of larger legitimate sources.

Reject ALL member range descriptors, including member;range=0-*, as unsupported
profile; never continue ranges. Detect the ranged companion even when a plain
member descriptor is an empty placeholder. Malformed descriptors, unknown fields,
options, duplicate aliases and invalid UTF-8 fail closed. Representation caps,
member on a foreign object kind, and recognizable range forms map to
unsupported_directory_profile; aggregate limits map directory_limit_exceeded;
malformed response maps invalid_response. Failure clears owned buffers and returns
no partial observation. Explicit LDAP access denial, referral, truncated search,
page failure and cancellation never become empty success.

Microsoft sources:
- member excludes primary-group-only membership:
  https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-ada2/763d0861-f4f6-4251-847e-7e8dedcfe73f
- Attribute read denial can look exactly like an absent attribute:
  https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-adts/8271b44d-a755-4872-a762-1ac57152099d
- Ranged consistency requires the same connection and no intervening modification:
  https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-adts/e27b48db-6f82-44cd-9038-2e54f790cc1f
- memberOf is a computed direct backlink with DC/GC scope limitations:
  https://learn.microsoft.com/en-us/windows/win32/ad/security-properties

Consequently returned count0 proves zero returned values, not an empty group;
missing means unknown absence/ACL omission. No primaryGroupID/SPN substitution,
transitive closure or cross-domain completeness. Raw foreign/FSP-looking member
text is preserved without fetching or asserting access to its target. An oversized
or ranged group fails the whole v4 observation; old producers remain usable.
The multi-page domain observation is not a transactional AD-wide snapshot.

## Lifecycle/storage and integration interfaces

New global-and-v4 gate ADTR_DIRECTORY_READ_V4_ENABLED defaults false. Global
ADTR_DIRECTORY_READ_ENABLED is also required. Gate installation grants no access;
old purposes/grants never imply v4. No real setting/enrollment/grant change.
Existing current tenant/actor/domain/function authority, source/account/credential/
policy revisions, lease/fence/attempt, cancellation and quiescence rules apply.

Migration20 preserves old bytes/hashes/validators and copies no grants. Require
schema19 exact baseline and quiescent credential execution; add v4 closed SQL
validation/purpose/use/dependency/audit/publication guards. All observation reads
validate full body/digest/count/GUID uniqueness/source before selecting any row.
Publication is staged under exact v4 use and exposed only for succeeded tasks
with current pins. Immutable canonical envelope order: dictionaryVersion, objects,
source. Existing DirectorySource schema is unchanged.

Transport interface: DirectoryV4Config/DirectoryV4ReadAuthorization/
DirectoryV4Observation {Objects []StoredObjectV4; Source DirectorySource},
ReadDirectoryV4 and Discard follow V3 exactly. Storage interfaces:
encodeDirectoryObservationV4, decodeDirectoryObservationV4,
publishDirectoryObservationV4Tx and loadDirectoryV4ObservationTx follow V3
signatures with V4 types. DirectoryV4ListTx and DirectoryV4List expose fixed-v4
observation discovery. No old observation upgrade or cross-observation fill.

## API

/api/directory/v4/{observation,receipt,task,sync,cancel} follows exact-purpose v3
controls, fresh proof, CSRF/origin, current authority and recovery semantics.
Separate /api/directory-credential-use/v4 routes retain builtin-admin management,
fresh proof and current scope. Installing routes creates no grants.
GET /api/group-assets/v4/members requires domainId, expectedRevision,
expectedCredentialRevision, observationId and objectGUID. No optional latest or
fallback; no body/duplicate/unknown keys. Return dictionaryVersion4, selection
(current revision pins), observationId, source and object (PublicObjectV4,
kind=group). Requested observation unavailable/corrupt/stale yields409; missing
or wrong-kind object yields404. Current reader must have domains.read AND
directory_assets.read and domain scope, independent of producer gates/grants.
GET cannot call LDAP/use credentials/submit tasks. Entire observation validates
before GUID/kind selection; complete JSON response bounded8MiB including newline.

## Verification / evidence

Baseline then full make check. Preserve v1/v2/v3 golden bytes/hash/grants. Named
wire/codec/SQL tests cover null/empty/value ordering, exact duplicates, NUL/UTF-8,
all bounds, non-group attributes and ranges (including placeholder+range).
Migration19->20/repeat/quiescence/collision/no copied grants; bypass corruption,
full-body off-row failure; cross-version/current-authority/revision/pin/tenant/
domain/gate/read-only/cancel/publication tests. Genuine registered Worker through
synthetic certificate-verified TLS LDAP (exact11 requested attributes), real PG
and HTTP must validate stored bytes and projected values. Injected success
observations are isolated unit/integration fixtures, not this full-path proof.
No skipped/blocked DB/browser stage counts as passed. Independent actual-diff
review, final head full CI and current-main compatibility precede normal merge;
main-push CI is separately verified. #30 and #151 remain open; AD-F-043 stays
in_progress and accepted count0. Remaining old memberCount semantics, memberOf,
range continuation, nested/primary/cross-domain parity, UI, Windows and capacity
are explicit open requirements.

## Evidence provenance gate

The backend artifact retains the actual API rows and stored canonical bodyBase64,
SHA256, source, identity/member sections and original-return ledger outcome.
Before upload, validate_directory_v4_evidence.py requires a closed schema,
recomputes the body hash, compares body/API values and expected fixture members,
and binds actual checkout commit/tree separately from event head/base, workflow
commit, run/attempt and fifteen source SHA256s. Missing/mismatched evidence fails
the job. CI uses supplied base identity without requiring HEAD^ in shallow clones.
The added backend suite increases the50-job baseline to51; all old jobs and gates
remain. Exact workflow hash/count baseline updates are explicit review changes.
