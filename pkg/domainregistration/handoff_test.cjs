const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const source = fs.readFileSync(path.join(__dirname, '../../web/static/site_copy.js'), 'utf8');

function element() {
  return {
    dataset: {}, value: '', textContent: '', hidden: false, disabled: false, children: [], listeners: {},
    addEventListener(name, handler) { this.listeners[name] = handler; },
    appendChild(child) { this.children.push(child); },
    setCustomValidity() {}, reportValidity() { return true; },
    hasAttribute() { return false; },
    submit() { this.submitted = true; }
  };
}

function registrationBrowser(domainReplies, ownerDomain = 'sitebrush.example') {
  const controls = new Map();
  const select = selector => {
    if (!controls.has(selector)) controls.set(selector, element());
    return controls.get(selector);
  };
  const form = element();
  form.querySelector = select;
  const body = element();
  const requests = [];
  const redirects = [];
  const window = {location: new URL('https://' + ownerDomain + '/'), SiteBrushDomainNameInput: {
    normalize: name => name.toLowerCase().trim(), valid: name => /^[a-z0-9-]+$/.test(name),
    address: (name, parent) => name + '.' + parent
  }};
  window.location.assign = address => redirects.push(address);
  const document = {currentScript: null, body, createElement: () => element()};
  const fetch = async (address, options) => {
    requests.push({address, options});
    if (address.includes('trial_site_texts')) return {ok: true, json: async () => ({})};
    return {ok: true, json: async () => domainReplies.shift()};
  };
  vm.runInNewContext(source, {window, document, fetch, URL, FormData, console});
  window.SiteBrushPublicTrial.attachNewSite(form, {endpoint: 'https://' + ownerDomain + '/', parentDomain: ownerDomain});
  select('input[name="subdomain"]').value = 'example';
  return {form, body, requests, redirects};
}

const flush = () => new Promise(resolve => setImmediate(resolve));

test('parent and subdomain handoff keeps correlation outside URLs', async () => {
  const browser = registrationBrowser([{domain: 'example.sitebrush.example', available: true, registration_attempt: 'a'.repeat(48)}]);
  browser.form.listeners.submit({preventDefault() {}});
  await flush();
  const check = browser.requests.find(request => request.address.includes('trial_site_domain_check'));
  assert.equal(check.options.method, 'POST');
  assert.equal(check.options.body.get('subdomain'), 'example');
  assert.equal(check.address.includes('a'.repeat(48)), false);
  assert.equal(browser.body.children.length, 1);
  const handoff = browser.body.children[0];
  assert.equal(handoff.method, 'POST');
  assert.equal(handoff.action, 'http://example.sitebrush.example/?register&site_workflow=new_site');
  assert.equal(handoff.children[0].name, 'registration_attempt');
  assert.equal(handoff.children[0].value, 'a'.repeat(48));
  assert.equal(handoff.action.includes('a'.repeat(48)), false);
  assert.equal(handoff.submitted, true);
});

test('a retried domain check reuses the attempt in a POST body', async () => {
  const browser = registrationBrowser([
    {domain: 'example.sitebrush.example', available: false, registration_attempt: 'b'.repeat(48)},
    {domain: 'example.sitebrush.example', available: true, registration_attempt: 'b'.repeat(48)}
  ]);
  browser.form.listeners.submit({preventDefault() {}});
  await flush();
  browser.form.listeners.submit({preventDefault() {}});
  await flush();
  const checks = browser.requests.filter(request => request.address.includes('trial_site_domain_check'));
  assert.equal(checks.length, 2);
  assert.equal(checks[0].options.body.get('registration_attempt'), null);
  assert.equal(checks[1].options.body.get('registration_attempt'), 'b'.repeat(48));
  assert.equal(checks[1].address.includes('b'.repeat(48)), false);
  assert.equal(browser.body.children.length, 1);
});

test('registration continues when analytics cannot create a correlation attempt', async () => {
  const browser = registrationBrowser([{domain: 'example.sitebrush.example', available: true, registration_attempt: ''}]);
  browser.form.listeners.submit({preventDefault() {}});
  await flush();
  assert.deepEqual(browser.redirects, ['http://example.sitebrush.example/?register&site_workflow=new_site']);
  assert.equal(browser.body.children.length, 0);
});

test('a custom installation domain keeps its own registration handoff', async () => {
  const browser = registrationBrowser([{domain: 'example.atelier.example.net', available: true, registration_attempt: 'c'.repeat(48)}], 'atelier.example.net');
  browser.form.listeners.submit({preventDefault() {}});
  await flush();
  const check = browser.requests.find(request => request.address.includes('trial_site_domain_check'));
  assert.equal(new URL(check.address).host, 'atelier.example.net');
  assert.equal(browser.body.children[0].action, 'http://example.atelier.example.net/?register&site_workflow=new_site');
  assert.equal(browser.body.children[0].children[0].value, 'c'.repeat(48));
});
