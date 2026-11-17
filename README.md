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

Open the website normally.

* **Desktop:** right-click an element
* **Phone or tablet:** long-press

Edit text, pictures, buttons, menus, page blocks, or HTML.



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

On Unix systems the service starts with root privileges to apply system tuning and open ports 80/443, then runs SiteBrush under a dedicated service account. The data directory selected in the installer becomes its working directory and database root. Windows uses a dedicated virtual service account.

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
ftp -o /root/sitebrush-install.sh https://sitebrush.com/download/install.sh && sh /root/sitebrush-install.sh
```

Run this command as `root`. The installer uses OpenBSD's built-in `ftp`, verifies the downloaded binary, then asks where to store site data. For example, choose `/data1/sitebrush`. The service creates a dedicated unprivileged account and prepares the selected directory before startup.

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
/usr/local/bin/sitebrush -uninstall
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





## Go profiling

Add `--profiler` to the existing server arguments and restart the service with the new build. Profiling is disabled by default. On an installed service, retain the existing `-service-runtime`, `-path`, `-port` and logging arguments.

Each public/application process opens its own `127.0.0.1` diagnostic port and logs its role, PID, build version, address and a ready-to-copy capture command. Ports can change after a restart. The journal goes to the console for a foreground run, to `--logfile` when supplied, or to `storage/logs/sitebrush.log` beneath the installed service's data directory. The privileged service launcher does not start another profiler.

The profile is the HTTP response saved by `curl -o`. A relative output filename is written in the **curl client's current directory**, not automatically inside the server's data directory. Run these commands on the SiteBrush server, substituting the ports printed for `public` and `application`:

```sh
curl --fail --max-time 40 -o ./cpu-public.pprof 'http://127.0.0.1:PUBLIC_PORT/debug/pprof/profile?seconds=30' &
public_capture_pid=$!
curl --fail --max-time 40 -o ./cpu-application.pprof 'http://127.0.0.1:APPLICATION_PORT/debug/pprof/profile?seconds=30' &
application_capture_pid=$!
wait "$public_capture_pid"
wait "$application_capture_pid"
```

While those captures are running, run the existing benchmark from the separate load-generator machine:

```sh
ab -k -c 500 -n 10000 http://vz.sitebrush.com/
```

Keep both profiles and a copy of the **exact executable used by each profiled PID**; the public process and the updated application may use different binaries. On a machine with Go installed, inspect each profile with its matching binary:

```sh
go tool pprof -top ./sitebrush-public-binary ./cpu-public.pprof
go tool pprof -top -cum ./sitebrush-public-binary ./cpu-public.pprof
go tool pprof -top ./sitebrush-application-binary ./cpu-application.pprof
```

Additional local endpoints are `/debug/pprof/heap`, `/debug/pprof/allocs`, and `/debug/pprof/goroutine?debug=2`. Save the first two using `curl -o`, then use `go tool pprof -alloc_space` to inspect cumulative allocation cost. To measure allocations specifically during a benchmark, capture `/debug/pprof/allocs?seconds=30` in a separate run. CPU captures default to 30 seconds; explicit durations must be between 1 and 120 seconds. Only one CPU capture can run per process at a time.

The diagnostic listener is separate from website ports and accepts the printed loopback address. It closes when its process stops. Keep `-debug` off during performance measurements; `-request-timings` can provide complementary request-stage counters. Profiled throughput includes profiling overhead and should not be used as the final comparison against nginx.
