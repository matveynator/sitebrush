'use strict';
// Static contract for the shared technical-page skin. Run with: node scripts/browser/admin_theme_consistency_test.cjs
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const read = name => fs.readFileSync(path.join(__dirname, '../../', name), 'utf8');
const theme = read('web/static/admin_theme.css');
const controls = read('web/static/admin_controls.css');
const shared = read('web/static/technical_pages.css');
const server = read('web/static/server_settings.css');
const profiler = read('web/static/server_profiler.css');

assert.match(shared, /@import url\("\/p\/static\/directory_listing\.css"\)/);
assert.match(read('web/static/directory_listing.css'), /@import url\("\/p\/static\/admin_theme\.css"\)/);
assert.match(theme, /:root:not\(\[data-bs-theme="light"\]\)/,
  'OS dark mode must not override an explicit light selection');
assert.match(theme, /:root\[data-bs-theme="dark"\]/,
  'explicit dark selection must work on a light OS');
assert.match(controls, /background-color: var\(--theme-card\) !important/,
  'form controls must use the same light/dark surface');
for (const filename of [
  'web/domain_settings.html', 'web/profile.html', 'web/expenses.html',
  'web/files.html', 'web/analytics.html', 'web/revisions.html',
  'web/server_update.html'
]) {
  assert.match(read(filename), /\/p\/static\/technical_pages\.css/,
    filename + ' must use the common technical page stylesheet');
}
assert.match(shared, /Unified admin shell/);
assert.match(server, /Match the canonical website settings shell/);
assert.match(server, /\.server-root-tabs button\.is-active/);
assert.match(profiler, /var\(--theme-font-sans\)/);
assert.match(profiler, /var\(--theme-card\)/);
assert.doesNotMatch(profiler, /Arial,Helvetica/,
  'profiler must not introduce a separate font family');
console.log('Admin theme consistency: shared palette, controls, settings, and profiler passed');
