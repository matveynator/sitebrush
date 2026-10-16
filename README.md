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
normal file storage after preparing a cropped, resized preview for the current
frame, with a 2× pixel budget for sharp displays. Fill / Crop is the default;
External image URLs use the validated server downloader and do not need browser
CORS permission. Crop position accepts percentages from 0% to 100% and the
left/center/right/top/bottom keywords; unsupported positions are rejected before Save.
Fit shows the whole image, and Stretch requires an explicit choice. GIF, WebP and APNG
animation and SVG vector content are preserved. Existing gallery links receive a
separate full-resolution version; an unlinked image stores only its preview.
Cancel or Close discards the dialog draft; Save saves the whole page. Existing
CSS sizing and frame proportions are preserved unless you resize the frame.

HTML mode provides Undo, Redo, Cancel, Save and Back to Visual in one compact row,
including on narrow phones. Undo/Redo history lasts for the open editor session,
including switches between visual and HTML modes. Save writes the HTML directly; Back applies it to the visual draft
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

### Site creation limits and demo refresh

Hosting settings contain independent maximum site sizes for public Test Drive,
self-registration and the demo, each defaulting to **1 GB** (1024³ bytes).
Registration and Test Drive use the smaller of that maximum and the tariff quota.
Changing these defaults preserves existing site quotas; the configured demo uses
its own current maximum. Usage includes pages, publication copies, revisions and
files.

A new hostname, or a deleted anonymous trial hostname, offers first-administrator
registration when self-registration is enabled, and a request form when it is
closed. Registration verifies DNS and email before creating the site database.
A still-valid certificate is reused. A missing database for a site still present
in the registration registry remains an availability error.

Demo imports and snapshot restores are prepared in separate temporary storage
and checked against the demo quota before replacing live content. Successful
refreshes remove obsolete pages, publication files and revisions, so the tree
matches the current template. Failed preparation preserves the working demo.

Account cards share one grid. Sessions and administrator IPs appear together;
sessions and administrator IPs have independent pages of three records, while
other long lists show five records per page. Administrators can add any valid IP
or subnet without a prior session from that address. Explicit IP and subnet
allowlist entries are permanently allowed until removed. AI connection instructions
are always available in the account; access lists appear when grants or sessions exist.

### AI access to a whole site

Give an AI the site address. SiteBrush advertises `/?ai=manifest` through a public
`Link` response header. Discovery, documentation (`/?ai=documentation`) and
OpenAPI (`/?ai=openapi`) require no credentials. This provider-neutral discovery
does not require a well-known path. The AI needs tools that can inspect HTTP
responses and perform the documented requests; this does not automatically add
networking tools to every chatbot. The manifest also advertises `mcp_url` for
clients with native Remote MCP support.

#### ChatGPT and Remote MCP

The same SiteBrush instance serves Streamable HTTP at
`https://example.com/_sitebrush/mcp`. There is no separate daemon. The adapter
exports the existing content and Analytics operations, plus `sitebrush_get_access`
with the connected site, actual permissions, expiry and suggested next actions.
OAuth discovery uses protected-resource and authorization-server metadata at the
standard `/.well-known/` paths. Public endpoints require HTTPS.

