# AI control plane task status

This checklist follows the 26 numbered requirements in the security task.
"Implemented" means the change has local automated coverage; it does not imply
production deployment or acceptance by a real provider. Two requirements still
need external acceptance: client compatibility (5) and the production scenario
(26). The task as a whole is therefore not yet fully accepted.

| # | Requirement | Status | Implementation and evidence |
| --- | --- | --- | --- |
| 1 | Remove public AI pathname APIs | Implemented | MCP/OAuth use query selectors; old handlers and the public Analytics pathname are removed. Only the gated authorization-server discovery adapter remains. `TestAIProviderGateBeforeQueryRoutingAndProxyTrust`, `TestBlockedAdminIPSeesStaticSiteAndNoSiteBrushRoutes`. |
| 2 | Selectors independent of pathname | Implemented | MCP and OAuth use the same canonical resource from arbitrary paths. Browser Analytics uses `?request=analytics`. `TestAIProviderGateBeforeQueryRoutingAndProxyTrust`, `TestBrowserAnalyticsQuerySelectorIsIndependentOfPath`. |
| 3 | Untrusted selectors do not exist | Implemented | Admission precedes selector processing; public responses match ordinary routing across paths and methods. `TestAIIngressStealthQueriesMatchPublicRouting`. |
| 4 | No public AI advertising | Implemented | No public discovery Link, OAuth challenge or CORS hint. `TestHiddenAISelectorsMatchPublicMiddlewareResponses`. |
| 5 | Real MCP/OAuth client compatibility | Partial | Explicit query resource metadata, canonical resource binding and gated RFC 8414 discovery work with the Go MCP SDK and local browser approval. Real ChatGPT, Claude and configured xAI clients have not been tested; the retained discovery shim still needs that client-level confirmation. |
| 6 | Default-deny provider ingress | Implemented | Official OpenAI snapshot/cache, optional native mTLS and published Anthropic CIDR; xAI requires explicit administrator CIDRs. `pkg/aiingress` provider/certificate/invalid-refresh tests and production proxy-trust regressions. |
| 7 | Separate machine and owner admission | Implemented | Machine endpoints require provider admission; owner approval uses existing admin session/IP and CSRF policies. Provider gate tests, `pkg/aicapability` pairing/OAuth tests, local Chromium approval. |
| 8 | No separate JSON/TXT security store | Implemented | Application persistence uses site control SQL participants. Legacy files are import sources only and are deleted after completion. `TestLegacyAIMigrationRetriesCommittedSitesWithoutRestoringRevokedGrants`. |
| 9 | Global and site content databases | Implemented | Global registry/control dispatcher and isolated site content databases. `TestSiteSubsystemIsolationAndUnknownHost`, `TestLegacySitePartitionPreservesExistingRecords`. |
| 10 | Site control/security database | Implemented | Settings, account security, provider policy and durable AI/OAuth state belong to the site control participant. Restart, corruption and restore tests use the production router. |
| 11 | Site Analytics database | Implemented | Dedicated SQL owner and storage, including history migration. `TestLegacyAnalyticsFilesMigrateIntoSiteOwner`, `TestAnalyticsGoalsUseSeparateSiteAnalyticsDatabase`. |
| 12 | One global and three site owners | Implemented | Explicit channel participants and request/reply routing; subsystem ownership is checked by production-router tests and `scripts/architecture`. |
| 13 | Bounded loading; no arbitrary Host creation | Implemented | Registry lookup precedes loading; 256-site bound. Unknown-host regression verifies that probes create no storage. |
| 14 | No transport-owned database writes | Implemented | HTTP/MCP use routed SQL and existing operation pipelines; cross-owner statements are rejected. Automatic SSL now performs separate content/control lookups. `TestSiteSubsystemAutomaticSSLCandidatesUseSeparateOwners`. |
| 15 | Capability manager as control facade | Implemented | Site managers persist through their control participant, which is the sole application source of truth. Concurrent issue/revoke and restart tests. |
| 16 | Hashes, ephemeral secrets and bindings | Implemented | Credential hashes persist; temporary sessions/codes/pairing do not. `pkg/aicapability` tests cover PKCE, site/resource/client bindings, rotation/replay, narrowing and revoke; migration tests inspect stored documents for raw secrets. |
| 17 | Crash-safe legacy migration | Implemented | Transactional verification and markers precede source cleanup. Busy/interrupted multi-site import retries preserve later revocations. Invalid documents return no partial import. |
| 18 | Stable internal site IDs | Implemented | Registered internal IDs define site directories, independent of raw Host paths. Registry/partition and unknown-host tests exercise the layout. |
| 19 | Backup and restore all database groups | Implemented | Updater snapshots include global/content/control SQL; Analytics and archives have their own backup group. Restore preserves policy/revocations without reviving ephemeral access. `TestSiteDatabaseBackupArchivesAllOwnerSnapshots`, `TestSiteControlSnapshotRestoresPolicyAndRevocationsWithoutAccessSessions`, `pkg/appupdate` tests. |
| 20 | Site-local policy; global LKG ranges | Implemented | Site control owns provider enablement; global SQL owns validated range documents. Refresh failure retains the verified snapshot. Provider policy and invalid-refresh tests. |
| 21 | Hide the whole AI control plane | Implemented | All AI selectors, owner-approval probes and the discovery shim pass the same ingress gate. Public route/middleware equivalence tests. |
| 22 | Redact logs and Analytics | Implemented | Existing `SafeQuery` boundary covers OAuth/pairing/capability secrets; untrusted selectors are removed before observations. Credential-redaction tests in `pkg/accountauth` and `TestMCPPublicTransportSecurityAndCredentialRedaction`. |
| 23 | Security processing order | Implemented | Resolve trusted proxy/client address, admit once, then parse selectors and authorize. `TestAIIngressAdmissionRunsOnceAcrossProductionHTTPAdapters` counts actual policy reads across middleware and routing; denied callers receive no OAuth challenge. |
| 24 | Drafts and publication stay separate | Implemented | Existing version-checked operations remain in use. `TestMCPRemoteOAuthAndExistingSiteOperations` exercises draft update, explicit publish, rollback and revoke with the SDK. |
| 25 | Security regressions | Implemented | Production-router isolation, concurrency, busy migration/retry, corruption/recovery, shutdown, secret storage, backup/restore, symlink escapes and query-fingerprinting tests; CI includes native database regressions on Linux/macOS/Windows. |
| 26 | Real acceptance on twochicks.sitebrush.com | Not run | This branch has not been deployed there. Actual provider ingress, owner login/Allow, tools and immediate revoke on that hostname still require the deployed build and real provider connections. |

