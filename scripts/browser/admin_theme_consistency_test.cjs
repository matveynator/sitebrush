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
// Deletion must remain a real grid child beside AI · SiteBrush, never after </main>.
const profile = read('web/profile.html');
const aiConnect = profile.indexOf('class="authentication-card profile-form profile-account-card profile-ai-connect-card"');
const danger = profile.indexOf('class="authentication-card profile-form profile-account-card profile-delete-card"');
const aiAccess = profile.indexOf('class="authentication-card profile-form profile-account-card profile-ai-card"');
const gridClose = profile.indexOf('</div>\n</main>', danger);
assert.ok(aiConnect >= 0 && danger > aiConnect && aiAccess > danger,
  'destructive card must be after AI · SiteBrush and before AI-access card');
assert.ok(gridClose > danger, 'destructive card must appear inside the profile grid');
assert.equal(profile.match(/class="authentication-card profile-form profile-account-card profile-delete-card"/g).length, 1,
  'both account and website deletion must occupy one grid cell');
for (const action of ['delete_site', 'delete_account']) {
  assert.match(profile, new RegExp('name="profile_action" value="' + action + '"'),
    action + ' must preserve its POST action');
}
assert.ok(shared.includes('.profile-delete-card .profile-account-card-header h2'),
  'danger heading must be styled in the shared stylesheet');
assert.ok(shared.includes('var(--theme-destructive)'),
  'danger zone must use the canonical destructive token');
assert.ok(shared.includes('grid-template-columns: minmax(0, 1fr);'),
  'profile cards must stack into one column on phones');
assert.ok(!shared.includes('.profile-account-grid > .profile-ai-card { grid-column: 2; }'),
  'AI access must auto-place when deletion occupies column two');

const files = read('web/files.html');
assert.ok(!files.includes('value="set_favicon"'),
  'files manager must not offer the ambiguous favicon action');
assert.ok(files.includes('value="save_access"') && files.includes('data-preview-image'),
  'existing access and preview actions must remain available');

console.log('Admin theme consistency: palette, controls, profile grid, deletion and files actions, and profiler passed');
