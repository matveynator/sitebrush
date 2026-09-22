const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const path = require('node:path');
const source = fs.readFileSync(path.join(__dirname, '../../web/static/analytics.js'), 'utf8');
function storage() {
    const contents = new Map();
    return {getItem: key => contents.get(key) || null, setItem: (key, text) => contents.set(key, String(text)), removeItem: key => contents.delete(key)};
}
function browser(localStorage, sessionStorage, pathname, search = '?utm_source=habr&utm_campaign=launch&token=private', hash = '') {
    let clock = 2000;
    let identity = 0;
    const timers = [];
    const windows = {};
    const documents = {};
    const sockets = [];
    class Socket {
        static OPEN = 1;
        static CLOSING = 2;
        constructor() { this.readyState = 0; this.bufferedAmount = 0; this.listeners = {}; this.sent = []; sockets.push(this); }
        addEventListener(name, callback) { this.listeners[name] = callback; }
        send(encoded) { assert.ok(Buffer.byteLength(encoded) <= 4096); this.sent.push(JSON.parse(encoded)); }
        close() { this.readyState = 3; }
        open() { this.readyState = 1; this.listeners.open(); }
    }
    const window = {
        WebSocket: Socket, crypto: {getRandomValues: bytes => bytes.fill(++identity)},
        setTimeout: (callback, delay) => { timers.push({callback, delay}); return timers.length; },
        setInterval: () => 1, addEventListener: (name, callback) => { windows[name] = callback; },
        innerHeight: 600, scrollY: 0
    };
    const document = {readyState: 'complete', visibilityState: 'visible', referrer: 'https://habr.com/article?token=do-not-store', documentElement: {lang: 'ru', scrollHeight: 1200}, body: {scrollHeight: 1200}, addEventListener: (name, callback) => { documents[name] = callback; }};
    const location = {href: 'https://site.example' + pathname + search + hash, pathname, search, hash, origin: 'https://site.example', host: 'site.example', protocol: 'https:'};
    const environment = {window, document, localStorage, sessionStorage, navigator: {language: 'ru-RU'}, location, URL, URLSearchParams, performance: {now: () => clock}, crypto: window.crypto, WebSocket: Socket, Uint8Array, TextEncoder, Intl, Date, console};
    vm.runInNewContext(source, environment);
    timers.shift().callback();
    return {window, document, location, sockets, documents, windows, timers, advance: milliseconds => {clock += milliseconds;}};
}
const local = storage(), session = storage();
const first = browser(local, session, '/');
first.sockets[0].open();
assert.equal(first.sockets[0].sent.length, 1);
assert.equal(first.sockets[0].sent[0].campaign.Source, 'habr');
assert.equal(first.sockets[0].sent[0].referrer, 'https://habr.com/article');
assert.equal(first.sockets[0].sent[0].uri, '/');
first.advance(100);
first.window.SiteBrushAnalytics.action('download', '/download?secret=hidden');
assert.ok(session.getItem('sitebrush.analytics.pending'));
const pending = JSON.parse(session.getItem('sitebrush.analytics.pending'));
assert.equal(pending.actions[0].target, '/download');
assert.equal(pending.actions[0].count, 1);
assert.ok(!JSON.stringify(pending).includes('secret='));
const second = browser(local, session, '/download');
// Ensure different page view IDs even with this deterministic random source.
second.sockets[0].open();
assert.equal(second.sockets[0].sent[0].path, '/');
{
    assert.equal(second.sockets[0].sent[0].actions[0].name, 'download');
    second.advance(1200);
    second.timers.shift().callback();
    assert.equal(second.sockets[0].sent[1].path, '/download');
}
second.advance(2000);
second.window.SiteBrushAnalytics.action('copy-command', '/');
assert.equal(second.sockets[0].sent.at(-1).actions.at(-1).name, 'copy-command');
assert.equal(second.document.visibilityState, 'visible');

const uriSession = storage();
const fragment = browser(storage(), uriSession, '/', '?mode=download&token=private&utm_source=test', '#download');
fragment.sockets[0].open();
assert.equal(fragment.sockets[0].sent[0].uri, '/?mode=download#download');
fragment.advance(1200);
const namespacedAction = {
    closest() { return this; },
    getAttribute(name) { return name === 'sitebrush-data-analytics-action' ? 'download' : name === 'href' ? '/package.zip' : null; },
    hasAttribute() { return false; }
};
fragment.documents.click({target: namespacedAction});
assert.equal(fragment.sockets[0].sent.at(-1).actions.at(-1).name, 'download');
fragment.advance(1200);
fragment.location.hash = '#other';
fragment.windows.hashchange();
assert.equal(fragment.sockets[0].sent.at(-1).uri, '/?mode=download#other');
fragment.advance(1200);
const legacyAction = {
    closest() { return this; },
    getAttribute(name) { return name === 'data-analytics-action' ? 'legacy-action' : name === 'href' ? '/' : null; },
    hasAttribute() { return false; }
};
fragment.documents.click({target: legacyAction});
assert.equal(fragment.sockets[0].sent.at(-1).actions.at(-1).name, 'legacy-action');
console.log('browser analytics: bounded messages, URI cleaning, namespaced actions and compatibility passed');
