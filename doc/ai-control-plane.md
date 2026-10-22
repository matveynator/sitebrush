# AI control plane

SiteBrush enables known published AI networks by default. A public request with `mcp`,
`oauth`, `ai`, `ai_token` or `ai_session` selectors is processed by the ordinary
site router when its source has not been admitted. The router does not add AI
discovery links, OAuth challenges, CORS responses or protocol errors for that
visitor. This also applies to the authorization-server compatibility route.

## Connecting a provider

Connect `https://example.com/?mcp` in the
provider's MCP client. OAuth browser authorization uses the existing account,
session, CSRF and administrator IP policies. It does not require a provider IP.
Machine requests require a published provider network or a site administrator
IP, and then OAuth or capability
authorization. Updates and rollbacks remain drafts; publication is separate and
version checks remain required.

Provider selection is site local. A missing policy defaults to all listed
providers enabled; saved exclusions remain effective, and newly added providers
inherit the default. A corrupt or unavailable policy never grants network access.
Administrator IPs retain admission independently of provider selections. Network
admission grants discovery, not authority to read private content or edit a site.

The browser AI editor uses the existing administrator session at `/?ai`; it
does not depend on external provider settings or MCP credentials. A valid browser
session follows the same lifetime and address rules as the rest of the editor.
Profile includes a direct link. Opening `?mcp` without a bearer token from an
admitted network returns the OAuth challenge (401); that is the connection's
authorization step, not a missing endpoint.

Embedded verified snapshots bootstrap the global SQL cache and a channel-owned
range participant. A separate worker refreshes official HTTPS sources daily,
validates every prefix and commits only complete valid documents. Invalid, older
or unavailable downloads retain the last valid snapshot. Admission reads parsed
prefixes from its own participant and does not wait for provider downloads or
reparse every list per HTTP request. Custom ranges cannot be `/0`.

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
