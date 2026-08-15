<p align="center">
  <a href="https://sitebrush.com/">
    <img src="https://sitebrush.com/p/ad311fa907c60a01022a1f14a934f5ab.png" alt="SiteBrush logo" width="170">
  </a>
</p>

<h1 align="center">SiteBrush</h1>

<p align="center">
  <strong>Replace hacked WordPress with editable static HTML.</strong><br>
  Keep the website and its design. Remove the fragile public CMS backend.
</p>

<p align="center">
  <a href="https://sitebrush.com/">Website</a> ·
  <a href="https://demo.sitebrush.com/">Live demo</a> ·
  <a href="#downloads">Downloads</a> ·
  <a href="#install-the-server-version">Install</a>
</p>

<p align="center">
  <img src="https://img.shields.io/badge/output-static_HTML-0ea5e9" alt="Static HTML">
  <img src="https://img.shields.io/badge/deployment-one_binary-f97316" alt="One binary">
  <img src="https://img.shields.io/badge/editing-right--click_or_long--press-22c55e" alt="Browser editing">
</p>

---

SiteBrush imports an existing website, preserves its pages and design, and replaces the risky public-facing CMS with fast static files.

Visitors receive ordinary HTML, CSS, JavaScript, and images. The site owner can still edit text, pictures, buttons, menus, and page blocks directly in the browser.

**SiteBrush is not a WordPress clone. It is a WordPress retirement tool.**

## Why SiteBrush?

- Preserve the existing design instead of rebuilding the website.
- Remove WordPress, public login forms, plugins, themes, PHP, and the database from the visitor-facing side.
- Reduce the attack surface and ongoing maintenance burden.
- Edit content directly on the page with a right-click or long-press.
- Freeze the public version while preparing changes, then publish safely.
- Run the server as one standalone binary without a separate dependency stack.
- Import existing WordPress, Joomla, Wix, Readymag, and custom websites.

Nothing is completely invulnerable, but a static public website exposes far fewer moving parts than a traditional WordPress installation.

## How it works

1. **Import** — SiteBrush copies pages, media, styles, scripts, and other website files.
2. **Edit** — open the site and use **right-click** on desktop or **long-press** on mobile.
3. **Publish safely** — visitors keep seeing the stable version until your changes are ready.

[Try SiteBrush with your own website](https://sitebrush.com/) without changing the live site, or [open the demo](https://demo.sitebrush.com/).

SiteBrush Templates

Static sites are simple and fast, but repeated content can be annoying to maintain.

Imagine you import a website with 200 pages, all with the same footer address. Just mark one of those elements as a template:

<div class="SiteBrush-Template FooterAddress">
    123 Main Street, New York, NY 10001
</div>

SiteBrush will find the same matching elements on the other pages and assign the template to them too.

From that point on, you can edit the element in one place, and SiteBrush keeps it the same across all pages.

This works well for headers, footers, sidebars, menus, contact information, tables, shared <style> blocks and other repeated content.

All changes are stored in revisions, so previous versions can be restored.

Automatic Import

When you import an existing site, SiteBrush also follows its references and automatically imports the files it uses — CSS, JavaScript, images and other assets.

So you can take an existing static website, import it, and immediately start editing it almost like a dynamic CMS — while the result remains simple static HTML.

# Downloads

The **server version is recommended** for a public website. Desktop builds are useful for local work with a graphical interface.

| Platform | Desktop application | Server binary |
|---|---|---|
| <img src="https://img.shields.io/badge/Linux-111827?logo=linux&logoColor=white" alt="Linux"> | [amd64 GTK 4.1](https://sitebrush.com/download/latest/desktop-app/sitebrush_linux_amd64_desktop_gtk41.zip) · [GTK 4.0](https://sitebrush.com/download/latest/desktop-app/sitebrush_linux_amd64_desktop_gtk40.zip)<br>[arm64 GTK 4.1](https://sitebrush.com/download/latest/desktop-app/sitebrush_linux_arm64_desktop_gtk41.zip) · [GTK 4.0](https://sitebrush.com/download/latest/desktop-app/sitebrush_linux_arm64_desktop_gtk40.zip) | [amd64](https://sitebrush.com/download/latest/server-app/sitebrush_linux_amd64) · [arm64](https://sitebrush.com/download/latest/server-app/sitebrush_linux_arm64) |
| <img src="https://img.shields.io/badge/macOS-111827?logo=apple&logoColor=white" alt="macOS"> | [Universal DMG](https://sitebrush.com/download/latest/desktop-app/sitebrush_darwin_universal_desktop.dmg) | [Intel amd64](https://sitebrush.com/download/latest/server-app/sitebrush_darwin_amd64) · [Apple Silicon arm64](https://sitebrush.com/download/latest/server-app/sitebrush_darwin_arm64) |
| <img src="https://img.shields.io/badge/Windows-0078D4?logo=windows11&logoColor=white" alt="Windows"> | [amd64 ZIP](https://sitebrush.com/download/latest/desktop-app/sitebrush_windows_amd64_desktop.exe.zip) · [arm64 ZIP](https://sitebrush.com/download/latest/desktop-app/sitebrush_windows_arm64_desktop.exe.zip) | [amd64 EXE](https://sitebrush.com/download/latest/server-app/sitebrush_windows_amd64.exe) · [arm64 EXE](https://sitebrush.com/download/latest/server-app/sitebrush_windows_arm64.exe) |
| <img src="https://img.shields.io/badge/FreeBSD-AB2B28?logo=freebsd&logoColor=white" alt="FreeBSD"> | — | [amd64](https://sitebrush.com/download/latest/server-app/sitebrush_freebsd_amd64) · [arm64](https://sitebrush.com/download/latest/server-app/sitebrush_freebsd_arm64) |
| <img src="https://img.shields.io/badge/OpenBSD-F2CA30?logo=openbsd&logoColor=black" alt="OpenBSD"> | — | [amd64](https://sitebrush.com/download/latest/server-app/sitebrush_openbsd_amd64) · [arm64](https://sitebrush.com/download/latest/server-app/sitebrush_openbsd_arm64) |
| <img src="https://img.shields.io/badge/NetBSD-F0544C?logo=netbsd&logoColor=white" alt="NetBSD"> | — | [amd64](https://sitebrush.com/download/latest/server-app/sitebrush_netbsd_amd64) · [arm64](https://sitebrush.com/download/latest/server-app/sitebrush_netbsd_arm64) |

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

# Start editing

After installation:

1. Point the website domain to the server.
2. Open the domain in a browser.
3. Use **right-click** on a computer or **long-press** on a phone.
4. Edit the content and save it.

```text
https://your-domain.example
```

## Best suited for

- Business websites and landing pages
- Portfolios and agency websites
- Documentation and knowledge bases
- Old WordPress websites that keep breaking
- Mostly static websites that still need easy browser editing

Keep a traditional dynamic CMS when the project requires complex e-commerce, memberships, extensive server-side workflows, or custom plugin logic.

---

<p align="center">
  <strong>Keep the website. Keep the design. Keep simple browser editing.<br>
  Leave the database, plugin stack, public admin backend, and constant maintenance behind.</strong>
</p>

<p align="center">
  <a href="https://sitebrush.com/"><strong>sitebrush.com</strong></a>
</p>
