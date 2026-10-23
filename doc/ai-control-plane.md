# AI control plane

SiteBrush enables known published AI networks by default. A public request with `mcp`,
`oauth`, `ai`, `ai_token` or `ai_session` selectors is processed by the ordinary
site router when its source has not been admitted. The router does not add AI
discovery links, OAuth challenges, CORS responses or protocol errors for that
visitor. This also applies to the authorization-server compatibility route.

The exact enabled public demo domain is an explicit exception: humans and AI
clients have the same immediate public editing permission from every IP. Its
`?mcp` endpoint requires no OAuth, bearer token, cookie or consent screen. This
exception never follows a `demo.*` naming pattern or a domain alias. The demo
has all four existing scopes, saves drafts and permits publication within the
requested task. During a reset it rejects new AI work; replacing its content
revokes old grants and closes job/session channels. Nothing private belongs on
a shared, automatically reset demo. OAuth-only clients can still register and
complete PKCE automatically, without a login or consent screen.

## Connecting a provider

Connect `https://example.com/?mcp` in the
provider's MCP client. OAuth browser authorization uses the existing account,
session, CSRF and administrator IP policies. It does not require a provider IP.
Machine requests require a published provider network or a site administrator
IP, and then OAuth or capability
authorization. Updates and rollbacks remain drafts; publication is separate and
version checks remain required.

Deleting the last active revision queues deletion of the entire page; a later
publication removes its public page and static file. The MCP tool descriptor
and working guide disclose this fallback and require reporting it to the owner.

Page and site imports download concurrently, then commit quota, asset metadata and pages
through the site's content participant in one transaction. Asset writes never
reconcile away page reservations between these steps. A rejected or canceled
batch restores replaced files and removes newly written files before completing.

Provider selection is site local. A missing policy defaults to all listed
providers enabled; saved exclusions remain effective, and newly added providers
inherit the default. A corrupt or unavailable policy never grants network access.
Administrator IPs retain admission independently of provider selections. Network
admission grants discovery, not authority to read private content or edit a site.

The browser AI editor uses the existing administrator session at `/?ai`; it
does not depend on external provider settings or MCP credentials. A valid browser
session follows the same lifetime and address rules as the rest of the editor.
Profile shows the current IP, administrator admission and the shared administrator
IP list. It links to the editor and MCP instructions. A browser opening `?mcp`
from an admitted network gets connection instructions; an API client without a
bearer token gets the OAuth challenge (401).

Embedded verified snapshots bootstrap the global SQL cache and a channel-owned
range participant. A separate worker refreshes official HTTPS sources daily,
validates every prefix and commits only complete valid documents. Invalid, older
or unavailable downloads retain the last valid snapshot. Admission reads parsed
prefixes from its own participant and does not wait for provider downloads or
reparse every list per HTTP request. Custom ranges cannot be `/0`.

Requests without control selectors bypass AI admission SQL and provider-network
jobs, including tracking and asset cache queries. The path-based OAuth discovery
endpoint still requires admission.
Admitted administrators do not need a provider-network check. Refresh workers
parse new snapshots before handing them to the range participant, so parsing
does not stall its admission queue.

`BenchmarkAIIngressHTTP` measures this middleware independently of page delivery.
On Apple M1 with pure-Go SQLite, a 200 ms local run measured 0.40 microseconds /
12 allocations for `/` and static assets, and 0.79 microseconds / 17 allocations
for `?utm_source=example`. Control requests still perform authorization work.
These figures measure middleware overhead, not production page timings.

The source catalog and snapshots are in `pkg/aiingress`. Sources verified on
2026-10-22:

