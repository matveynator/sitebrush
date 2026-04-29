# Sidebrush wiki

## Overview
Sidebrush is a single-binary Go web app prototype for local SiteBrush-style editing.

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
go run Sidebrush.go
```
