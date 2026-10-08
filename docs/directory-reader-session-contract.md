# Real-browser directory reader/session evidence

This test-only slice follows commit 2be15ef and adds source for the remaining planned browser scenarios for a custom data reader and a stale privileged response after a same-context account switch. Production authority remains unchanged.

Use a new isolated directory-readers suite with a fast empty synthetic LDAP fixture. Create the actual domain, resource scope, bound account and explicit directory grant through normal UI/API flows; publish a real successful empty observation. Create a custom role with domains.read and directory_assets.read only, assign the existing domain scope, and create/enroll an actual reader account. No tasks or operation-account metadata permission and no directory credential-use grant are added to that role.

The reader must see the successful observation, while task/receipt, synchronization/cancellation and grant metadata remain forbidden. The UI must not expose privileged mutation or grant controls. Data-read authority is intentionally independent of the producer's account/use grant; do not incorrectly require the reader to own the observation task.

For cross-tab isolation, capture a real privileged grant-metadata GET response using route.fetch, hold only its browser delivery, and switch the shared cookie session in a second tab from admin to the reader. Release the original genuine response after reader identity is current. The first tab must remain the reader, remove old private controls/metadata, and permit only the reader's actual data access. No API response body is fabricated, no secret is broadcast/persisted, and a cancelled transport is not treated as a successful late-data application.

The new suite has an independent bounded timeout and retains the old suites' limits, CI parallelism 2 and final verification. The same real account switch also exercises trusted focus/visibility revalidation in a second old tab with both notification transports unavailable. Typecheck/discovery or a missing-fixture hard failure is not real browser/DB/AD acceptance. All fixtures and credentials are disposable synthetic data.

Setup respects the unchanged production limit of ten sensitive actions per actor per fifteen minutes. The bootstrap admin uses ten actions: password/MFA setup (three), domain/resource group/scope (three), producer account creation (one), and reader role/scope/account creation (three). The separate UI-created platform administrator uses eight: its password/MFA setup (three), operation account/connection grant/binding/directory grant/sync (five). No rate table is cleared or clock/limit changed. An HTTP request that fails after proof verification can still consume its actor's budget.

Held response evidence distinguishes actual requestfinished delivery from a browser-recorded abort/cancel. Any other requestfailed outcome fails the test. The test never equates an arbitrary network failure with successful stale-response isolation.

The inherited directory and directory-controls browser setups also exceeded that unchanged limit. Both now create the producer through actual UI and use seven setup-admin actions and ten producer actions. Directory counts the proof-checked denied sync and final grant revocation; controls counts both sync requests and cancellation. Existing task ownership, receipt-recovery checks and suite timeouts are preserved.
