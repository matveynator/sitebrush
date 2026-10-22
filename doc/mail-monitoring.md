# Mail and account monitoring

**Hosting and support → server metrics → Mail and accounts** shows delivery and account counters, with a link to the selected server in **Analytics → Security**. The detailed journal, cancellation, mail incidents and IP controls appear only in security analytics for server administrators. The journal contains one operation with expandable actions: the originating request, relay acceptance, SMTP attempts and final account outcome. Search by server, recipient, mail kind, event type and period; each page contains at most 50 operations. The event filter searches every observed phase within the selected period, even when the operation has since completed or an older step is outside the bounded visible timeline. The row keeps its final outcome. Filter forms and pagination return to the journal, and live updates do not repeatedly change the reader's scroll position. Repeated operations from one initiator are folded into a series after five requests in a ten-minute bucket.

## Delivery and account outcomes

Each operation keeps the originating installation, a non-secret flow identifier, message identifiers and the initiator IP. The installation IP is separate and labeled as reported by the installation. Forwarded client IPs use the existing trusted-proxy account boundary; loopback requests remain loopback requests.

A relay acceptance is an intermediate result. SMTP acceptance means the recipient server accepted DATA; it does not confirm inbox placement or reading. Recovery completion is recorded only after the password transaction succeeds. Registration completion is recorded after account creation. Account outcomes are committed in the site's SQL transaction and exported through a durable event queue, so a disconnected browser or lost synchronization acknowledgment does not erase a successful outcome.

The central server's counters cover unique messages and related account operations handled through that server. An originating installation's view covers its own operations. The same message's origin and relay queue events do not double the counter. Requests and completions are counted by their own timestamps, during the last 24 hours and 90 days; these windows need not contain the same cohort. Relay acceptance and recipient SMTP acceptance have separate counters.

The existing signed, byte-bounded monitoring exporter sends account outcomes to the center. A signed control response returns the center's delivery events to their originating installation. Replays do not increase counters. Older message-only history is joined to its originating flow when the authenticated ownership and matching message ID survive, including when an offline origin synchronizes later. An explicit saved password-recovery outcome can repair a missing completion; an email delivery alone cannot.

## Transport and process logs

Expanded actions include actual selected NetChan transport, both library versions, wire version 2, source and destination endpoints, and QUIC/UDP or TCP/TLS fallback. SMTP actions report the selected MX, connected endpoint, STARTTLS/plaintext mode, response code and sanitized error. Missing peer versions remain unknown. SiteBrush observes the existing transport adapters; the NetChan v2.1.0 dependency, wire protocol and transport priority are unchanged.

SMTP reports the host used for the connection and the port from its actual remote socket. The status code comes from the final DATA response on success or the failing SMTP command on rejection. Short recognized responses are normalized to safe summaries such as `OK`, `User unknown` and `Try again later`; unrecognized responses retain their numerical code and a generic summary. Arbitrary SMTP prose is never stored because a peer could reflect codes, passwords, links or message content. Acceptance still does not establish inbox placement.


The ordinary process log contains a final delivery summary with the route and result. The originating installation also records the confirmed recipient result received from the center. A successful account recovery is a separate meaningful event. Queue, attempt, retry and routine synchronization lines are available with `SITEBRUSH_MAIL_DEBUG=1`.

Monitoring synchronization reports its first failure, a summary of identical retries at most once every five minutes, and one recovery message. NetChan fallback details are debug-only. An older center may reject signed events containing new fields; the sender retries once with the previous signed schema and retains the full events until an updated center acknowledges their details. The updated center enriches the existing basic events without changing their audit fields or increasing counters. A rejected legacy retry remains an authorization failure.

Repeated process results are grouped by installation, initiator, kind and outcome: after the first four results, additional results are summarized at most once per minute. The writer owns at most 4096 active groups and an overflow group. Persistent rejection summaries retain exact attempt totals, first/last times and at most 20 sanitized samples per ten-minute bucket. Their recipient and error strings are samples, not unbounded grouping keys. Rate-limit counts include rejected attempts even though these attempts do not create individual journal rows.

The administrator interface uses localized dates in the browser's time zone. Process summaries include readable server-local dates and UTC offsets. Diagnostics never include mail bodies, passwords, confirmation codes or action URLs. Details are limited to 500 bytes and redact reflected secrets.

## Cancellation and mail IP blocks

The journal offers cancellation for queued mail and scheduled retries. Queue claiming and cancellation compete through the serialized SQL owner; a message owned by an active SMTP attempt cannot be recalled. Remote cancellation displays a pending result until confirmed. Durable cancellation tombstones prevent a delayed copy of the same origin/message from restarting the delivery. Failed, cancelled and accepted states remain distinct.

**Spam — block initiator IP** creates a mail incident, visible in the journal and **Analytics → Security**. The security journal and its incident list can block or unblock the IP. An existing incident remains after unblocking and can be blocked again. These are mail-request restrictions; they do not change the HTTP access policy.

The sitebrush.com server administrator controls global public-IP mail blocks. Other server administrators create installation-local restrictions and report incidents to the center. Loopback addresses, including all of 127.0.0.0/8, ::1 and mapped IPv4 loopback addresses, cannot be blocked. Existing loopback mail rules are ignored and can be removed. Private and link-local IPs are installation-scoped. A block cancels pending matching mail; an active SMTP attempt may already complete. Unblocking does not restart cancelled messages. Local independent administrator decisions survive a central global unblock.

