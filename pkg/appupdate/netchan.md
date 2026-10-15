# Signed release delivery over NetChan

This is a proposed transport extension. The installed updater currently downloads official GitHub assets over its HTTPS boundary adapter. Its existing NetChan contacts carry local lifecycle commands; the separate announcement contact does not carry executable bytes.

## Ownership and trust

Keep `pkg/appupdate` responsible for validating the release signature, numeric build sequence, target platform, declared size and complete binary SHA-256 before execution. The release-signing public key remains the authority for executable content. A server authenticated by TLS cannot authorize an unsigned binary or a downgrade.

A separate release-delivery subsystem on sitebrush.com owns an artifact cache and an input channel. It obtains the signed manifest and numbered assets from official GitHub releases through the existing boundary adapter, verifies them before making them available, and serves immutable objects by manifest digest and artifact digest. Publishing remains the existing GitHub workflow. The mirror never receives the release private signing key.

Use a dedicated, versioned NetChan session for transfer, separate from announcement/probe and local supervisor channels. A slow executable transfer must not hold up authorization or live version proofs. Clients initiate outbound connections, so installation-side inbound ports are unnecessary. Authenticate the central endpoint and installation identity using the pinned identities already registered by SiteBrush; keep this endpoint's capacity independent of mail contacts. Changing its port/address or trust anchor requires a supported protocol transition.

## Transfer protocol

1. The consumer sends `CatalogRequest{Protocol, Reply}`. The reply contains the original signed manifest envelope, subject to the existing metadata size limit. The installation validates it locally before requesting an artifact. A later manifest change requires a fresh administrator confirmation.
2. The consumer opens a dedicated session with `ArtifactRequest{ManifestDigest, ArtifactDigest, OS, Arch, Offset, Session}`. These are validated identities, never file paths or arbitrary URLs. The mirror derives the allowed filename from verified metadata. It accepts only bounded offsets inside that artifact.
3. A per-transfer worker owns its file handle and session goroutine. The consumer grants a bounded read credit with `Next{Offset, MaximumBytes, Reply}`. The worker replies with one `Chunk{Offset, Bytes, Final, Error}`. An initial 64 KiB chunk and one outstanding credit bound application buffering; negotiated hard limits must also respect NetChan's framing limits. Never send a whole executable as one message.
4. The consumer owns an exclusive temporary file. It validates the expected offset and maximum remaining size before each write, hashes the stream, and requests another chunk only after consuming the preceding reply. This supplies backpressure without a shared transfer map or mutex. An early final marker, repeated/skipped offset, oversized chunk, transport error or mismatched hash fails the transfer.
5. A successful transfer fsyncs the temporary file and its directory before it becomes a candidate. Existing preflight, backup, migration, probation and rollback steps remain unchanged. Cleanup uses journal references regardless of which transport produced the candidate.

The session/reply capability defines task lifetime. Closing it, disconnecting, a failed send or an idle timeout cancels the producer, closes its file, and releases its bounded session slot. Jobs waiting for a slot must still observe their consumer's lifetime. No background producer may continue buffering for a disconnected installation.

## Retry and fallback

A reconnect can resume only the same signed artifact and manifest digest. Rehash the retained prefix locally before continuing; the final full SHA-256 is always mandatory. Keep an exclusive, size-bounded partial file associated with that transfer. A different manifest, mismatching prefix or changed artifact discards the partial transfer.

If the official mirror is unavailable, the installation can use the existing official GitHub HTTPS adapter with the exact already-confirmed signed metadata. Failure to authenticate the mirror never relaxes content verification or permits another host supplied by a peer. Partial files from different transports are not combined unless the signed artifact identity and validated prefix match. Neither a catalog response nor an announcement grants permission to install.

## Verification before enabling

Test real NetChan transfer on Linux, macOS and Windows; both supported architectures must compile. Include wrong server/installation identities, forged manifests, replayed older releases, disconnects at each stage, revoked reply channels, stalled consumers, slow disk, slot exhaustion, invalid offsets, truncated/oversized chunks, corrupt resume prefixes, cancellation, fallback and a manifest changing during transfer. Verify bounded memory with concurrent large artifacts, no surviving transfer workers/file handles after cancellation, and unchanged working binaries on every failed transfer. Test the mirror without access to the signing key.

This adds a SiteBrush-controlled mirror and operational capacity requirements. GitHub remains the publisher and fallback; enabling the new transport should be a separately reviewed change with deployment of its central endpoint and protocol compatibility established first.
