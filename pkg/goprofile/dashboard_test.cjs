const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const source = fs.readFileSync(path.join(__dirname, '../../web/static/server_profiler.js'), 'utf8');

function fixture(options = {}) {
  class Element {
    constructor(tag) {
      this.tagName = tag;
      this.textContent = '';
      this.children = [];
      this.attributes = {};
      this.style = {};
      this.dataset = {};
      this.disabled = false;
      this.classList = {toggle() {}};
    }
    replaceChildren() {this.children = []; this.textContent = '';}
    appendChild(child) {this.children.push(child);}
    setAttribute(name, content) {this.attributes[name] = content;}
    addEventListener(name, callback) {this[name + 'Callback'] = callback;}
    focus() {this.focused = true;}
    select() {this.selectedText = true;}
    showModal() {this.open = true;}
    close() {this.open = false; if (this.closeCallback) this.closeCallback();}
  }
  const nodes = new Map();
  function element(selector) {
    if (!nodes.has(selector)) nodes.set(selector, new Element('div'));
    return nodes.get(selector);
  }
  const tabs = ['functions', 'graph', 'code', 'memory', 'stages', 'network', 'goroutines'].map(view => {
    const button = new Element('button');
    button.dataset.view = view;
    return button;
  });
  const root = {dataset: {csrf: 'fixture'}, querySelector: element, querySelectorAll: () => tabs};
  const handlers = new Map();
  const timers = new Map();
  const sockets = [];
  const copied = [];
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
      const report = {
        runID: 'fixture-run', pid: command.role === 'public' ? 202 : 101, user:'fixture', system:'openbsd', architecture:'amd64', version:'test', started:new Date().toISOString(), network:{requests:command.role === 'public' ? 20 : 9}, id: 'fresh-' + command.role, finished: new Date().toISOString(), cpu: 100, heap: 200, gc: 0, matches: 2,
        functions: [
          {id: 1, name: 'main.handle', file: 'sitebrush.go', line: 10, kind: 'application', self: 20, total: 100, samples: 10},
          {id: 2, name: 'runtime.work', file: 'runtime.go', line: 20, kind: 'runtime', self: 80, total: 80, samples: 8}
        ],
        groups: [{kind: 'application', self: 20}, {kind: 'runtime', self: 80}]
      };
      const response = {protocol:1, role: command.role, runID: command.runID || 'fixture-run', busy: false, reports: [report]};
      if (command.action === 'capabilities') response.targets = options.targets || ['application', 'public'];
      if (command.action === 'export') response.text = 'Report for ' + command.role;
      if (options.publicUnavailable && command.role === 'public') {response.error = 'Public contact unavailable'; response.reports = [];}
      if (command.action === 'report') {
        if (command.view === 'function') {
          report.detail = {
            function: report.functions[command.function - 1],
            callers: command.function === 2 ? [report.functions[0]] : [],
            callees: command.function === 1 ? [report.functions[1]] : []
          };
        }
        response.report = report;
      }
      if(options.applicationStatusPending&&command.role==='application'&&command.action==='status')return;
      if(options.publicStatusPending&&command.role==='public'&&command.action==='status')return;
      if(options.applicationOverviewPending&&command.role==='application'&&command.action==='report'&&command.view==='overview')return;
      if(options.publicDisconnected&&command.role==='public'){queueMicrotask(()=>{this.readyState=3;this.onclose();});return;}
      queueMicrotask(() => this.onmessage({data: JSON.stringify(response)}));
    }
    close() {this.readyState = 3; this.delayedClose = this.onclose;}
  }
  vm.runInNewContext(source, {
    window, WebSocket: Socket, location: {protocol: 'https:', host: 'owner.example'},
    document: {querySelector: () => root, createElement: tag => new Element(tag)},
    navigator: {clipboard: {writeText: async text => {if(options.copyDenied)throw new Error('Denied');copied.push(text);}}},
    console
  });
  async function settle() {
    for (let iteration = 0; iteration < 10; iteration++) {
      for (const socket of sockets) if (socket.readyState === 0) socket.open();
      await new Promise(resolve => setImmediate(resolve));
    }
  }
  element('[data-profiler-open]').onclick();
  function descendants(parent) {
    return [parent, ...parent.children.flatMap(descendants)];
  }
  function findButton(caption) {
    return descendants(element('[data-profiler-content]')).find(node => node.tagName === 'button' && node.textContent.includes(caption));
  }
  return {nodes, handlers, timers, sockets, settle, tabs, findButton, copied, descendants};
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
    assert.ok(restored.flatMap(socket => socket.commands).some(command => command.action === 'report'), 'Restoration refreshes its new viewing session');
    panel.nodes.get('[data-profiler-start]').onclick();
    await panel.settle();
    assert.equal(restored.flatMap(socket => socket.commands).filter(command => command.action === 'start').length, 2, 'Recording remains usable after restoration');
    panel.nodes.get('[data-profiler-close]').onclick();
    await new Promise(resolve => setImmediate(resolve));
    assert.ok(restored.every(socket => socket.readyState === 3), 'Closing the dialog terminates viewing contacts for server cleanup');
    panel.nodes.get('[data-profiler-open]').onclick();
    await panel.settle();
    const contacts = panel.sockets.length;
    panel.handlers.get('pageshow')({persisted: true});
    await panel.settle();
    assert.equal(panel.sockets.length, contacts, 'Repeated pageshow does not create another poller');
  }
  const tablePanel = fixture();
  await tablePanel.settle();
  assert.ok(tablePanel.findButton('Собственное CPU'), 'Full statistics load without expanding another panel');
  const processStatistics=tablePanel.descendants(tablePanel.nodes.get('[data-profiler-overview]')).map(node=>node.textContent).join(' ');
  assert.ok(processStatistics.includes('101')&&processStatistics.includes('202'),'The first screen lists both process PIDs');
  tablePanel.findButton('Собственное CPU').onclick();
  await tablePanel.settle();
  let reports = tablePanel.sockets.flatMap(socket => socket.commands).filter(command => command.action === 'report');
  assert.equal(reports.at(-1).sort, 'self', 'Sorting requests the entire report before server paging');
  tablePanel.nodes.get('[data-profiler-kind]').onchange({target: {value: 'application'}});
  await tablePanel.settle();
  reports = tablePanel.sockets.flatMap(socket => socket.commands).filter(command => command.action === 'report');
  assert.equal(reports.at(-1).kind, 'application', 'Category filters go to the report owner');
  tablePanel.findButton('main.handle').onclick();
  await tablePanel.settle();
  let details = tablePanel.sockets.flatMap(socket => socket.commands).filter(command => command.view === 'function');
  assert.equal(details.at(-1).function, 1, 'Expanding a function requests sampled relationships');
  tablePanel.findButton('↳ Перейти').onclick();
  await tablePanel.settle();
  details = tablePanel.sockets.flatMap(socket => socket.commands).filter(command => command.view === 'function');
  assert.equal(details.at(-1).function, 2, 'Following a relationship opens the related function');
  tablePanel.nodes.get('[data-profiler-unit]').onchange({target: {value: 'ms'}});
  assert.ok(tablePanel.findButton('Собственное CPU'), 'Changing units keeps the table usable');
  tablePanel.nodes.get('[data-profiler-close]').onclick();
  await new Promise(resolve => setImmediate(resolve));
  assert.ok(tablePanel.sockets.every(socket => socket.readyState === 3), 'Expanded details retain no viewing contacts after close');
  for (const copyDenied of [false,true]) {
    const single = fixture({targets:['application'],copyDenied});
    await single.settle();
    assert.ok(single.sockets.flatMap(socket=>socket.commands).every(command=>command.role==='application'),'Single-process mode never contacts an absent public process');
    assert.equal(single.nodes.get('[data-profiler-copy]').disabled,false,'Whole-measurement text is prepared independently of detail views');
    await single.nodes.get('[data-profiler-copy]').onclick();
    if(copyDenied) {
      assert.equal(single.nodes.get('[data-profiler-copy-fallback]').hidden,false);
      assert.equal(single.nodes.get('[data-profiler-copy-text]').selectedText,true);
    } else {
      assert.ok(single.copied[0].includes('Report for application'));
      assert.ok(single.copied[0].includes('найти узкие места'));
    }
    single.nodes.get('[data-profiler-close]').onclick();
    await new Promise(resolve=>setImmediate(resolve));
    assert.equal(single.nodes.get('[data-profiler-copy-text]').value,'','Closing discards prepared report text');
  }
  const split = fixture();
  await split.settle();
  await split.nodes.get('[data-profiler-copy]').onclick();
  assert.ok(split.copied[0].includes('Report for public')&&split.copied[0].includes('Report for application'),'Export contains both source reports');
  assert.ok(split.copied[0].includes('CPU 0.00 мс'),'Report contains an aggregate CPU summary');
  assert.ok(split.copied[0].includes('Внешних HTTP-запросов: 20.'),'External request counts are not added to internal requests');
  for (const pending of ['applicationStatusPending','applicationOverviewPending','publicStatusPending']) {
    const parallel = fixture({[pending]:true});
    const availableRole = pending==='publicStatusPending'?'application':'public';
    const availablePID = availableRole==='public'?'202':'101';
    await parallel.settle();
    assert.ok(parallel.sockets.flatMap(socket=>socket.commands).some(command=>command.role===availableRole&&command.action==='report'&&command.view==='functions'),pending+' cannot block another process report');
    assert.ok(parallel.findButton('Собственное CPU'),pending+' cannot hide a ready function table');
    const readyStatistics=parallel.descendants(parallel.nodes.get('[data-profiler-overview]')).map(node=>node.textContent).join(' ');
    assert.ok(readyStatistics.includes(availablePID),pending+' cannot hide another process overview');
    parallel.nodes.get('[data-profiler-close]').onclick();
  }
  const disconnected = fixture({publicDisconnected:true});
  await disconnected.settle();
  assert.ok(disconnected.findButton('Собственное CPU'),'Application statistics remain visible when the public contact disconnects');
  assert.ok(disconnected.sockets.flatMap(socket=>socket.commands).some(command=>command.role==='application'&&command.action==='report'&&command.view==='functions'),'An available report is selected automatically');
  const partial = fixture({publicUnavailable:true});
  await partial.settle();
  await partial.nodes.get('[data-profiler-copy]').onclick();
  assert.ok(partial.copied[0].includes('Данные отсутствуют: Public contact unavailable'),'Expected unreachable process remains an explicit partial report');
  console.log('Profiler dashboard lifecycle, overview, export and function navigation scenarios passed');
})().catch(error => {console.error(error); process.exitCode = 1;});
