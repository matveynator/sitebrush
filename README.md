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
  <a href="https://sitebrush.com/"><strong>Try your website</strong></a> ·
  <a href="https://demo.sitebrush.com/">Live demo</a> ·
  <a href="#downloads">Download</a> ·
  <a href="#install-the-server-version">Install</a>
</p>

<p align="center">
  <a href="https://pkg.go.dev/github.com/matveynator/sitebrush/v2"><img src="https://pkg.go.dev/badge/github.com/matveynator/sitebrush/v2.svg" alt="Go Reference"></a>
  <a href="https://goreportcard.com/report/github.com/matveynator/sitebrush"><img src="https://goreportcard.com/badge/github.com/matveynator/sitebrush" alt="Go Report Card"></a>
  <a href="https://app.codecov.io/gh/matveynator/sitebrush"><img src="https://codecov.io/gh/matveynator/sitebrush/graph/badge.svg" alt="Code coverage"></a>
</p>

<p align="center">
  <img src="https://img.shields.io/badge/output-static_HTML-0ea5e9" alt="Static HTML">
  <img src="https://img.shields.io/badge/deployment-one_binary-f97316" alt="One binary">
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
