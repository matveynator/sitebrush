<p align="center">
  <a href="https://sitebrush.com">
    <img src="https://sitebrush.com/p/ad311fa907c60a01022a1f14a934f5ab.png" alt="Sitebrush logo" width="180">
  </a>
</p>

<h1 align="center">Sitebrush</h1>

<p align="center">
  <strong>Replace hacked WordPress with editable static HTML.</strong>
</p>

<p align="center">
  WordPress rescue CMS · Static HTML · One binary
</p>

<p align="center">
  <a href="https://sitebrush.com">Website</a> ·
  <a href="#download">Download</a> ·
  <a href="#how-it-works">How it works</a>
</p>

Sitebrush imports an existing website, removes the risky dynamic backend, and creates a fast static website that can still be edited directly in the browser.

It is designed for old business websites, landing pages, portfolios, documentation, small company websites, and abandoned WordPress installations that no longer need a database, plugins, or a public admin panel.

Why Sitebrush?

Many websites contain only pages, text, images, menus, articles, and contact information, but still depend on WordPress, PHP, MySQL, themes, plugins, updates, and public login pages.

Sitebrush keeps the website and its design while removing the fragile backend.

Static HTML for visitors

Browser-based visual editing

Import of pages, images, CSS, JavaScript, and other files

Revisions and safe publishing

Shared headers, footers, menus, and other template blocks

Protection for old URLs

One standalone binary

No plugin maintenance stack

Linux, Windows, macOS, FreeBSD, OpenBSD, and NetBSD support

Sitebrush is not a WordPress clone. It is a WordPress retirement tool.

How it works

ImportPoint Sitebrush at an existing WordPress, Joomla, Wix, Readymag, or custom website.

EditOpen the imported website and use right click on desktop or long press on mobile to edit its content.

Publish safelyFreeze the public version while editing. Visitors continue to see the stable website until the new version is ready.

Download

Latest builds are available at:

https://sitebrush.com/download/latest

Desktop applications

macOS

<p>
  <img src="https://sitebrush.com/p/fbad588e1b8c94b6b80708bc9917706e.png" alt="macOS" width="42">
</p>

Universal build for Intel and Apple Silicon:

Download Sitebrush for macOS

Windows

<p>
  <img src="https://sitebrush.com/p/66aab89d1af641ee0ae190f6b3ea4e09.png" alt="Windows" width="42">
</p>

Windows amd64 ZIP

Windows arm64 ZIP

Linux

<p>
  <img src="https://sitebrush.com/p/04b158d78c93b65c714bb6256da221a4.png" alt="Linux" width="42">
</p>

GTK 4.1 is intended for newer distributions. GTK 4.0 is intended for older LTS distributions.

Linux amd64 — GTK 4.1

Linux amd64 — GTK 4.0

Linux arm64 — GTK 4.1

Linux arm64 — GTK 4.0

Server installation

The server version is recommended for public websites. Sitebrush installs itself as a system service and automatically detects the operating system, CPU architecture, and available service manager.

Linux

<p>
  <img src="https://sitebrush.com/p/04b158d78c93b65c714bb6256da221a4.png" alt="Linux" width="42">
</p>

amd64

sudo curl -L -o /usr/local/bin/sitebrush \
  https://sitebrush.com/download/latest/server-app/sitebrush_linux_amd64

sudo chmod +x /usr/local/bin/sitebrush
sudo /usr/local/bin/sitebrush -install

arm64

sudo curl -L -o /usr/local/bin/sitebrush \
  https://sitebrush.com/download/latest/server-app/sitebrush_linux_arm64

sudo chmod +x /usr/local/bin/sitebrush
sudo /usr/local/bin/sitebrush -install

macOS

<p>
  <img src="https://sitebrush.com/p/fbad588e1b8c94b6b80708bc9917706e.png" alt="macOS" width="42">
</p>

Intel amd64

sudo curl -L -o /usr/local/bin/sitebrush \
  https://sitebrush.com/download/latest/server-app/sitebrush_darwin_amd64