Connect the site's MCP endpoint once in ChatGPT's **Plugins → Add custom MCP
server**, select OAuth authentication and install the resulting connection.
Then select it in the conversation and ask, for example, “Посмотри мой сайт
https://example.com и помоги мне его улучшить”. ChatGPT opens the normal SiteBrush
login/approval screen. After **Allow**, the client handles the credential exchange
and refresh automatically. The owner never copies tokens or runs API commands.
Workspace policies and client support can affect connection availability. See
the [official connection instructions](https://developers.openai.com/plugins/deploy/connect-chatgpt).

The distributable [SiteBrush skill package](integrations/chatgpt/plugin.json)
contains the [working skill](integrations/chatgpt/skills/sitebrush/SKILL.md) and its
tool reference. Import `integrations/chatgpt` as a portable plugin using a
supported personal or workspace plugin installation. For a local desktop
installation, the repository marketplace exposes this package as `sitebrush`.
Follow the [official package installation instructions](https://developers.openai.com/plugins/build/plugins).
The skill works with the per-site connection; it cannot silently install a new
MCP server merely because a URL appeared in chat. No site credentials belong in
the package, skill or conversation.

MCP creates, updates and rollbacks always save drafts, even with `publish` access.
`sitebrush_publish` publishes **all pending changes**, including drafts made by
other editors. Updates and rollback require the last read `expected_version`;
conflicts require a fresh read. Attachments use ChatGPT's native file input or
base64 bytes and the existing immutable uploader, followed by a page update.
Analytics remain bounded aggregates with explicit coverage/no-data information.

Authorization uses existing pairing, capabilities and owner controls. Public
clients use exact registered redirect URLs and PKCE S256. Access sessions last
15 minutes; rotated refresh tokens expire after 30 days without use. Only token
hashes and capability references are persisted in the capability owner's private
OAuth binding file. Unused client registrations expire after 24 hours. Restrict
or revoke access in the normal SiteBrush account card; the same session channels
cancel active work. This does not change the legacy capability API below.

Before declaring a deployment ready, test it in an actual external ChatGPT
conversation: connect/Allow, read pages and Analytics, edit a draft, publish,
verify the public page, restore a revision, publish and verify again, then revoke
access. Automated SDK tests cover this path locally; they do not establish that
a particular ChatGPT account or deployed server has passed it.

#### Provider-neutral capability API

The [AI working guide](pkg/aicapability/guide.md) is the source served verbatim
at `/?ai=documentation` and included in OpenAPI. It explains the relationship
between saved source, revisions and published static HTML, exact template
identity and propagation rules, complete-document updates, image uploads,
version conflicts, recovery and evidence-based analytics feedback.

The AI calls `POST /?ai=pair`, shows the returned `verification_url`, and polls
`POST /?ai=token` with its private `device_code`. The owner signs in and approves
once. The ten-minute request is consumed once; the browser never receives the
capability token. Approval defaults to **read, write, publish, analytics** for
this site, with advanced opt-outs. Public credentials require HTTPS; direct
loopback development uses the existing localhost exception.

The AI exchanges its persistent capability at `/?ai=exchange` for a fifteen-minute
session. Requests carry `X-SiteBrush-Capability` and `Authorization: Bearer` headers,
with no secret in their URL. The ordinary menu's **AI · SiteBrush** entry opens the
account card. Revocation prevents new exchanges and closes session channels,
canceling queued and running work through the existing worker and SQL adapters.
Restriction also cancels work admitted under broader permissions. Changes already
committed remain revisions and can be restored normally. Existing current-page
links remain supported.

Content actions are `?ai=pages`, `page`, `files`, `file`, `revisions`, `publish`
and `rollback`. Page reads include a version. Updates require `expected_version`;
rollback takes both `revision_id` and `expected_version`, so a newer edit cannot
be silently overwritten. A conflict returns HTTP 409. Site-wide saves use the
same template normalization, propagation, quota, revision and publication path
as the normal editor. Without `publish`, writes and rollbacks remain drafts.
Page-limited grants cannot propagate template edits to neighbouring pages.

Uploads are immutable, content-addressed files. Optional image dimensions use the
normal editor image preparation pipeline. Change an image using **upload_file →
update_page**, reusing the returned `/p/` URL; there is no replace-image action.
**Edit as text** (`?text`) and **Edit with voice** (`?ai`) share the same page
storage, templates and revisions; external agents use `read_page → update_page`.
Site copying/import, password protection, revision deactivation and freezing
remain ordinary menu actions, described as such in the AI documentation.

`?ai=get_site_report&days=30` gives bounded, read-only aggregates from the existing
Analytics snapshots. Focused actions cover summary, funnels, paths, page behaviour,
forms, sources, page impacts and insights. Periods are 1, 7, 30 and 90 days;
`limit` (at most 50) and `offset` paginate sections. Reports carry collection status,
period boundaries and denominators. Audience aggregates count active sessions,
returning visitors and page returns across distinct sessions, including returns
on the same UTC day. Tabs sharing a browser session are joined; 30 minutes of
inactivity starts another session. Form stages include per-device user counts,
so mobile and desktop abandonment can be compared. These are observations, not
proof of buying intent. `audience_since` and `audience_incomplete` identify the
coverage of new measurements. Historical sessions are not reconstructed, and
old form attempts retain an unknown device. Checkpoints preserve the new bounded
observations in the existing Analytics worker, with the same 90-day retention.

No visitor IPs, raw sessions, replay or form values are exported. DOM identifiers
and campaigns are hashed; referrers contain origins only. Missing measurements
are unavailable, not measured zero. Search-engine queries and internal-search
terms are unavailable. Insights include supporting aggregates; publication
comparisons include their windows, small-sample and overlapping-change flags.
The protocol instructs agents to ground advice in facts and treat explanations
of causes or unfulfilled demand as hypotheses. Final language is generated by
the connected AI, not by an additional SiteBrush statistics engine.

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

# Built-in server updates

Server owners can open **SiteBrush updates** in the server menu, check the installed and available versions, and confirm an update. The email button opens this page and requires ordinary administrator authentication; opening its URL never installs a release. Site administrators cannot change the server version.

For a manual update over SSH or a local administrative terminal:

```sh
sitebrush -upgrade
sitebrush -upgrade -path /var/lib/sitebrush
```

Run the command as the installation's process user, with access to its private update directory and binaries. The command is the confirmation; it prints progress and exits with status `0` when updated or already current. Failure returns a nonzero status, including when automatic recovery succeeds. Installed service settings are reused unless explicitly overridden. A stopped installation is returned to its stopped state after verification. Desktop and container installations use their existing distribution mechanism.

The update page shows the running HTTP server version separately from the version recorded by the updater. `sitebrush --version`, `sitebrush -version` and `sitebrush -v` always print the compiled version of the executable being invoked, even when the installed file has been replaced and an older server is still running. Replacing the executable does not switch the server version selected in the update journal.

The operation card includes the source and target versions, a progress bar, completed steps, start time, elapsed time and current-step duration. The overall percentage represents completed steps, with measured byte/file progress inside supported steps. Download and backup estimates use observed transfer speed; steps without reliable measurements display “Calculating” instead of an invented completion time. The final 100% requires a completed operation and a matching running server version. The last operation's timestamped journal is stored in the private update state, capped at 256 KiB, and can be copied or downloaded. CLI `-upgrade` reads the same event sequence, so short steps remain visible between status requests.

Files and settings are first copied while the HTTP server remains available. After stopping the server and all writers, SiteBrush reconciles changed, replaced, added and deleted files, then copies the selected databases and performs migrations. This reduces downtime; the final database copy and migration still require a maintenance interval. An already open page preserves its last confirmed progress, continues its elapsed timer and reconnects automatically; no new progress is claimed while disconnected. Once the server returns, the page restores the complete retained journal and result. Closing the page does not cancel the supervisor's operation. Announcement contact failures on TCP 9877 appear as separate warnings and do not disable release verification or downloading.

### Bootstrapping an older updater

An older running supervisor performs its own backup checks; downloading a new candidate cannot repair that already running code. For example, supervisors that reject `download/latest` or size the backup against the entire installation need the corrected supervisor installed before retrying. Preserve the complete installation directory, including `.sitebrush-update`, its journal, identities and backups.

For an existing Linux systemd installation, first obtain a **signed official server build containing the fix** and verify it against its signed release metadata. Stop `sitebrush.service`, install the verified executable at the service's existing executable path using a temporary file and atomic rename, then start the service. This starts the corrected supervisor while retaining the selected server and its recovery references. Run `sitebrush --upgrade -path /var/lib/sitebrush` against that running supervisor to install the signed current release; use the actual configured installation path if different. Check the final result and the running-server version in the panel. Do not edit the journal's version number or remove `download/latest` to bypass backup validation.

For local publication, `scripts/crosscompile` automatically loads the existing signing key from `os.UserConfigDir()/sitebrush/release-ed25519.key` when publishing a complete server matrix to sitebrush.com or sitebrush.ru. On macOS this is `~/Library/Application Support/sitebrush/release-ed25519.key`; an explicit `-release-key` path or `SITEBRUSH_RELEASE_PRIVATE_KEY` can supply the same trusted key. Publish the generated `update.json` and `SHA256SUMS` together with the server artifacts. A manually copied executable alone is not a signed release. The private signing key stays on the build machine; installations only require the embedded public verification key.

Source builds (`go run sitebrush.go`, version `dev`) always run the newly compiled source, without selecting a cached executable from an earlier updater journal. They retain the installation process-session guard, leave existing update artifacts untouched, and disable in-place upgrades and release announcements. Use a numbered server build for those features. Supervisor NetChan protocol types are frozen independently of JSON journal and lifecycle-result fields, so additive domain fields do not break communication between releases. The signed manifest now requires protocol 2; prototype launchers using the earlier typed-state contact cannot install it automatically and need the normal first-install procedure. The public release-download channel keeps its independent protocol 1.

The launcher remains at the installed executable path. Its supervisor runs the selected server binary from the private `.sitebrush-update` directory under `-path`. CLI and panel operations share one authenticated local NetChan owner. Downloads use the pinned official NetChan mirror at `sitebrush.com:9878`, with the official HTTPS download directories and `matveynator/sitebrush` GitHub assets as fallbacks. Every transport requires the same signed release metadata, exact platform, size and SHA-256 checked against the embedded Ed25519 public key. Existing `MD5SUMS` files are retained for download compatibility and are not used to authorize an update.

Before each update or rollback, select four independent backup groups: **panel and site databases**, **analytics**, **website files**, and **settings, keys and certificates**. All groups are selected by default and the choice applies to that operation only. Website files include uploaded files, published HTML and chroot content. Settings include TLS and service keys, certificate caches, security state and installation configuration. The updater directory, old backups, generated site packs and logs are excluded. The configured control database and its sibling site databases, including WAL/SHM/rollback-journal companions, are accounted separately from analytics databases. Database paths must remain inside the installation and cannot traverse symbolic links.

The same selection is available in the CLI:

```sh
sitebrush -upgrade -upgrade-backup=all
sitebrush -upgrade -upgrade-backup=databases,analytics,settings
sitebrush -upgrade -upgrade-backup=none
```

An explicit `none` creates no recovery snapshot. Excluded groups are never restored or removed by snapshot recovery. If migrations changed unbacked persistent formats, returning to the old binary requires the release compatibility contract and a successful read-only preflight of the actual databases. Otherwise SiteBrush stays stopped and reports that an external backup or candidate repair is required. Existing full-tree and database-only snapshots retain their original restore rules; selected-group snapshots use a separate marker that old restorers reject.

The candidate starts with public requests paused and outbound background jobs held until database, migration, HTTP and process checks succeed for 60 seconds. Cancellation of an operation has its own channel and does not cancel the previous server's recovery health checks. Stopping the whole service persists an interrupted recovery boundary for the next exclusive startup. Closing the browser page does not cancel an accepted operation. After writes reopen, snapshot recovery never overwrites new user writes; binary rollback requires format compatibility.

Preparation and backup report bytes, files, speed and heartbeat through the same durable state and timestamped journal, with progress events at ten-second intervals during long stages. Notification errors distinguish an invalid public panel URL from a refused central connection or failed authentication. These warnings do not block updates: release downloading retains its signed HTTPS fallback if NetChan TCP 9878 is unavailable.

Backup options require a supervisor built with this feature. An old supervisor rejects the new command instead of silently ignoring the policy. Install the verified new launcher at the service's existing executable path and restart the service once as described above; keep the installation and update journal intact. The update page and CLI report when this bootstrap is required.

At least the previous working binary and recovery backup are retained. After the final journal is durably acknowledged, the operation worker prunes superseded snapshots, partial operations and unreferenced binaries. Cleanup also retries after restart. Current, previous, candidate, launcher and active recovery references are retained; a failed preparation preserves the last working snapshot. Version operations remain serialized throughout cleanup, and cleanup errors are reported separately without undoing a successful update. Do not delete `.sitebrush-update` while changing versions: it contains the selected version, recovery journal and private contact identity. The launcher protocol is versioned; an incompatible launcher requires a separate administrative installation. The first release containing this feature must likewise be installed and started normally, because older executables do not implement `-upgrade`. Offline updates require this established supervisor state; an unrelated downloaded executable cannot claim ownership of a legacy running installation.

Publishing or discovering a release never sends update emails. Only the authenticated owner of the pinned central sitebrush.com installation can preview and start an important-release campaign: security fixes, significant features, or both. The central update page provides the announcement button, a localized branded preview, recipient estimate, delivery counts and cancellation. All such mail uses `SiteBrush <sitebrush@sitebrush.com>`; customer installations and the generic mail relay cannot create release announcements.

Before each delivery attempt, the central server requests a fresh nonce-bound Ed25519 status over the installation's outgoing NetChan session. TLS pins the central signing identity and verifies the registered installation key. The central listener uses TCP port **9877**; installations need outbound access, including those behind NAT, and require no public update-status endpoint. Status includes the actual selected version, active update state, current owners and their saved mail languages. The link must match the registered installation domain. Only confirmed owner addresses are eligible. Already updated installations are excluded; busy, offline or unknown installations wait for up to seven days. No fresh proof means no email.

Each owner receives at most one announcement per release; several qualifying installations are grouped into one message. Durable campaign history survives restarts and outbox cleanup. Saved languages come from successful account authentication and survive logout; the supported-language fallback is English. Ordinary version checks and manual updates remain available without a campaign.

## Publishing signed updates

The release signing key must match `pkg/appupdate/release-public-key.txt`. Provision its base64 Ed25519 private key as the protected GitHub Actions secret `SITEBRUSH_RELEASE_PRIVATE_KEY`; never commit the private key. Releases fail closed when this secret is missing or mismatched. The public key is an initial trust anchor: changing it without a compatible trust transition would prevent existing installations from accepting future releases.

`pkg/appupdate/compatibility.json` declares the generation of every persistent format, the readable generation range, the oldest migratable generation (`migrates_from`), and whether predecessor writes remain compatible. Review this contract for SQL migrations and filesystem-format changes. Incompatible changes advance the generation and disable predecessor rollback after activation. Unknown contracts cannot be published or installed.

The existing `stable release` commit trigger and manual workflow remain available. The workflow publishes numbered `release-<build>` assets, a signed `update.json`, and `SHA256SUMS`, then updates the persistent `stable-release` entry. Local and GitHub releases use `git rev-list --count HEAD` from the complete history as their numeric version; workflow run counters do not supersede source history. Build a new commit for a new release. For the primary local cross-compilation path, official `-sync` destinations automatically require and generate signed server metadata. Load the private key from `SITEBRUSH_RELEASE_PRIVATE_KEY`, `-release-key <private-file>`, or the default `os.UserConfigDir()/sitebrush/release-ed25519.key` (on macOS, `~/Library/Application Support/sitebrush/release-ed25519.key`). Nonofficial publication can opt in with `-sign-updates`. Signing requires the complete server matrix; the signing helper is also available as `go run ./scripts/releases -directory <server-artifacts> -version <build> -commit <full-sha>`.

## Cumulative database migrations

The latest binary carries the migration path; application release numbers are independent from database schema versions. A direct upgrade across twenty releases runs the missing SQL steps in order using `pkg/migrations`, without downloading intermediate executables. Each component has an append-only log with step ID, ordinal, revision and completion time. SQL changes and their completion marker commit in the same transaction. Legacy filesystem/data adapters are explicitly idempotent, run under the stopped database owner and record completion only after success.

Preflight rejects future schemas and changed, missing or reordered migration history before maintenance changes begin. The database backup covers the control database, all site and analytics databases, and their SQLite journal companions; health checks verify the resulting schemas before public writes resume. Tests include a chain of twenty dependent data changes with failure/resume and historical SiteBrush schemas from commit `9d4a3ab`.

The legacy `site-schema-v12` and `billing-schema-v27` chains are frozen compatibility baselines. Append future steps to their component or add a new component; never edit completed SQL, insert a step into an existing prefix or remove historical transformations. Keep data transformations idempotent where they cannot use one SQL transaction. If increasing an explicit schema version prevents the old binary from opening it, advance the persistent-data generation and declare a migration range separately from the readable range; do not advertise safe predecessor rollback. New components must be included in source-path and final-health validation.

## Releasing on GitHub

1. Keep a private backup of the Ed25519 signing key. In repository **Settings → Secrets and variables → Actions**, create the repository secret `SITEBRUSH_RELEASE_PRIVATE_KEY` containing its base64 private key. The key must match the embedded public trust anchor; never paste it into an issue, PR, workflow output or this repository.
2. Merge the reviewed PR. A commit message containing **`stable release`** on `main` triggers `release-request.yml`, which dispatches `release.yml`. Alternatively use **Actions → Stable Release → Run workflow → main**, or `gh workflow run release.yml --ref main`.
3. Wait for server and desktop builds and signing to finish. The workflow publishes immutable numbered `release-<build>` assets and updates the stable link only afterwards. Check `update.json`, `SHA256SUMS` and the required native-platform assets. The primary local build path remains `scripts/crosscompile`.
4. Install and start the first updater-enabled release normally. Later installations can use the protected update page or `sitebrush -upgrade`.
5. For an exceptional security or feature announcement, open the **sitebrush.com server update panel**, choose its reason, preview the message, and explicitly press the notification button. Releasing frequently never performs this step automatically.

## Local publication and NetChan downloads

The existing publishing command remains valid, including Docker desktop builds:

```sh
go run scripts/crosscompile/crosscompile.go \
  -sync "root@sitebrush.com=/var/lib/sitebrush/storage/chroot/sitebrush.com/download" \
  -sync "root@sitebrush.ru=/var/lib/sitebrush/storage/chroot/sitebrush.ru/download"
```

Docker readiness checks print their current step and stop after 30 seconds if the daemon or a cached-platform probe does not respond. Check that `docker info` returns normally before retrying; build and image-download output remains streamed, without a short compilation timeout. Publishing with `-sync` does not require stopping the running service. Avoid appending unconditional SSH replacement commands with `;`: they would execute even if compilation or signing failed. Subsequent installed updates use the protected panel or `sitebrush -upgrade`.

The command builds and publishes artifacts; it does not replace the running executable. Signed `update.json` and `SHA256SUMS` accompany the complete server matrix before publication. Missing or mismatched signing keys fail before compilation. The private key stays on the build machine. Desktop binaries continue to use the existing Docker/native build and distribution paths.

After the first updater-enabled installation has been started on sitebrush.com, its pinned central identity serves the published `storage/chroot/sitebrush.com/download` tree over TCP **9878**. Allow inbound access on that central server and outbound access from installations. Public release reads need no installation registration, email or administrative token. Channel/session delivery, bounded backpressure, verified resume and HTTPS fallback are documented in [pkg/appupdate/netchan.md](pkg/appupdate/netchan.md). Updating an installation still requires its protected confirmation page or `sitebrush -upgrade`.


## Importing browser-saved pages

On a missing page, the archive form accepts SiteBrush ZIP backups, Safari `.webarchive` files (binary or XML), and ZIP files containing Firefox's saved HTML and adjacent resource folder. A common outer folder and macOS ZIP metadata are accepted. A Firefox archive must have one unambiguous main HTML document; HTML frames inside its resource folder are preserved as resources.

Browser saves create the main page at the exact address entered in the form and then open its visual editor. Captured images, stylesheets, fonts, scripts and frames receive local URLs in a unique import directory. HTML, CSS imports, `srcset`, relative URLs, base URLs and text encodings are rewritten without fetching external resources. Missing remote resources remain external links. Normal authentication, CSRF checks, storage quotas, transactional page storage and revision/publication rules apply. SiteBrush ZIP backups continue to restore their original page hierarchy beneath the chosen base path.
