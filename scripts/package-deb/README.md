# Debian and Ubuntu packaging

SiteBrush publishes native `.deb` packages for Linux `amd64` and `arm64`.

## Package layout

The package installs the standalone server binary as:

```text
/usr/bin/sitebrush
```

The package uses SiteBrush's existing service installer during `postinst` and
removes that service during package removal. Package upgrades do not uninstall
the running service before the new package is configured.

## Build a package

```sh
scripts/package-deb/build.sh \
  binaries/sitebrush_linux_amd64 \
  amd64 \
  2.0.123 \
  dist
```

The stable release workflow builds both Debian architectures automatically.

## SiteBrush APT repository

The public APT repository should have its own stable URL:

```text
https://sitebrush.com/apt/
```

Do not use `/download/latest/` as the APT repository root. That path represents
the latest downloadable build, while APT repositories need persistent package
versions plus signed metadata.

Recommended archive layout:

```text
/apt/
├── sitebrush-archive-keyring.asc
├── dists/
│   └── stable/
│       ├── InRelease
│       ├── Release
│       ├── Release.gpg
│       └── main/
│           ├── binary-amd64/
│           │   ├── Packages
│           │   └── Packages.gz
│           └── binary-arm64/
│               ├── Packages
│               └── Packages.gz
└── pool/
    └── main/
        └── s/
            └── sitebrush/
                ├── sitebrush_VERSION_amd64.deb
                └── sitebrush_VERSION_arm64.deb
```

The archive metadata must be signed with a dedicated repository OpenPGP key.
Client setup should use `Signed-By`; do not use the deprecated global
`apt-key` mechanism.

A future `sitebrush-archive-keyring` package can manage repository signing-key
rotation after the initial bootstrap.

## Official Debian

The `.deb` files produced here are upstream binary packages for the SiteBrush
repository. Admission to the official Debian archive is a separate process and
requires a Debian source package that Debian can rebuild from source according
to Debian policy.
