# SiteBrush MCP operations

Tools may have a client-specific connection prefix; find them by their `sitebrush_` names and verify the connected site with `get_access`.

| Tools | Purpose and constraints |
| --- | --- |
| `get_access` | Actual site, scopes, expiry, limits, page restriction and task. No credentials. |
| `list_pages`, `read_page` | Page metadata and current HTML/version; use exact returned paths. |
| `create_page`, `update_page` | Save drafts through normal templates, revisions and quotas. Updates require `expected_version`. |
| `list_files`, `upload_file` | Existing files and immutable uploads. Upload accepts `file` from ChatGPT or base64 `file_content`; supply exactly one source. `file_name`, page `path`, `image_width`, `image_height`, `image_fit` use the normal uploader. |
| `list_revisions`, `rollback` | Restore target `revision_id`, guarded by current `expected_version`, as a draft. |
| `publish` | Publish all pending site changes using SiteBrush publication. |
| `get_site_report`, `get_analytics_summary` | Bounded, privacy-safe aggregates with coverage and no-data states. |
| `get_funnels`, `get_paths`, `get_page_behaviour`, `get_forms`, `get_sources`, `get_page_impacts`, `get_insights` | Focused reports from the existing Analytics subsystem. |

Analytics periods are 1, 7, 30 or 90 days. Default is 30 days; rows are limited to 50 per call. Use the response's pagination information and `offset`/`limit` when additional rows are relevant. Reports do not expose identities, IPs, replay or raw form contents.

`read` permits page/file/revision reads; `write` permits draft edits, uploads and rollback; `publish` permits publication; `analytics` permits aggregate reports. Actual permissions can be narrower than the defaults or restricted to one page.

A revision conflict requires a new read and reconciliation. Quota or validation failures require correcting the requested change, not bypassing checks. An uncertain write outcome after a disconnect requires checking the page/revisions before retrying. Revocation terminates active work but does not undo a change already committed. Use the native connection flow to reconnect only when appropriate to the owner's request.
