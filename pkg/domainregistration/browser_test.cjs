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
};
function browser(result, fail = false) {
  const elements = new Map();
  const element = () => ({textContent: '', dataset: {}, hidden: false, value: 0, children: [], replaceChildren(...children) { this.children = children; }, append(child) { this.children.push(child); }});
  const get = id => { if (!elements.has(id)) elements.set(id, element()); return elements.get(id); };
  get('https-progress').dataset.state = 'queued';
  const timeouts = [], intervals = [];
  vm.runInNewContext(source, {
    document: { getElementById: get, querySelectorAll: () => Object.entries(texts).map(([key,value]) => ({dataset: {key}, textContent: value})), createElement: element },
    location: new URL('http://ero.sitebrush.ru/?email_confirm=secret'), URL, Date, AbortSignal,
    fetch: async () => { if (fail) throw new Error('offline'); return {ok: true, json: async () => result}; },
    setTimeout: callback => timeouts.push(callback), setInterval: callback => intervals.push(callback),
  });
  return {get, timeouts, intervals};
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