sudo chmod +x /usr/local/bin/sitebrush
sudo /usr/local/bin/sitebrush -install

Apple Silicon arm64

sudo curl -L -o /usr/local/bin/sitebrush \
  https://sitebrush.com/download/latest/server-app/sitebrush_darwin_arm64

sudo chmod +x /usr/local/bin/sitebrush
sudo /usr/local/bin/sitebrush -install

Windows

<p>
  <img src="https://sitebrush.com/p/66aab89d1af641ee0ae190f6b3ea4e09.png" alt="Windows" width="42">
</p>

Run PowerShell as Administrator.

amd64

$ErrorActionPreference = "Stop"
$Dir = Join-Path $env:ProgramFiles "sitebrush"
$Exe = Join-Path $Dir "sitebrush.exe"

New-Item -ItemType Directory -Force -Path $Dir | Out-Null
Invoke-WebRequest `
  -Uri "https://sitebrush.com/download/latest/server-app/sitebrush_windows_amd64.exe" `
  -OutFile $Exe

& $Exe -install

arm64

$ErrorActionPreference = "Stop"
$Dir = Join-Path $env:ProgramFiles "sitebrush"
$Exe = Join-Path $Dir "sitebrush.exe"

New-Item -ItemType Directory -Force -Path $Dir | Out-Null
Invoke-WebRequest `
  -Uri "https://sitebrush.com/download/latest/server-app/sitebrush_windows_arm64.exe" `
  -OutFile $Exe

& $Exe -install

FreeBSD

<p>
  <img src="https://sitebrush.com/p/c1ce8baa90a2ffd348069e69fa4fda93.png" alt="FreeBSD" width="42">
</p>

Replace ARCH with amd64 or arm64.

sudo fetch -o /usr/local/bin/sitebrush \
  https://sitebrush.com/download/latest/server-app/sitebrush_freebsd_ARCH

sudo chmod +x /usr/local/bin/sitebrush
sudo /usr/local/bin/sitebrush -install

OpenBSD

<p>
  <img src="https://sitebrush.com/p/e3124d65b5feeb6af8ec8f882b167a35.png" alt="OpenBSD" width="42">
</p>

Replace ARCH with amd64 or arm64.

sudo ftp -o /usr/local/bin/sitebrush \
  https://sitebrush.com/download/latest/server-app/sitebrush_openbsd_ARCH

sudo chmod +x /usr/local/bin/sitebrush
sudo /usr/local/bin/sitebrush -install

NetBSD

<p>
  <img src="https://sitebrush.com/p/c1ce8baa90a2ffd348069e69fa4fda93.png" alt="NetBSD" width="42">
</p>

Replace ARCH with amd64 or arm64.

sudo ftp -o /usr/local/bin/sitebrush \
  https://sitebrush.com/download/latest/server-app/sitebrush_netbsd_ARCH

sudo chmod +x /usr/local/bin/sitebrush
sudo /usr/local/bin/sitebrush -install

Uninstall

Linux, macOS, and BSD

sudo /usr/local/bin/sitebrush -uninstall

Windows

Run PowerShell as Administrator:

& "$env:ProgramFiles\sitebrush\sitebrush.exe" -uninstall

Editing a website

After installation, open your domain in a browser.

https://your-domain.example

Use right click on desktop or long press on mobile to open the Sitebrush editor.

Best use cases

Sitebrush is a good choice for:

Business websites

Landing pages

Portfolios and agency websites

Documentation and knowledge bases

Old WordPress websites that keep breaking

Mostly static websites that still need simple browser editing

Keep a traditional dynamic CMS when the project requires complex e-commerce, memberships, extensive server-side workflows, or custom plugin logic.

<p align="center">
  <strong>Keep the website. Keep the design. Keep browser editing.<br>
  Remove the database, plugin stack, public admin backend, and endless update anxiety.</strong>
</p>

<p align="center">
  <a href="https://sitebrush.com">sitebrush.com</a>
</p>
