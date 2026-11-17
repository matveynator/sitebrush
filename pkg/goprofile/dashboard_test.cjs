const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const source = fs.readFileSync(path.join(__dirname, '../../web/static/server_profiler.js'), 'utf8');

function fixture() {
  const nodes = new Map();
  function element(selector) {
    if (!nodes.has(selector)) nodes.set(selector, {textContent: '', disabled: false, replaceChildren() {}, appendChild() {}, setAttribute() {}, classList: {toggle() {}}});
    return nodes.get(selector);
  }
  const root = {dataset: {csrf: 'fixture'}, querySelector: element, querySelectorAll: () => [], classList: {toggle() {}}};
  const handlers = new Map();
  const timers = new Map();
  const sockets = [];
  let timerID = 0;
  const window = {
    addEventListener: (name, callback) => handlers.set(name, callback),
    setTimeout: callback => {const identifier = ++timerID; timers.set(identifier, callback); return identifier;},
    clearTimeout: identifier => timers.delete(identifier)
  };
  class Socket {
    static OPEN = 1;
    constructor() {this.readyState = 0; this.commands = []; sockets.push(this);}
    open() {this.readyState = 1; this.onopen();}
    send(encoded) {
      const command = JSON.parse(encoded);
      this.commands.push(command);
      const report = {id: 'fresh-' + command.role, finished: new Date().toISOString(), cpu: 0, heap: 0, gc: 0, functions: []};
      const response = {role: command.role, busy: false, reports: [report]};
      if (command.action === 'report') response.report = report;
      queueMicrotask(() => this.onmessage({data: JSON.stringify(response)}));
    }
    close() {this.readyState = 3; this.delayedClose = this.onclose;}
  }
  vm.runInNewContext(source, {
    window, WebSocket: Socket, location: {protocol: 'https:', host: 'owner.example'},
    document: {querySelector: () => root, createElement: () => element('created')},
    console
  });
  async function settle() {
    for (let iteration = 0; iteration < 10; iteration++) {
      for (const socket of sockets) if (socket.readyState === 0) socket.open();
      await new Promise(resolve => setImmediate(resolve));
    }
  }
  return {nodes, handlers, timers, sockets, settle};
}

(async function testRestoration() {
  for (const pending of [true, false]) {
    const panel = fixture();
    if (!pending) await panel.settle();
    const previous = panel.sockets.slice();
    panel.handlers.get('pagehide')({persisted: true});
    await new Promise(resolve => setImmediate(resolve));
    assert.equal(panel.timers.size, 0, 'Suspending clears polling and request timers');
    assert.ok(previous.every(socket => socket.readyState === 3));
    panel.handlers.get('pageshow')({persisted: true});
    // A delayed close from the cached page must not cancel the replacement connection.
    previous.forEach(socket => {if (socket.delayedClose) socket.delayedClose();});
    await panel.settle();
    const restored = panel.sockets.filter(socket => !previous.includes(socket));
    assert.ok(restored.length >= 2, 'Restoration reconnects both roles');
    assert.ok(restored.flatMap(socket => socket.commands).some(command => command.action === 'report'), 'Restoration reloads retained history');
    panel.nodes.get('[data-profiler-start]').onclick();
    await panel.settle();
    assert.equal(restored.flatMap(socket => socket.commands).filter(command => command.action === 'start').length, 2, 'Recording remains usable after restoration');
    const contacts = panel.sockets.length;
    panel.handlers.get('pageshow')({persisted: true});
    await panel.settle();
    assert.equal(panel.sockets.length, contacts, 'Repeated pageshow does not create another poller');
  }
  console.log('Profiler dashboard back-forward restoration scenarios passed');
})().catch(error => {console.error(error); process.exitCode = 1;});
