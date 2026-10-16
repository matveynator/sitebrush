# AI control plane

SiteBrush hides external AI access by default. A public request with `mcp`,
`oauth`, `ai`, `ai_token` or `ai_session` selectors is processed by the ordinary
site router when its source has not been admitted. The router does not add AI
discovery links, OAuth challenges, CORS responses or protocol errors for that
visitor. This also applies to the authorization-server compatibility route.

## Connecting a provider

Sign in from an allowed administrator address, open Profile and enable external
AI access and the required provider. Connect `https://example.com/?mcp` in the
provider's MCP client. OAuth browser authorization uses the existing account,
session, CSRF and administrator IP policies. It does not require a provider IP.
Machine requests require provider network admission and then OAuth or capability
authorization. Updates and rollbacks remain drafts; publication is separate and
version checks remain required.

The provider selection is site local. OpenAI uses its official connectors egress
list. The embedded verified snapshot bootstraps a global SQL cache. A background
refresh fetches the official HTTPS document daily, validates every prefix and
commits only a complete valid document. Failure retains the previous snapshot.
Anthropic's published outbound range is `160.79.104.0/21`. No guessed xAI range is
included: xAI needs explicitly configured administrator CIDRs. Custom ranges
cannot be `/0`.

Native TLS requests a client certificate without requiring one from browsers.
If OpenAI presents one, SiteBrush validates its official CA chain, client-auth
usage and exact `mtls.prod.connectors.openai.com` SAN. The profile can require it.
A TLS-terminating proxy cannot assert a certificate through an HTTP header; use
TLS passthrough when requiring native mTLS. Client addresses use the existing
`SITEBRUSH_TRUSTED_PROXIES` boundary; headers from other peers confer no trust.

## Discovery compatibility

All machine selectors are independent of pathname. `/?mcp`, `/about?mcp` and
`/anything?mcp` reach the same canonical resource `https://example.com/?mcp`.
OAuth uses `?oauth=resource|server|authorize|token|register|revoke`.

The MCP challenge advertises the exact `/?oauth=resource` metadata URL. RFC 8414
issuer discovery still requires `/.well-known/oauth-authorization-server`; this
single gated shim delegates to the same `?oauth=server` implementation. There is
no protected-resource well-known route and no pathname-based MCP/OAuth API.
Existing connections configured with the removed URL must reconnect using the
query URL. Existing durable grant hashes, scopes and revocations are retained;
resource bindings are not widened to make old credentials fit a different URL.

## Database owners

The server control dispatcher owns the global database, including hosting
records, the site registry, stable site IDs, official provider range cache and
legacy AI migration journal. The registry admits sites before their participants
become reachable. Unknown hosts cannot create site participants or files. The
loaded site set is bounded at 256; capacity exhaustion refuses new loading.

Each active site uses three distinct SQL participants:

| Participant | Database | State |
| --- | --- | --- |
| Content | `db/sites/<site-id>/content.db` | pages, revisions, publication, assets, quotas, import frontier |
| Control | `db/sites/<site-id>/control.db` | accounts, sessions, passkeys, TOTP, IP policies, settings, AI grants, OAuth and provider policy |
| Analytics | `db/sites/<site-id>/analytics/analytics.db` | observations, reports, configuration and browser checkpoints |

The paths above are relative to the configured database directory's parent.
Published file bytes retain the existing storage jail. Historical Analytics
archives sit beside the analytics database. Workers own their queues and SQL
handles; HTTP/MCP adapters use the router and existing editor pipelines. SQL
statements crossing subsystem ownership are refused. The capability facade
keeps temporary pairing, authorization codes and access sessions ephemeral;
capability and refresh credentials persist only as hashes.

## Migration and restoration

A legacy site is migrated before it is exposed through its participants. Each
subsystem copies and verifies its tables in a transaction and commits an import
marker. Only then is its legacy source cleared. A subsequent attempt reuses the
committed destination. Closed content handles are relocated to the stable site
ID directory. The old empty schemas remain available for the existing schema
migration ledger; their rows are owned exclusively by the destination database.
Missing or corrupt control databases do not become empty authorized sites.

Legacy capability JSON and `.oauth` sources are validated together, partitioned
by site and imported through each control participant. Each transaction contains
the documents, verification and digest marker. The global completion marker is
committed after every site's import. Source files are removed only after that
point. Interruption and SQLite contention can be retried without overwriting
later revocations in an already committed site.

This layout is persistent-data generation 2 and migrates generation 1. It is
not writable by the predecessor. Install the updated launcher/supervisor before
the first layout-changing deployment so backup and recovery recognize nested
site databases. After activation, rolling the executable back to generation 1
is refused; restore the complete generation-1 database backup with its matching
binary when an administrative rollback is required.

Updater database backups include the global database and every content/control
database. The Analytics backup group includes analytics databases and history
archives. Published files and keys/certificates retain their own groups. Restore
the selected databases through the existing stopped-server update recovery
process. Restoring a control database restores its grants, narrowed scopes,
revocations, provider settings and administrator IP policies; it does not revive
in-memory bearer sessions or authorization codes. A content import is not a
security-database restore.

## Sources and validation

Protocol decisions use the [MCP authorization specification](https://modelcontextprotocol.io/specification/2026-07-28/basic/authorization),
[OpenAI authentication documentation](https://developers.openai.com/plugins/build/auth),
[official OpenAI egress documentation](https://developers.openai.com/api/docs/guides/ip-addresses),
and [Anthropic outbound addresses](https://platform.claude.com/docs/en/api/ip-addresses).
The OpenAI snapshot was fetched from `https://openai.com/chatgpt-connectors.json`;
its `creationTime` is included in the embedded document. OpenAI CA certificates
come from the official authentication documentation's linked PEM files.

Local acceptance uses the real Go MCP SDK, OAuth/PKCE browser approval handler,
and the production site subsystem router. Live ChatGPT/Claude provider ingress,
owner browser approval and end-to-end operations on the production hostname
require separate external acceptance after deployment.
