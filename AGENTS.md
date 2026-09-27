# Project rules

## Go dependencies and builds

- CGO is forbidden for server builds, normal tests, and application logic. Use `CGO_ENABLED=0` there.
- CGO is allowed only inside Docker desktop builds when the selected native webview backend requires it.
- Add only dependencies implemented in native Go and verify that their transitive dependencies do not require CGO.
- Do not introduce packages that require a C compiler, native libraries, or cgo-enabled database drivers.