| Provider | Published networks and primary source |
| --- | --- |
| OpenAI | [Connectors and Codex cloud](https://developers.openai.com/api/docs/guides/ip-addresses), plus [ChatGPT-User, SearchBot, GPTBot and AdsBot](https://developers.openai.com/api/docs/bots) |
| Anthropic | [Outbound MCP](https://platform.claude.com/docs/en/api/ip-addresses): `160.79.104.0/21`; inbound API ranges are excluded |
| Perplexity | [PerplexityBot and Perplexity-User](https://docs.perplexity.ai/docs/resources/perplexity-crawlers) |
| Google | [Google-owned fetchers and Google-Agent](https://developers.google.com/crawling/docs/crawlers-fetchers/google-user-triggered-fetchers); customer-owned GCP fetcher networks are excluded |
| Microsoft | [Official Bingbot JSON](https://www.bing.com/toolbox/bingbot.json) |
| Apple | [Applebot](https://support.apple.com/en-us/119829) for Siri, search and Apple Intelligence |
| Amazon | [Amazonbot, Amzn-SearchBot and Amzn-User](https://developer.amazon.com/amazonbot) for Alexa, search and AI |
| xAI / X / SpaceX | Explicitly requested corporate/subscriber network admission, described below |

Crawler and user-fetcher lists establish network admission; their publication
does not assert that the provider offers a Remote MCP client or that all its
products use those same source addresses. No stable official outbound list was
confirmed for Mistral, DeepSeek, Qwen, Meta, Groq, Cohere or You.com; these clients
can connect from a site administrator IP or an explicitly configured custom CIDR.
API host DNS records and general AWS/Azure/GCP ranges are not provider egress lists.

For xAI, X/Twitter and SpaceX, the explicitly requested policy admits the entire
registered networks, including Starlink subscriber addresses. It is enabled by
default and does not use a separate manual xAI CIDR field. The supplied
`69.12.56.0/21` and `31.207.0.0/24` are included. The registered ASN inventory is
AS63179, AS400297, AS13414, AS35995, AS54888, AS27277, AS14593, AS53348 and AS397763,
verified through ARIN organizations [TWITT](https://whois.arin.net/rest/org/TWITT/asns),
[XAI](https://whois.arin.net/rest/org/XAI/asns),
[SETC-3](https://whois.arin.net/rest/org/SETC-3/asns) and
[SS-2296](https://whois.arin.net/rest/org/SS-2296/asns).
[RIPEstat announced-prefixes](https://stat.ripe.net/docs/data-api/api-endpoints/announced-prefixes)
supplies current IPv4 and IPv6 origin prefixes for these ASNs. Routes withdrawn
earlier in the query interval are excluded; ASNs with no announced routes grant
no additional addresses until their next successful refresh.

Native TLS requests a client certificate without requiring one from browsers.
If OpenAI presents one, SiteBrush validates its official CA chain, client-auth
usage and exact `mtls.prod.connectors.openai.com` SAN. No profile setting or legacy
policy requires a client certificate. A TLS-terminating proxy cannot assert a
certificate through an HTTP header. Client addresses use the existing
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

The public browser Analytics transport also uses a query selector:
`?request=analytics` for WebSocket upgrades and POST beacons, independent of
pathname. Ordinary GET navigation with that query stays in the public router.
The former `/_sitebrush/analytics` pathname has no transport handler.

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

A failed security-state reload also closes active AI session channels and drops
temporary pairing state. Restoring valid durable grants permits new
authorization; it never resurrects the old access sessions.

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

## Browser editor and MCP connection page

Open `/?ai` for the existing voice/text AI editor. Allowed IPs without an account
session sign in and return to the editor. Ollama runs beside the SiteBrush process
on `127.0.0.1:11434`, including when the browser visits a remotely hosted site.

Browser navigation to `/?mcp` (`Accept: text/html`, without MCP credentials or
protocol headers) shows Claude/Claude Code setup instructions to an admitted
owner. Instructions do not require sign-in; approval and connection management
do. Protocol requests retain the OAuth challenge and Streamable HTTP behavior.
Admission binds the request's domain before reading its site control database.

Additional MCP tools cover publication state, page settings, draft page deletion,
revision deactivation, password protection, freeze and asynchronous page/site
import. They share existing scopes and operation pipelines. Page and revision
mutations remain drafts until publication. Password rules apply immediately to
subtrees; page-only grants cannot change them. Import jobs belong to one site
and grant and stop on cancellation/revocation. The content owner persists draft
deletion markers; the crawler persists draft mode across queue recovery.

The additive schema steps extend the existing migration history without changing
previous step IDs, order or revisions and without republishing modern drafts.
Real Claude/Claude Code acceptance on the deployed HTTPS site remains a separate
check from local SDK and browser regression tests.


## Assistant feature guide and protected files

`sitebrush_get_access` reports the actual site, grants, demo status and concise
workflow. `sitebrush_get_guide(section)` returns sections of the same canonical
HTTP guide: overview, editing, templates, files, publishing, revisions, analytics,
security and imports. Reading static help requires no extra content scope.

`set_revision_active` takes an explicit boolean and the opaque current version.
Version guards include revision flags. Restoring active source uses the same
transaction and shared-template propagation as page saves. Turning off the last
active revision queues page deletion; reactivation cancels that draft deletion.

`get_security_summary` returns a separate allowlist DTO under analytics scope.
It bounds requests, errors and suspicious events to the requested 1/7/30/90-day
period and includes coverage. No raw incident, address, user-agent, path/query or
form data is serialized. Complete blocked-request totals are currently unavailable.

`get_file_settings` and `set_file_access` expose safe file access metadata.
Site-wide write can bind a file to `protected_page_path` using an existing
section password, or explicitly make it public. `upload_file` accepts the same
path and optional `image_full` for prepared images. Preview/full files receive
rules together. A new private upload is stored in a fail-closed namespace before
the control transaction commits its ACL; failure removes files and quota.
Removing a referenced section rule denies the file until explicit reconfiguration.
Restored ACLs are committed before archive files become reachable, and section
paths follow the copy destination. Backups never need a plaintext password.

Manual acceptance after deployment is listed in [MCP demo checks](mcp-demo-checks.md).
