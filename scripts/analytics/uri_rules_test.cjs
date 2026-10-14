const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const {webcrypto} = require('node:crypto');
const source = fs.readFileSync(require('node:path').join(__dirname, '../../web/static/analytics.js'), 'utf8');

async function collectSignals(payload, address) {
    const windowEvents = new Map();
    const beacons = [];
    const storage = new Map();
    const browserStorage = {getItem: key => storage.get(key) || null, setItem: (key, content) => storage.set(key, content), removeItem: key => storage.delete(key)};
    let socket;
    class AnalyticsSocket {
        static OPEN = 1;
        static CLOSING = 2;
        constructor() { socket = this; this.handlers = new Map(); this.readyState = 0; this.bufferedAmount = 0; }
        addEventListener(name, handler) { this.handlers.set(name, handler); }
        send() {}
        close() { this.readyState = 3; }
    }
    const location = new URL(address);
    const browserWindow = {crypto: webcrypto, WebSocket: AnalyticsSocket, innerWidth: 1280, innerHeight: 800, scrollY: 0, addEventListener: (name, handler) => windowEvents.set(name, handler), setTimeout: () => 1, clearTimeout() {}, setInterval() {}};
    const environment = {window: browserWindow, location, WebSocket: AnalyticsSocket, crypto: webcrypto, localStorage: browserStorage, sessionStorage: browserStorage, performance: {now: () => 10000}, navigator: {userAgent: 'fixture browser', sendBeacon: (address, payload) => {beacons.push(payload); return true;}}, document: {readyState: 'loading', currentScript: null, referrer: '', body: {scrollHeight: 1000}, documentElement: {scrollHeight: 1000}, visibilityState: 'visible', addEventListener() {}, querySelectorAll: () => []}, URL, URLSearchParams, TextEncoder, Blob, console};
    vm.runInNewContext(source, environment);
    windowEvents.get('load')();
    socket.readyState = AnalyticsSocket.OPEN;
    socket.handlers.get('open')();
    for (let repetition = 0; repetition < 2; repetition++) socket.handlers.get('message')({data: JSON.stringify(payload)});
    windowEvents.get('hashchange')();
    windowEvents.get('pagehide')();
    const envelopes = await Promise.all(beacons.map(async beacon => JSON.parse(await beacon.text())));
    return envelopes;
}

(async function testURIRules() {
    if (process.argv[2]) {
        const envelopes = await collectSignals(JSON.parse(process.argv[2]), 'https://example.org/offer?plan=small&token=secret&utm_source=fixture#buy');
        process.stdout.write(JSON.stringify(envelopes));
        return;
    }
    const payload = {operation: 'rules', rules: [{name: 'configured_goal', match: '/offer?plan=small#buy'}, {name: 'wrong_goal', match: '/other'}, null, {name: 'invalid name', match: '/offer?plan=small#buy'}, {name: 'wrong_type', match: 3}]};
    const envelopes = await collectSignals(payload, 'https://example.org/offer?token=secret&plan=small&utm_source=fixture#buy');
    const goals = envelopes.flatMap(envelope => envelope.signals).filter(signal => signal.kind === 'goal');
    assert.equal(goals.length, 1, 'rules replies and hash events must not duplicate a goal');
    assert.equal(goals[0].name, 'configured_goal');
    for (const invalidRules of [null, {}, 'rules']) {
        const observed = await collectSignals({operation: 'rules', rules: invalidRules}, 'https://example.org/offer');
        assert.equal(observed.flatMap(envelope => envelope.signals).filter(signal => signal.kind === 'goal').length, 0);
    }
    console.log('Analytics URI rules regressions passed');
})().catch(failure => { console.error(failure); process.exitCode = 1; });
