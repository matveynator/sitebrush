# Sitebrush wiki

## Overview
Sitebrush is a single-binary Go web app prototype for local SiteBrush-style editing.

## Implemented now
- bootstrap first administrator when none exists
- create page when route is missing
- edit page with CKEditor
- page revisions list
- revision restore
- revision delete
- different top menu for authenticated and guest users
- all templates loaded from go:embed files

## Run
```bash
go run sitebrush.go
```

## Crosscompile release artifacts
```bash
go run ./scripts/crosscompile -version 123
```

Optional rsync publication:
```bash
go run ./scripts/crosscompile -version 123 -sync-host deploy@example.com -sync-base /srv/releases
```

The script writes artifacts to `binaries/<version>/server-app` and
`binaries/<version>/desktop-app`, updates `binaries/latest`, and mirrors the
release layout used in `.github/workflows/release.yml`.