## Verification

Run all native/server checks with `CGO_ENABLED=0`:

```sh
go test -count=1 ./...
go vet ./...
SITEBRUSH_ANALYTICS_BROWSER_TEST=1 SITEBRUSH_MCP_BROWSER_TEST=1 \
  go test -count=1 -timeout=180s . \
  -run '^Test(ConversionBrowserRegressions|MCPApprovalBrowserRedirect)$'
```

GitHub Actions also checks CodeQL, gosec, govulncheck, native maintenance and site
database migrations on Linux/macOS/Windows, and server cross-compilation for
Linux/macOS/Windows/FreeBSD/OpenBSD on amd64/arm64 plus Linux/386. NetBSD is not
part of the required/default server matrix because the current pure-Go SQLite
dependency does not support that target.

## Deployment and remaining acceptance

Install the generation-2 launcher/supervisor before the first database-layout
migration. See [migration and restoration](ai-control-plane.md#migration-and-restoration).
Existing integrations must reconnect using `?mcp`; durable credential resource
bindings are never widened to accept both the old and new URL.

After deployment, verify ordinary callers receive the same public response for
the same pathname with or without `mcp`, `oauth` and `ai` selectors. Then connect
from real enabled providers, complete owner approval from an allowed browser IP,
exercise list/read/report/update/publish/rollback and revoke during active work.
Record the provider/client versions, deployed commit and results before marking
requirements 5 and 26 complete.
