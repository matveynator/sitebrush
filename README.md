<p align="center">
  <a href="https://sitebrush.com/">
   <img width="300" alt="goopher sitebrush" src="https://github.com/user-attachments/assets/4e0ed480-6aa6-4e27-9474-4bc71822bef0" />
  </a>
</p>

<h1 align="center">SiteBrush</h1>

<p align="center">
  <strong>Keep the website. Retire WordPress.</strong><br>
  Turn an existing website into editable static HTML without rebuilding its design.
</p>

<p align="center">
  <a href="https://sitebrush.com/#trial-form"><strong>Try your website</strong></a> ·
  <a href="https://demo.sitebrush.com/">Live demo</a> ·
  <a href="#downloads">Download</a> ·
  <a href="#install-the-server-version">Install</a>
</p>

<p align="center">
  <a href="https://pkg.go.dev/github.com/matveynator/sitebrush/v2">
    <img src="https://pkg.go.dev/badge/github.com/matveynator/sitebrush/v2.svg" alt="Go Reference">
  </a>

  <a href="https://github.com/matveynator/sitebrush/actions/workflows/security.yml">
    <img src="https://github.com/matveynator/sitebrush/actions/workflows/security.yml/badge.svg?branch=main" alt="Security">
  </a>

  <a href="https://app.codecov.io/gh/matveynator/sitebrush">
    <img src="https://codecov.io/gh/matveynator/sitebrush/graph/badge.svg" alt="Code coverage">
  </a>

  <a href="https://github.com/matveynator/sitebrush/blob/main/LICENSE">
    <img src="https://img.shields.io/github/license/matveynator/sitebrush?color=brightgreen" alt="BSD-2-Clause License">
  </a>

  <a href="https://github.com/matveynator/sitebrush/actions/workflows/release.yml">
    <img src="https://github.com/matveynator/sitebrush/actions/workflows/release.yml/badge.svg?branch=main" alt="Stable Release">
  </a>
</p>

<p align="center">
  <img src="https://img.shields.io/badge/output-static_HTML-22c55e" alt="Static HTML">
  <img src="https://img.shields.io/badge/deployment-one_binary-22c55e" alt="One binary">
  <img src="https://img.shields.io/badge/editing-right--click_or_long--press-22c55e" alt="Browser editing">
</p>

---

## What is SiteBrush?

A lot of websites still run WordPress simply because somebody occasionally needs to change a phone number, image, paragraph, menu item, or address.

SiteBrush takes a different approach.

It imports the website you already have and preserves its:

* pages
* design
* CSS
* JavaScript
* images
* other referenced assets

Visitors then receive ordinary static files.

```text
HTML
CSS
JavaScript
images
```

The site owner can still edit the website directly in the browser.

**SiteBrush is not a WordPress clone. It is a WordPress backup and retirement tool.**

---

## Before and after

### Before

```text
WordPress
├── PHP
├── database
├── plugins
├── themes
├── public login
└── admin backend
```

### After SiteBrush

```text
Website
├── HTML
├── CSS
├── JavaScript
└── images
```

**Same website. Same design. Still editable.**

Nothing connected to the Internet is completely invulnerable, but a static public website exposes far fewer moving parts than a traditional dynamic CMS.

---

## Try it on your own website

You do not need to rebuild your site to see whether SiteBrush works for it.

