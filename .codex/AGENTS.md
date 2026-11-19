# SiteBrush ownership rules

Share immutable data. Transfer mutable ownership. Communicate through channels.

## Application concurrency

- Every mutable object has one owner, including nested slices, maps, pointers,
  interfaces and closure captures. Channel synchronization alone is not ownership.
- A successful send transfers ownership. The sender must stop accessing mutable
  payloads until an explicit reply returns ownership. Failed submission retains
  ownership with the sender. Immutable channel contacts may be shared.
- Copy small messages. Deep-copy nested mutable data when a caller retains its
  own copy. Never expose an owner's maps, slices or mutable pointers in replies.
- Initialize shared immutable data completely before publication. Keep its
  representation private and expose scalar accessors or independent copies.
- Use local, typed command/reply/session channels and small select loops. Do not
  introduce a generic actor framework, shared cache, mutex or application atomic.
- Keep channel state local to its owner's goroutine. Long I/O belongs in a
  bounded worker/session; its completion must not overwrite newer generations.
- Combine authorization, version validation and mutation at one owner/transaction
  boundary. Do not replace one coherent operation with independent Get/Set calls.
- Internal lifecycle uses channels. Context remains permitted at HTTP, SQL,
  external-library and existing compatibility boundaries; do not propagate it
  through new internal APIs solely for cancellation.

## Task lifetime and shutdown

- The consumer supplies a one-shot reply channel and a session channel in each
  queued job. A persistent subscriber may reuse a response channel when required
  by an existing protocol. Only its lifecycle owner closes a session channel.
- Do not close a reply channel while a worker may send. Go has no safe universal
  test for a closed send destination. Prefer directional channel types.
- Check session/owner termination before work and at meaningful costly stages.
  Reply delivery must account for abandonment and shutdown. Queue bounds and
  overflow policy are part of the protocol; optional telemetry can drop with
  explicit loss accounting, required commands must report failure.
- Legacy synchronous database callbacks temporarily loan their captures. The
  worker must handshake before execution and return ownership through completion
  before the caller returns. Cancellation before that handshake skips execution.
  Cancellation afterwards goes to the SQL boundary and joins the callback.
  Never spawn an escaping goroutine that still accesses those captures.
- An owner shutdown deadline may report an unfinished callback, but must not
  pretend its captures or resources were released. An acknowledged commit is not
  undone merely because its subscriber disconnected.
- Do not automatically replay mutations after an owner restart without existing
  durable idempotency/version checks. Missing authorization fails closed.

## Public serving

- Preserve parallel reads of immutable prepared responses. Do not add a central
  HTTP queue or a goroutine/channel operation for each immutable content read.
- Preserve Front.State's channel owner and `front.exchange(nil)`. Readiness
  confirmation must match the owner's publisher revision, including restarts
  with the same address.
- Public content whose persisted configuration permits publication must remain
  available during dynamic control-plane failures. Private content, access
  restrictions and active security bans still require enforcement.
- Existing reviewed atomic exceptions are only `publicHTTPInFlight`,
  `PublicRuntime.current` and `publicGeneration.readers`. They respectively cover
  ingress admission, immutable publication and resource lifetime. Do not expand
  them into mutable business-state coordination. Replacement requires ownership
  proof and measured performance; see scripts/ownership/readme.md.
- Authorize before mutation commit or response admission. An already admitted
  static transfer may finish after revocation; do not promise retroactive recall
  of bytes already sent.

## Validation and delivery

- Put application tests in sitebrush_test.go; reusable-package tests stay nearby.
- Test nested alias isolation, concurrent updates, cancellation, queue overflow,
  generation rejection, TOCTOU and shutdown with active operations. Use channel
  barriers to control ordering where possible.
- Server builds and ordinary tests use CGO_ENABLED=0. CGO is allowed in a separate
  Linux race-detector CI job only for instrumentation, and in desktop Docker
  builds when their native backend needs it.
- Prefer existing GitHub checks; fix observed failures in the same PR. Do not run
  broad local/load checks when the user reserves them. Full tests, race detection
  and the architecture checks provide evidence, not a borrow-checking proof.
- Keep PR titles and descriptions in Russian when requested. Report exact passed,
  failed and pending CI states. Performance and OpenBSD behavior remain unverified
  until the user's measurements are available. Never infer the 95% nginx target
  from a successful test suite.
