const {test} = require('node:test');
const assert = require('node:assert/strict');
const vm = require('node:vm');
const fs = require('node:fs');
const path = require('node:path');
const source = fs.readFileSync(path.join(__dirname, '../../web/static/account_https.js'), 'utf8');
const texts = {
  auth_https_queued: 'Queued', auth_https_requesting: 'Requesting', auth_https_ready: 'Ready',
  auth_https_wait: 'Waiting', auth_https_status_error: 'Reconnecting', auth_https_continue: 'Continue',
  auth_https_next_retry: 'Retry in', auth_https_waiting_dns: 'Waiting for DNS',
  auth_https_expired: 'Confirmation link expired', setup_create_admin: 'Create administrator',
};
function browser(result, fail = false, options = {}) {
  const elements = new Map();
  const element = () => ({textContent: '', dataset: {}, hidden: false, value: 0, children: [], replaceChildren(...children) { this.children = children; }, append(child) { this.children.push(child); }});
  const get = id => { if (!elements.has(id)) elements.set(id, element()); return elements.get(id); };
  get('https-progress').dataset.state = 'queued';
  get('https-progress').dataset.refreshUrl = options.refreshUrl || '/?email_confirm=secret';
  const requested = [];
  const timeouts = [], intervals = [];
  vm.runInNewContext(source, {
    document: { getElementById: get, querySelectorAll: () => Object.entries(texts).map(([key,value]) => ({dataset: {key}, textContent: value})), createElement: element },
    location: new URL(options.location || 'http://ero.sitebrush.ru/?email_confirm=secret'), URL, Date, AbortSignal,
    fetch: async url => { requested.push(new URL(url)); if (fail) throw new Error('offline'); return {ok: !options.httpStatus || options.httpStatus < 400, status: options.httpStatus || 200, json: async () => result}; },
    setTimeout: callback => timeouts.push(callback), setInterval: callback => intervals.push(callback),
  });
  return {get, timeouts, intervals, requested};
}
const flush = () => new Promise(resolve => setImmediate(resolve));
test('the countdown uses the server job start, keeps waiting visibly after ten minutes, and shows the retry deadline', async () => {
  const b = browser({ready: false, progress: {state: 'waiting_dns', started_at: new Date(Date.now() - 660000).toISOString(), retry_at: new Date(Date.now() + 60000).toISOString(), error: '<DNS error>', events: [{state: 'queued', at: new Date().toISOString()}]}});
  await flush();
  assert.equal(b.get('https-countdown').textContent, '0:00');
  assert.equal(b.get('https-timeout').hidden, false);
  assert.equal(b.get('https-stage').textContent, 'Waiting for DNS');
  assert.equal(b.get('https-error').textContent, '<DNS error>');
  assert.match(b.get('https-next-retry').textContent, /^Retry in/);
  assert.equal(b.get('https-events').children.length, 1);
  assert.equal(b.timeouts.length, 1);
});
test('a fast certificate finishes at 100%, preserving the original token only in the same-host HTTPS continuation', async () => {
  const b = browser({ready: true, secure_url: 'https://ero.sitebrush.ru/?email_confirm=secret&https_status=1'});
  await flush();
  assert.equal(b.get('https-bar').value, 100);
  assert.equal(b.get('https-countdown').textContent, '0:00');
  assert.equal(b.get('https-stage').textContent, 'Ready');
  assert.equal(b.get('https-progress').children[0].href, 'https://ero.sitebrush.ru/?email_confirm=secret');
  assert.equal(b.timeouts.length, 0);
});
test('temporary disconnection keeps polling instead of silently stopping', async () => {
  const b = browser(null, true); await flush();
  assert.equal(b.get('https-error').textContent, 'Reconnecting');
  assert.equal(b.timeouts.length, 1);
});

test('six-digit code polls the server confirmation URL when the address bar still contains register', async () => {
  const b = browser({ready: false, progress: {state: 'requesting', events: []}}, false, {
    location: 'http://ero.sitebrush.ru/?register',
    refreshUrl: '/?email_confirm=code-token&return_path=%2Fabout',
  });
  await flush();
  assert.equal(b.requested.length, 1);
  assert.equal(b.requested[0].origin, 'http://ero.sitebrush.ru');
  assert.equal(b.requested[0].searchParams.get('email_confirm'), 'code-token');
  assert.equal(b.requested[0].searchParams.get('https_status'), '1');
  assert.equal(b.requested[0].searchParams.get('return_path'), '/about');
  assert.equal(b.requested[0].searchParams.has('register'), false);
  assert.equal(b.get('https-stage').textContent, 'Requesting');
  assert.equal(b.get('https-error').textContent, '');
});
test('a malformed cross-origin refresh URL never receives the confirmation token', async () => {
  const b = browser(null, false, {refreshUrl: 'https://other.example/?email_confirm=secret'});
  await flush();
  assert.equal(b.requested.length, 0);
  assert.equal(b.get('https-error').textContent, 'Reconnecting');
});

test('an expired link stops reconnecting and offers a fresh registration without reusing its bearer token', async () => {
  for (const httpStatus of [404, 410]) {
    const b = browser(null, false, {httpStatus});
    await flush();
    assert.equal(b.get('https-stage').textContent, 'Confirmation link expired');
    assert.equal(b.timeouts.length, 0);
    const restart = new URL(b.get('https-progress').children[0].href);
    assert.equal(restart.origin, 'http://ero.sitebrush.ru');
    assert.equal(restart.searchParams.has('register'), true);
    assert.equal(restart.searchParams.has('email_confirm'), false);
    assert.equal(b.get('https-timeout').hidden, true);
  }
});