**[Try SiteBrush with your own website](https://sitebrush.com/#trial-form)** 

SiteBrush creates a separate copy for testing. Your live website is not changed.

Or open the:

**[Live demo](https://demo.sitebrush.com/)**

---

## How it works

### 1. Import

Give SiteBrush an existing website.

It follows the pages and referenced resources and imports the files the site needs.

### 2. Edit

Double-click or double-tap an image in the visual editor to change its source,
frame dimensions, fit and crop position. Replacement uploads use the site's
normal file storage and keep the original file. Fill / Crop is the default;
Fit shows the whole image, and Stretch requires an explicit choice. Cancel or
Close discards the dialog draft; Save saves the whole page. Existing CSS sizing
is preserved unless you explicitly resize the frame, so automatic height can
change when replacing an image.

HTML mode provides only Cancel, Save and Back to Visual, including on narrow
phones. Save writes the HTML directly; Back applies it to the visual draft
without saving, and Cancel restores the draft from before entering HTML mode.
The source editor starts at the beginning and preserves untouched document
metadata during visual edits.

Open the website normally.

* **Desktop:** right-click an element
* **Phone or tablet:** long-press

Edit text, pictures, buttons, menus, page blocks, or HTML.

### 3. Publish

Prepare changes without disturbing the version visitors are currently seeing.

Publish when everything is ready.

Visitors continue receiving ordinary static files.

---

## Why SiteBrush?

SiteBrush is useful when a website still needs editing but no longer needs a large dynamic CMS behind every page request.

It can help you:

* preserve an existing design instead of rebuilding the website
* remove WordPress from the visitor-facing side
* remove the public PHP/database/plugin stack
* reduce ongoing maintenance
* edit content directly on the page
* prepare changes separately before publishing them
* run the server as one standalone binary
* import WordPress, Joomla, Wix, Readymag, custom sites, and ordinary static websites

---

## SiteBrush Templates

Static sites are simple, but repeated content can become annoying to maintain.

Imagine a website with **200 pages** that all contain the same address in the footer.

Mark that element as a SiteBrush template:

```html
<div class="SiteBrush-Template FooterAddress">
    123 Main Street, New York, NY 10001
</div>
```

SiteBrush finds matching elements on the other pages and assigns the same template to them.

From then on:

**edit once → update everywhere**

Templates are useful for:

* headers
* footers
* menus
* sidebars
* contact information
* tables
* shared `<style>` blocks
* other repeated elements

Changes are stored in revisions, so previous versions can be restored.

---

## Automatic Import

When SiteBrush imports a website, it follows references and automatically imports the files the site uses:

* CSS
* JavaScript
* images
* other referenced assets

That means you can take an existing site and start editing it without first rebuilding it around SiteBrush.

The result remains ordinary static HTML.

---

## Best suited for

SiteBrush works especially well for:

* old WordPress websites
* business websites
* landing pages
* portfolios
* agency client websites
* documentation
* knowledge bases
* mostly static websites that still need simple browser editing


SiteBrush is not trying to replace every CMS.

It is for websites that no longer need one.

---

# Downloads

The **server version is recommended** for a public website. Desktop builds are useful for local work with a graphical interface.

| Platform                                                                                             | Desktop application                                                                                                                                                                                                                                                                                                                                                                                                                    | Server binary                                                                                                                                                                           |
| ---------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| <img src="https://img.shields.io/badge/Linux-111827?logo=linux&logoColor=white" alt="Linux">         | [amd64 GTK 4.1](https://sitebrush.com/download/latest/desktop-app/sitebrush_linux_amd64_desktop_gtk41.zip) · [GTK 4.0](https://sitebrush.com/download/latest/desktop-app/sitebrush_linux_amd64_desktop_gtk40.zip)<br>[arm64 GTK 4.1](https://sitebrush.com/download/latest/desktop-app/sitebrush_linux_arm64_desktop_gtk41.zip) · [GTK 4.0](https://sitebrush.com/download/latest/desktop-app/sitebrush_linux_arm64_desktop_gtk40.zip) | [amd64](https://sitebrush.com/download/latest/server-app/sitebrush_linux_amd64) · [arm64](https://sitebrush.com/download/latest/server-app/sitebrush_linux_arm64)                       |
| <img src="https://img.shields.io/badge/macOS-111827?logo=apple&logoColor=white" alt="macOS">         | [Universal DMG](https://sitebrush.com/download/latest/desktop-app/sitebrush_darwin_universal_desktop.dmg)                                                                                                                                                                                                                                                                                                                              | [Intel amd64](https://sitebrush.com/download/latest/server-app/sitebrush_darwin_amd64) · [Apple Silicon arm64](https://sitebrush.com/download/latest/server-app/sitebrush_darwin_arm64) |
| <img src="https://img.shields.io/badge/Windows-0078D4?logo=windows11&logoColor=white" alt="Windows"> | [amd64 ZIP](https://sitebrush.com/download/latest/desktop-app/sitebrush_windows_amd64_desktop.exe.zip) · [arm64 ZIP](https://sitebrush.com/download/latest/desktop-app/sitebrush_windows_arm64_desktop.exe.zip)                                                                                                                                                                                                                        | [amd64 EXE](https://sitebrush.com/download/latest/server-app/sitebrush_windows_amd64.exe) · [arm64 EXE](https://sitebrush.com/download/latest/server-app/sitebrush_windows_arm64.exe)   |
| <img src="https://img.shields.io/badge/FreeBSD-AB2B28?logo=freebsd&logoColor=white" alt="FreeBSD">   | —                                                                                                                                                                                                                                                                                                                                                                                                                                      | [amd64](https://sitebrush.com/download/latest/server-app/sitebrush_freebsd_amd64) · [arm64](https://sitebrush.com/download/latest/server-app/sitebrush_freebsd_arm64)                   |
| <img src="https://img.shields.io/badge/OpenBSD-F2CA30?logo=openbsd&logoColor=black" alt="OpenBSD">   | —                                                                                                                                                                                                                                                                                                                                                                                                                                      | [amd64](https://sitebrush.com/download/latest/server-app/sitebrush_openbsd_amd64) · [arm64](https://sitebrush.com/download/latest/server-app/sitebrush_openbsd_arm64)                   |
| <img src="https://img.shields.io/badge/NetBSD-F0544C?logo=netbsd&logoColor=white" alt="NetBSD">      | —                                                                                                                                                                                                                                                                                                                                                                                                                                      | [amd64](https://sitebrush.com/download/latest/server-app/sitebrush_netbsd_amd64) · [arm64](https://sitebrush.com/download/latest/server-app/sitebrush_netbsd_arm64)                     |

Linux desktop builds use **GTK 4.1** for newer distributions and **GTK 4.0** for older LTS distributions.

[Server checksums](https://sitebrush.com/download/latest/server-app/MD5SUMS) · [Desktop checksums](https://sitebrush.com/download/latest/desktop-app/MD5SUMS) · [All latest builds](https://sitebrush.com/download/latest/)

# Install the server version

Each block below is ready to copy and paste. It automatically selects `amd64` or `arm64`, downloads SiteBrush, makes it executable, and installs the system service.

<details open>
<summary><img src="https://sitebrush.com/p/04b158d78c93b65c714bb6256da221a490a73a31d1159732fe66604b68a64799.png" alt="Linux" width="28" align="middle"> <strong>Linux — copy and paste into the terminal</strong></summary>

```sh
(
  set -eu

  case "$(uname -m)" in
    x86_64|amd64) ARCH="amd64" ;;
    aarch64|arm64) ARCH="arm64" ;;
    *) echo "Unsupported CPU architecture: $(uname -m)" >&2; exit 1 ;;
  esac

  sudo mkdir -p /usr/local/bin
  sudo curl -fL \
    "https://sitebrush.com/download/latest/server-app/sitebrush_linux_${ARCH}" \
    -o /usr/local/bin/sitebrush
  sudo chmod +x /usr/local/bin/sitebrush
  sudo /usr/local/bin/sitebrush -install
)
```

The installer detects the available Linux service manager and configures automatic startup.

</details>

<details>
<summary><img src="https://sitebrush.com/p/fbad588e1b8c94b6b80708bc9917706efe4e7b5757c09e0946831b90e3e75722.png" alt="macOS" width="28" align="middle"> <strong>macOS — Intel and Apple Silicon</strong></summary>

```sh
(
  set -eu

  case "$(uname -m)" in
    x86_64|amd64) ARCH="amd64" ;;
    arm64|aarch64) ARCH="arm64" ;;
    *) echo "Unsupported CPU architecture: $(uname -m)" >&2; exit 1 ;;
  esac

  sudo mkdir -p /usr/local/bin
  sudo curl -fL \
    "https://sitebrush.com/download/latest/server-app/sitebrush_darwin_${ARCH}" \
    -o /usr/local/bin/sitebrush
  sudo chmod +x /usr/local/bin/sitebrush
  sudo /usr/local/bin/sitebrush -install
)
```

The installer configures SiteBrush as a `launchd` service.

</details>

<details>
<summary><img src="https://sitebrush.com/p/66aab89d1af641ee0ae190f6b3ea4e09ba8adae71f979ad315609cd825209c45.png" alt="Windows" width="28" align="middle"> <strong>Windows — PowerShell as Administrator</strong></summary>

Open **PowerShell as Administrator**, then paste:

```powershell
$ErrorActionPreference = "Stop"

$Arch = if ($env:PROCESSOR_ARCHITECTURE -eq "AMD64") {
    "amd64"
} elseif ($env:PROCESSOR_ARCHITECTURE -eq "ARM64") {
    "arm64"
} else {
    throw "Unsupported CPU architecture: $env:PROCESSOR_ARCHITECTURE"
}

$Dir = Join-Path $env:ProgramFiles "sitebrush"
$Exe = Join-Path $Dir "sitebrush.exe"

New-Item -ItemType Directory -Force -Path $Dir | Out-Null

Invoke-WebRequest `
    -Uri "https://sitebrush.com/download/latest/server-app/sitebrush_windows_${Arch}.exe" `
    -OutFile $Exe

& $Exe -install
```

The installer creates and verifies the Windows service and enables automatic startup.

</details>

<details>
<summary><img src="https://sitebrush.com/p/c1ce8baa90a2ffd348069e69fa4fda93baa5c431020737bc1f5efd176d5e77e6.png" alt="FreeBSD" width="28" align="middle"> <strong>FreeBSD — amd64 and arm64</strong></summary>

```sh
(
  set -eu

  case "$(uname -m)" in
    x86_64|amd64) ARCH="amd64" ;;
    aarch64|arm64) ARCH="arm64" ;;
    *) echo "Unsupported CPU architecture: $(uname -m)" >&2; exit 1 ;;
  esac

  sudo mkdir -p /usr/local/bin
  sudo fetch \
    -o /usr/local/bin/sitebrush \
    "https://sitebrush.com/download/latest/server-app/sitebrush_freebsd_${ARCH}"
  sudo chmod +x /usr/local/bin/sitebrush
  sudo /usr/local/bin/sitebrush -install
)
```

</details>

<details>
<summary><img src="https://sitebrush.com/p/e3124d65b5feeb6af8ec8f882b167a35ab8e4cc791701789479d0523394267ba.png" alt="OpenBSD" width="28" align="middle"> <strong>OpenBSD — amd64 and arm64</strong></summary>

```sh
(
  set -eu

  case "$(uname -m)" in
    x86_64|amd64) ARCH="amd64" ;;
    aarch64|arm64) ARCH="arm64" ;;
    *) echo "Unsupported CPU architecture: $(uname -m)" >&2; exit 1 ;;
  esac

  doas mkdir -p /usr/local/bin
  doas ftp \
    -o /usr/local/bin/sitebrush \
    "https://sitebrush.com/download/latest/server-app/sitebrush_openbsd_${ARCH}"
  doas chmod +x /usr/local/bin/sitebrush
  doas /usr/local/bin/sitebrush -install
)
```

Run the commands as `root` when `doas` is not configured.

</details>

<details>
<summary><img src="https://sitebrush.com/p/c1ce8baa90a2ffd348069e69fa4fda93baa5c431020737bc1f5efd176d5e77e6.png" alt="NetBSD" width="28" align="middle"> <strong>NetBSD — amd64 and arm64</strong></summary>

```sh
(
  set -eu

  case "$(uname -m)" in
    x86_64|amd64) ARCH="amd64" ;;
    aarch64|arm64) ARCH="arm64" ;;
    *) echo "Unsupported CPU architecture: $(uname -m)" >&2; exit 1 ;;
  esac

  sudo mkdir -p /usr/local/bin
  sudo ftp \
    -o /usr/local/bin/sitebrush \
    "https://sitebrush.com/download/latest/server-app/sitebrush_netbsd_${ARCH}"
  sudo chmod +x /usr/local/bin/sitebrush
  sudo /usr/local/bin/sitebrush -install
)
```

</details>

# Uninstall

The `-uninstall` command removes the configured system service.

<details open>
<summary><strong>Linux, macOS, FreeBSD, and NetBSD</strong></summary>

```sh
sudo /usr/local/bin/sitebrush -uninstall
```

</details>

<details>
<summary><strong>OpenBSD</strong></summary>

```sh
doas /usr/local/bin/sitebrush -uninstall
```

</details>

<details>
<summary><strong>Windows — PowerShell as Administrator</strong></summary>

```powershell
& "$env:ProgramFiles\sitebrush\sitebrush.exe" -uninstall
```

</details>

---

# Start editing

After installation:

1. Point the website domain to the server.
2. Open the domain in a browser.
3. Use **right-click** on a computer or **long-press** on a phone.
4. Edit the content and save it.

```text
http://your-domain.example
```

---

## Visitor analytics

Analytics is one screen: the visitor map and daily square calendar appear first, followed by conversion, user paths, behaviour, traffic quality and retention. Year and day selection sit beside the visitor map. Technical request totals, browsers, operating systems and response times are not Analytics reports. Security keeps its independent observations and dashboard.

Stealth continues to hide administrator controls, but public HTML retains the visitor collector and its event endpoint. Configuration and replay remain subject to account, IP and CSRF checks. Each browser keeps at most four unacknowledged batches, bounded to 64 KiB and 30 minutes, and removes them only after the processing worker confirms receipt. Beacon delivery is retried through WebSocket on the next visit; duplicate view/sequence pairs do not increase counters. Reports are checkpointed approximately once a minute and appear on page refresh. A failed report read is shown as an error rather than zero traffic.

On the first authenticated Analytics visit, up to four initial goals are saved once from the published site structure. They use real authored links, downloads and form submissions; simple outcome goals allow direct landing traffic to convert without visiting Home. Navigation funnels use only actual published links, not URL folder nesting. Existing settings, including an explicitly empty list, are preserved. The constructor explains page paths, structural element keys, custom events, primary goals and form success. Use **Goals and funnels** to edit these defaults or add your own primary outcome and ordered steps. A visitor can complete a funnel across sessions within 90 days of entry. Reports select visitors by entry date and count a browser once per configuration version. The entry source stays attached when the visitor returns. Open windows show preliminary losses; departures after 30 inactive minutes are reported separately while those visitors remain eligible to return within the conversion window. Funnel insights use these observed departures rather than waiting 90 days; editing a rule creates a new version without rewriting previous outcomes. A random first-party browser identity expires after 90 days without activity. Different browsers and devices are not joined.

Standard observations cover page views, clicks, reached scroll depths and sections, downloads, external links, and form view/start/submit. Business outcomes can be reported with `SiteBrushAnalytics.event('purchase')`; the existing `SiteBrushAnalytics.action('download', '/download')` remains supported. Goals can match a published path, a structural element key, a named event, or a scroll threshold. `data-analytics-key="download"` gives an element an explicit key. No arbitrary business properties are accepted.

A form submit is not a confirmed success. Set `data-analytics-key="contact"` on the form and call `SiteBrushAnalytics.formResult('contact', true)` after successful processing (`false` for failure), or configure a success page on a form-success step. Field drop-off uses structural field identifiers only. SiteBrush never records input, textarea, password, form-message, file, token or payment-field contents. Dead and rage clicks are observations to investigate, not proof that an interface is broken. Heatmaps use an inert, sanitized preview of the published page and refuse overlays when the published page version differs from the observed version.

The user-path report overlays entrances, visitors, conversions and exits on the published site tree, using page titles from the editor’s catalog. Expanding a page shows observed next steps separately from its URL hierarchy. The catalog is limited to 1,000 pages and branches render only when expanded. Structure inspection reads at most 16 relevant published pages, 64 KiB per page and 512 KiB of accepted HTML per report; it retains links and structural action keys, never page bodies or field contents. For inspected pages, observed transitions without a published link are marked for investigation. Redirects, scripts or browser navigation can explain these transitions; uninspected pages are not falsely labeled.

Traffic reports connect acquisition evidence to primary-goal conversion. Available referrer URLs and UTM source/medium/campaign are retained; credentials and secret query parameters are removed; search queries are not inferred. Weekly retention uses first known browser visits and labels incomplete weeks. Clearing browser storage starts a new observed identity. Temporary identities cannot establish long-term retention.

The collector batches up to 20 events every ten seconds, with a 100-event local queue and a 16 KiB message ceiling. WebSocket is the normal transport; `sendBeacon` is the unload compatibility adapter. Both feed the same bounded worker and deduplication gate. There is no mousemove stream, raw event journal, video capture or continuous keystroke recording. Optional sampled session replay stores only bounded navigation, scroll, click/touch, focus, resize and error observations plus a sanitized published-page snapshot; field contents are never recorded. Only the mobile/tablet/desktop class is retained to explain conversion and page behaviour. User agents are inspected transiently to exclude automation; fingerprints, OS, browser, language, timezone, screen and processor profiles are not collected. The visitor map retains server-derived IP addresses with approximate GeoIP coordinates in bounded browser state; they expire after 90 inactive days and are never used as identity keys. IP addresses and visitor locations are stripped from long-lived daily archives. On first read, the storage worker removes obsolete field values, fingerprints and client profiles from development checkpoints while preserving aggregate counts. The map includes all retained visitors in the selected period, including source and landing page. Unknown locations remain in the visitor list. The daily square calendar retains unique visitor totals for approximately 13 months. Its darkest green represents the maximum daily visitor count in the selected year; every smaller count uses a lighter shade, and the legend gives the exact integer ranges, including years with only one or two visits. The visits map uses the canonical OSM tile URL, visible linked copyright attribution and an origin-only tile referrer. Normal browser caching applies; no tile proxy, prefetch or bulk download is used. If tiles fail, visitor details remain available in the list. Overload preserves admitted observations, rejects later work and marks the report incomplete; serving a page never waits for SQL or analytics persistence.

Hourly goal completion counts expire after 30 days; daily summaries and cohort totals survive for approximately 13 months. Browser identity state and bounded behaviour summaries expire after 90 inactive days; session contacts last only while active. Form contact labels are removed after seven days while bounded aggregate outcomes remain available for the 90-day report window. Existing technical checkpoints are removed when conversion state is saved. Historical request data is not reconstructed as conversion or paths.

Analytics is stored separately in `storage/analytics/site-<domain>/<domain>.analytics.db`, with one session channel and one SQL worker per site. Existing `analytics.db` files are renamed by that worker on first access. The analytics database, SQLite journals, archives and temporary files count toward the site’s disk quota and appear as a separate storage component. A write reserves space through the site quota worker, then reconciles the actual file size. Quota checks for page edits include live analytics reservations. Collection marks its growth as blocked when the quota cannot admit a write.

Sampled session replay and page-change impact are included in this release. Replay defaults to off; when enabled, it samples 5% of sessions (configurable from 1% to 10%), keeps at most 24 sessions with 256 actions each and expires them after seven days. Playback uses sanitized authored snapshots and does not reconstruct arbitrary dynamic DOM changes. Sessions with dead/rage clicks, failed forms, UI errors or stopped funnels are prioritized in the report. Page-change impact uses actual publication timestamps, equal seven-day before/after visit windows and seven-day conversion follow-up. Small samples, overlapping changes and unfinished windows are labeled; historical changes predating this feature are not reconstructed. This is a before/after observation, not an A/B test or a claim of causation.

### Redesign completion — 2026-10-14

The requested minimal Analytics feature scope is implemented. The previous estimates mixed implementation with pending verification; the listed gaps now have concrete implementation or targeted evidence. No broader BI, full dynamic-DOM replay, Search Console integration or A/B-testing platform is implied.

| Requirement | Implementation / evidence |
| --- | --- |
| 1–3. Minimal reports, one screen, conversion/goals | Map/calendar preserved; technical reports removed; standard/custom goals and editable initial goals. |
| 4. Graphical user paths | Published site tree, actual journeys, conversion/exits and explicit unlinked-transition labels. |
| 5–6. Behaviour and sampled replay | Desktop/mobile browser regressions verify inert click heatmaps, scroll reports, event playback and exclusion of field contents. |
| 7–10. Sources, search, forms, retention | Actual available referrer URLs, source conversion, structural form outcomes and weekly first-visit cohorts. |
| 11. Publication impact | Publication-channel regression and browser rendering of a changed CTA, equal before/after windows and sample warnings. |
| 12. Insights | Deterministic thresholds and minimum sample sizes; maximum three findings. |
| 13. Performance | Bounded admission and local batches; the existing parallel static-serving benchmark was run three times for disabled, saturated, working and stalled analytics. |
| 14–15. Storage and privacy | Separate per-site SQL/channel workers, quota reservations, bounded aggregation/expiry, seven-day replay, legacy field/profile cleanup. |
| 16–17. Compact and actionable UI | 1280×800 and 320×568 browser regressions, native disclosures, explained goals and color ranges, all 16 translation catalogs. |

On Apple M1, the median of three 300 ms warm-cache runs was 1.303 ms/op with analytics disabled and 1.308 ms/op with saturated queues (+0.37%). The p95 samples were 12.674 ms and 12.735 ms respectively; p99 was 13.460 ms and 14.001 ms. Working and stalled analytics had median times of 1.280 ms/op and 1.298 ms/op. These are short synthetic parallel measurements, not a production capacity guarantee. Reproduce with:

```sh
CGO_ENABLED=0 go test . -run '^$' -bench '^BenchmarkAnalyticsStatic/warm/(disabled|saturated|worker|stalled)$' -benchmem -benchtime=300ms -count=3
```

Session playback intentionally uses sanitized authored snapshots rather than arbitrary dynamic DOM capture. Impact results need real observation time; changes predating the feature are not reconstructed. Unknown GeoIP locations stay in the list. Identity-backed map detail is limited by the documented retention and memory budgets, never an arbitrary 200-visitor cap. These are explicit product boundaries, not guessed or fabricated results.

A central peer using the older signed snapshot schema may reject `analytics_bytes` after decoding and reserializing the request. Snapshot reporting and mail adapters retry once with that additive field omitted and a fresh valid signature. Total `used_bytes` still includes analytics, and local accounting keeps its separate component. Updated peers receive the full split immediately; older peers need an upgrade to display the separate analytics component. Signature verification and tamper rejection remain mandatory on both attempts.

## Security analytics

The local journal combines active local/manual blocks with observed incidents that have not caused a block. Each IP appears once with its current status and retained request evidence. Global entries remain in a separate read-only registry. Both journals search before pagination, show 25 rows per page, and return searches and page navigation to the corresponding list.

Country activity shows events and unique IPs together, ten countries per page, for the selected reporting period or calendar day. Period audiences deduplicate addresses across days. Address evidence is bounded and retained for up to 30 days; historical or incomplete audiences display No data rather than inferred counts.

Global reputation sync controls both exchange and enforcement. Enabling it pulls confirmed entries immediately and refreshes every ten minutes. Disabling it leaves cached global entries visible but inactive; local/manual protection and allowlist precedence remain unchanged. The registry shows the last successful pull and refresh errors. A failed refresh does not remove unexpired cached protection.

## Project history

SiteBrush v2 is written in Go.

The original PHP implementation was developed publicly from 2021 and remains available in the [SiteBrush v1 repository](https://github.com/matveynator/sitebrush-v1) as the historical predecessor of the current project.

---

<p align="center">
  <strong>Keep the website. Keep the design. Keep simple browser editing.<br>
  Leave the database, plugin stack, public admin backend, and constant maintenance behind.</strong>
</p>

<p align="center">
  <a href="https://sitebrush.com/#trial-form"><strong>Try SiteBrush on your website</strong></a>
  ·
  <a href="https://demo.sitebrush.com/"><strong>Live demo</strong></a>
</p>

### Account confirmation and secure registration

Every sign-in method uses the same persistent, HttpOnly session cookie and a server-enforced 15-day idle period. Foreground navigation and mutations renew both; assets, WebSocket traffic and background fetches do not. Revoked and expired sessions cannot be renewed. Existing valid sessions receive one migration grace period. Passkeys are offered first when a key is registered for the site; the password form is opened through **Sign in with a password**, or automatically when WebAuthn is unavailable. The browser, rather than a server key count, determines which passkey is available on the device.

The visual editor uses the visible viewport, including mobile keyboard changes and safe-area insets. Code scrolls inside its workspace while the toolbar remains available. Chromium and WebKit regressions can be selected with `SITEBRUSH_BROWSER_ENGINE`; use the existing `SITEBRUSH_EDITOR_BROWSER_TEST=1` or `SITEBRUSH_ANALYTICS_BROWSER_TEST=1` Go test switches.

Registration starts with an email address and a one-time email code; the initial
form never accepts a password. Public HTTP confirmation waits for a trusted HTTPS
certificate, checking every ten seconds. Only then does the confirmation form
accept a new password and create the account/session. Opening an email link alone
does not change an account. Direct loopback connections to localhost can set a password and sign in without
email codes. Both a local host and a loopback transport peer are required; proxy
headers disable this exception. Local sessions appear as 127.0.0.1 / localhost.
Public test-drive links start on HTTP; no self-signed certificate is required.

New browser sessions require both the password and an email code, even from a
previously recorded IP. Existing sessions survive VPN changes and travel, recording
the new address. The profile lists addresses observed within 90 days and lets the
owner remove an address and terminate sessions currently using it. Upgrading to
this account schema invalidates legacy sessions once. Recovery and email changes
revoke existing sessions and address history.

Codes expire after 15 minutes, allow five attempts, and resending replaces older
codes. Requests have a 60-second cooldown and a maximum of three per 15-minute
account/IP window. Account emails include the domain, purpose, original request
IP/time, expiry, and a form link. Code fields support `autocomplete="one-time-code"`;
autofill availability depends on the device/mail client. Mail links to code forms prefill the code through a URL fragment, which is removed
from browser history and is not sent in HTTP requests. Login links submit their one-time code automatically. Account changes still
require explicit confirmation. Email codes remain selectable text.

`SITEBRUSH_TRUSTED_PROXIES` accepts comma-separated IP addresses or CIDRs for
account IP attribution. By default only loopback proxies are trusted. Forwarded
addresses from other peers are ignored; an unavailable client IP is never stored
as a trusted address. Configure this list to match the actual reverse proxies.
