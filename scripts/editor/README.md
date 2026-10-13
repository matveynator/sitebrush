# Editor regression tests

The browser test uses the embedded Jodit assets, production upload/save handlers,
an authenticated administrator session, and temporary site storage. It runs at
1280×800 and 320×568. The Go fixture exposes only test images and a read-only view
of saved HTML; upload and save are not mocked in the successful scenarios.

From this directory:

```sh
npm ci --ignore-scripts
npx playwright install --with-deps chromium
node document_test.cjs
```

From the repository root:

```sh
CGO_ENABLED=0 SITEBRUSH_EDITOR_BROWSER_TEST=1 go test . -run '^TestVisualEditorBrowserRegressions$' -count=1
```

`SITEBRUSH_EDITOR_CHROMIUM` optionally selects an existing Chromium executable.
`SITEBRUSH_EDITOR_ARTIFACT_DIR` optionally saves screenshots of each code toolbar.
CI installs the locked test dependencies and runs both regression suites.
