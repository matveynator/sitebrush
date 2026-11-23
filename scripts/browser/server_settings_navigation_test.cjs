'use strict';
// Reproduces real sidebar transitions without a browser or a live installation.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

const source = fs.readFileSync(path.join(__dirname, '../../web/static/server_settings.js'), 'utf8');

function navigationFixture({ hash = '', query = '?server-settings', includeNetwork = true, servedID = 'local' } = {}) {
  const location = new URL('https://example.test/' + query + hash);
  const events = {};
  const notices = [];
  let assignedURL = '';

  const views = ['overview', ...(includeNetwork ? ['network'] : [])].map(name => ({
    dataset: { serverConfigView: name }, hidden: false
  }));
  const buttons = ['overview', 'network'].map(section => ({
    dataset: { serverRootTarget: 'servers', serverConfigShortcut: section },
    classList: { toggle() {} },
    setAttribute() {},
    removeAttribute() {},
    textContent: section
  }));
  const layout = {
    dataset: {},
    querySelectorAll(selector) {
      return selector === '[data-server-config-view]' ? views : [];
    },
    querySelector() { return null; }
  };
  const panel = {
    dataset: { serverLocal: String(servedID === 'local'), serverId: servedID, serverName: servedID },
    hidden: false,
    querySelector(selector) {
      return selector === '[data-server-config-layout]' ? layout : null;
    },
    querySelectorAll(selector) {
      return selector === '[data-server-config-view]' ? views : [];
    },
    addEventListener() {}
  };
  const root = {
    hidden: false,
    querySelector(selector) {
      return selector === '[data-server-navigation-error]' ? notices[0] || null : null;
    },
    prepend(element) { notices.unshift(element); }
  };
  const tabs = { dataset: {localServerId: 'local'}, querySelectorAll: () => buttons };
  const document = {
    documentElement: { lang: 'ru' },
    body: { dataset: {} },
    querySelector(selector) {
      if (selector === '[data-server-root-tabs]') return tabs;
      if (selector === '[data-hosting-installation-panel="servers"]') return root;
      return null;
    },
    querySelectorAll(selector) {
      if (selector === '[data-expense-server-panel]') return [panel];
      if (selector === '[data-server-navigation-error]') return notices;
      return [];
    },
    addEventListener(event, handler) { events[event] = handler; },
    createElement() {
      return { dataset: {}, hidden: false, setAttribute() {}, className: '', textContent: '' };
    }
  };
  const window = {
    location: {
      get href() { return location.href; },
      get search() { return location.search; },
      get hash() { return location.hash; },
      assign(url) { assignedURL = url; }
    },
    addEventListener(event, handler) { events[event] = handler; }
  };
  const history = {
    pushState(_state, _title, hashValue) { location.hash = hashValue; },
    replaceState(_state, _title, hashValue) { location.hash = hashValue; }
  };
  new Function('window', 'document', 'history', 'URL', 'URLSearchParams', 'console', source)(
    window, document, history, URL, URLSearchParams, console
  );
  return {
    views, notices, panel,
    open(section) {
      const button = buttons.find(item => item.dataset.serverConfigShortcut === section);
      events.click({
        target: { closest: selector => selector === '[data-server-root-target]' ? button : null }
      });
    },
    get assignedURL() { return assignedURL; },
    get hash() { return location.hash; }
  };
}

const transition = navigationFixture();
assert.equal(transition.views[0].hidden, false);
transition.open('network');
assert.equal(transition.views[0].hidden, true, 'overview must close when opening network');
assert.equal(transition.views[1].hidden, false, 'network must open from overview');
transition.open('overview');
assert.equal(transition.views[0].hidden, false, 'overview must reopen from network');
assert.equal(transition.views[1].hidden, true);
assert.match(transition.hash, /section=overview/);

const deferred = navigationFixture({
  hash: '#server-id=local&section=network', includeNetwork: false
});
assert.match(deferred.assignedURL, /section=network/, 'missing lazy section must request its real route');

const unavailable = navigationFixture({
  query: '?server-settings&installation=local&section=network',
  hash: '#server-id=local&section=network', includeNetwork: false
});
assert.equal(unavailable.assignedURL, '', 'missing response must not trigger a reload loop');
assert.equal(unavailable.notices.length, 1, 'missing response must show an error');
assert.equal(unavailable.views[0].hidden, true, 'missing response must not show Overview');

const unknown = navigationFixture({ hash: '#server-id=local&section=unknown' });
assert.equal(unknown.assignedURL, '', 'unknown route must not be loaded repeatedly');
assert.equal(unknown.notices.length, 1);
assert.ok(unknown.views.every(view => view.hidden), 'unknown route must not fall back to Overview');

const lazyClick = navigationFixture({includeNetwork: false});
lazyClick.open('network');
assert.equal(lazyClick.hash, '', 'lazy navigation must leave the prior history entry intact');
assert.equal(new URL(lazyClick.assignedURL).searchParams.get('section'), 'network');

const remote = navigationFixture({query: '?server-settings&installation=remote', servedID: 'remote'});
remote.open('overview');
assert.equal(new URL(remote.assignedURL).searchParams.get('installation'), 'local');

const missingRemote = navigationFixture({query: '?server-settings&installation=unknown'});
assert.equal(missingRemote.assignedURL, '');
assert.equal(missingRemote.panel.hidden, true, 'do not show the local panel for a missing remote server');
assert.equal(missingRemote.notices.length, 1);

console.log('Server-settings navigation: 7 scenarios passed');