Sender and installation IPs, incident summaries and block entries include geography from the existing local GeoIP database. Loopback addresses show local access; private and link-local addresses show local network. Missing geographic evidence remains explicitly unavailable. GeoIP lookups have one bounded time budget after the mail database session ends.

Global policy responses are signed by the pinned central installation identity and use monotonic revisions, including removals. Origin installations check for updates at startup, during activity reports and every 30 seconds while running. Offline installations retain the last received policy until they reconnect. New control features require updated originating and central installations; older peers continue the existing compatible delivery path and cannot falsely acknowledge an unsupported cancellation.

## Access and retention

The WebSocket journal requires a server-manager session, matching Origin and an account CSRF token for commands. Commands and returned delivery events are scoped to their authenticated installation. A remote installation cannot create global bans or cancel another installation's mail.

The detailed journal and rejection summaries retain 90 days. Message bodies are cleared on terminal delivery or cancellation. Durable account events remain pending until the control journal acknowledges them. Journal operations and counters are projections of immutable events; synchronization and migration can replay these events safely. Remote views report their last synchronization time. Bounce/DSN processing and inbox-delivery confirmation are outside this feature.

## Account email abuse protection

Registration, password recovery and site registration requests require a one-use, six-character PNG CAPTCHA. Its random answer is stored only as a salted hash in the server control database; the browser receives an opaque form nonce and an inline PNG. The image endpoint uses HTTPS-only HttpOnly cookies; password-free HTTP onboarding posts its nonce as a hidden field. A challenge is bound to its source IP, domain and form purpose, lasts five minutes, requires at least two seconds before submission and is consumed even on a wrong answer. Image issuance is limited to six per IP per minute, with at most 10,000 live challenges. New-domain registration remains available before HTTPS without creating a site database or transmitting a password.

The server applies these limits across all sites and both registration/recovery forms:

| Boundary | Limit |
| --- | --- |
| Source IP requests, including invalid CAPTCHA attempts | 8/minute, 20/hour, 40/day |
| Different recipient addresses requested by one IP | 3/hour, 5/day; the next distinct address triggers a block |
| Different recipients inside one minute | 3; the next distinct address triggers a block |
| Network requests | 100/hour per IPv4 /24 or IPv6 /64 |
| CAPTCHA-approved requests to one recipient across all source IPs | 60-second cooldown, 3/hour, 6/day |

A source violation blocks account email requests from that IP for one hour, or 24 hours for a daily violation. It does not lock the victim's account or disable ordinary website visits. Blocks survive restarts. Recipient delivery quotas are charged only after a valid CAPTCHA, so simply naming a victim in an unsolved request cannot exhaust that recipient's allowance. The request ledger is retained for 24 hours and capped at 20,000 rows; reaching capacity fails closed for new account email requests.

The security mail journal shows the initiating IP when known. Follow-up outbox events inherit it from the queued event, including after restart. Historical rows do not acquire inferred IPs. Analytics → Security shows persistent administrator restrictions and expiring account-mail guard restrictions together. Removing an account-mail abuse restriction also removes its associated local registration-spam HTTP restriction; unrelated security decisions remain. Loopback sources cannot receive IP blocks. Delivery workers recheck source blocks before sending queued messages. An already accepted SMTP message cannot be withdrawn.

IP attribution uses the account subsystem's trusted proxy chain. Configure `SITEBRUSH_TRUSTED_PROXIES` for an actual reverse proxy; direct clients cannot select their identity with forwarded headers. Requests with unavailable client identity fail closed. Remote server journals retain their originating IPs in the existing signed diagnostic synchronization; blocking controls apply on the server that owns the requests.

A raster CAPTCHA is a barrier against trivial parsing and guessing, not a guarantee against OCR, browser automation or human solving services. Independent IP, network and recipient limits remain mandatory.

## Registration cooldown

New registration emails reserve a cooldown for the source IP and normalized domain, independently of the recipient address. The first unsuccessful attempt permits retry after one hour, the second after six hours, the third after 24 hours, and the fourth and later after three days. An admitted request reserves its pause immediately, preventing parallel submissions or email changes from producing additional letters. Requests rejected during a pause do not extend it or raise the penalty.

Only a delivered, unconfirmed attempt (after the confirmation deadline), or an explicitly rejected confirmation, raises the failure count. Each attempt counts once. Delivery still in progress retains its reservation without counting as a user failure. Terminal SMTP/NetChan/HTTPS failures, expired delivery, abandoned preparation and known account-creation infrastructure failures release the reservation and preserve the previous failure count. Ordinary CAPTCHA/recipient safety quotas still apply. Successful account creation clears the originating attempt's IP/domain state, even when confirmation uses another IP. The control database retains cooldowns across restarts.

Registration shows the server-derived remaining time, disables retry while paused and keeps confirmation available. At expiry, retry loads a fresh form and CAPTCHA. Server admission remains authoritative; changing the browser clock or omitting JavaScript cannot authorize another email.

When four unsuccessful registrations on one IP/domain pair involve four distinct recipient addresses, the server records **Registration email spam** (`registration-mail-abuse`) in Analytics → Security. Evidence retains at most four recipient hashes; addresses, codes and tokens are not copied into the security event. The IP is blocked from account email requests for three days. With automatic security blocking enabled, the existing guard also applies a local site block for at least three days. Existing global reputation rules are unchanged. Unknown, unconfirmed domains use the existing server owner's security storage when available and do not create site databases. A bounded background sweep checks expired registrations every minute, so detection does not depend on another request. Refreshing a blocked page does not create another spam incident or extend the block. Successful registration clears its accumulated recipient evidence.
