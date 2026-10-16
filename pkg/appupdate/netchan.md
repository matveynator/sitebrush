# Signed release delivery over NetChan

`NewNetworkCatalog` uses the official `sitebrush.com:9878` NetChan endpoint first. The pinned central installation starts `ServeReleaseMirror`, reading the same `storage/chroot/sitebrush.com/download` directory populated by the existing crosscompile `-sync` command. The mirror never needs the release private signing key. It validates published signatures and binary checksums before serving them. Only the build machine or protected GitHub release job signs releases.

## Ownership and trust

Release reads are public, as in the HTTPS download directory: updating must work independently of installation registration and email campaigns. TLS 1.3 requires the pinned sitebrush.com Ed25519 identity. This channel is separate from the mutually authenticated announcement/proof channel on port 9877. It exposes no installation-changing command or database access.

`pkg/appupdate` validates the original signed manifest envelope, numeric build sequence, selected OS/architecture, exact size and complete binary SHA-256 locally. Authenticating the mirror cannot authorize unsigned bytes or downgrade the supervisor's persisted highest release sequence. A newly discovered manifest does not install anything; the administrator still confirms its digest through the normal protected update mechanism.

The central listener admits eight independent transfer workers. Each contact owns its request, file handle and credit channel; there is no shared transfer registry or lock. Excess sessions are disconnected. A consumer initiates outbound communication, so installations need no additional inbound port.

## Protocol version 1

1. A `ReleaseRequest` with an empty `Version` requests the latest original signed envelope through its `Reply` channel. The client enforces the existing manifest size and signature limits.
2. An artifact request specifies the numeric `Version`, canonical `ManifestDigest`, `OS`, `Arch`, `Offset` and `PrefixSHA256`. No peer-provided path or URL is opened. The mirror derives the filename from the signed numbered manifest, opens it inside an `os.Root`, verifies its size and full checksum, then validates the resume prefix.
3. `ReleaseResponse` returns that signed envelope and a dedicated `Session` credit channel. The consumer verifies that the signed metadata still matches its confirmed manifest before writing any chunk.
4. One `ReleaseCredit{Offset, Reply}` authorizes one `ReleaseChunk{Offset, Bytes, Final, Error}` of at most 64 KiB. The consumer requests its next chunk only after writing the preceding chunk. Repeated/skipped offsets, oversized bytes, empty progress or an incorrect final marker fail immediately.
5. `Finish` acknowledges completion. The mirror waits for the caller to close its parent contact, so returning from a local channel send cannot revoke the final network frame before delivery. Closing the parent contact revokes all nested capabilities. Failed sends, disconnection, 30-second idle credit timeout, server shutdown or a 15-minute session limit stop the producer and release its slot/file handle.
6. The consumer rechecks the complete size and SHA-256 and fsyncs its exclusively created candidate. The existing supervisor makes it durable before preflight, backup, cumulative migrations, probation and possible rollback. A failed download removes the candidate and never overwrites an existing executable.

## Reconnect and fallback

A transfer can reconnect once using the same exclusive partial file. The consumer rehashes the retained prefix; the mirror compares that hash against the signed artifact at the requested offset. Completion always requires the full binary checksum. A changed manifest or corrupt prefix cannot be resumed.

If NetChan is unavailable or verification fails, the consumer truncates its candidate and tries numbered artifacts from `https://sitebrush.com/download/`, then `https://sitebrush.ru/download/`, then the official GitHub `release-<version>` asset. Catalog fallback uses the signed `latest/server-app/update.json` on sitebrush.com and sitebrush.ru followed by the official GitHub stable manifest. Redirects remain restricted to official HTTPS download/release hosts. Fallback never relaxes signature or checksum requirements and never mixes unchecked bytes across transports.

## Publishing and operations

Official crosscompile `-sync` destinations automatically require signed server metadata. The existing local Docker desktop workflow is unchanged. Local builds and GitHub publication use the same full-history Git commit count, preventing unrelated workflow counters from hiding a locally published newer source revision. Each new release requires a new source commit or a deliberately assigned larger numeric version.

Install and start the first updater-enabled central binary through the normal installation procedure, then permit inbound TCP 9878. Before that endpoint is deployed, clients retain the signed HTTPS fallback. Central identity rotation requires an explicit compatible pin transition. Releasing does not send mail; only the central administrator's exceptional security/feature campaign does that after fresh installation status proofs.

Real TLS/NetChan tests cover chunked delivery, wrong pins, forged/changed/oversized manifests, checksum failures, invalid credits/chunks, backpressure, verified prefix resume, interrupted reconnection, cancellation and HTTPS fallback. Database-only snapshot tests verify that published releases, site files and the `latest` publication link are excluded and remain untouched by rollback.
