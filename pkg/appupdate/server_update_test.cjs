const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const source = fs.readFileSync(path.join(__dirname, '../../web/static/server_update.js'), 'utf8');

class Element {
  constructor() {
    this.dataset = {};
    this.listeners = new Map();
    this.children = [];
    this.textContent = '';
    this.disabled = false;
    this.hidden = false;
    this.srcdoc = '';
  }
  addEventListener(eventName, callback) {
    const listeners = this.listeners.get(eventName) || [];
    listeners.push(callback);
    this.listeners.set(eventName, listeners);
  }
  dispatch(eventName) {
    if (eventName === 'click' && this.disabled) return;
    for (const callback of this.listeners.get(eventName) || []) callback({target: this});
  }
  append(child) { this.children.push(child); }
  replaceChildren() { this.children = []; }
}

function installationPanel(central) {
  const elements = new Map();
  function element(selector) {
    if (!elements.has(selector)) elements.set(selector, new Element());
    return elements.get(selector);
  }
  const root = element('root');
  root.dataset = {csrf: 'session-proof', waiting: 'Connecting', unavailable: 'Unavailable'};
  const announcements = central ? element('announcements') : null;
  if (announcements) {
    announcements.dataset = {recipientCount: '%d owners', campaignCounts: '%s: %d sent; %d waiting; %d skipped; %d failed'};
    announcements.querySelector = element;
    element('[data-announcement-cancel-label]').content = {textContent: 'Cancel'};
  }
  root.querySelector = selector => selector === '[data-announcements]' ? announcements : element(selector);
  root.querySelectorAll = () => ['current', 'available', 'downloading', 'failed', 'rolled_back'].map(stage => ({dataset: {stage}, textContent: stage}));
  const document = {querySelector: () => root, createElement: () => new Element(), createTextNode: text => ({textContent: text})};
  const windowEvents = new Map();
  const timers = [];
  const sockets = [];
  const window = {
    location: {href: 'https://owner.example/?server_update', protocol: 'https:'},
    addEventListener: (eventName, callback) => windowEvents.set(eventName, callback),
    setInterval: callback => { timers.push(callback); return timers.length; },
    clearInterval: () => {}, setTimeout: () => {}
  };
  class Socket extends Element {
    static OPEN = 1;
    constructor(address) { super(); this.address = address; this.readyState = 1; this.commands = []; sockets.push(this); }
    send(payload) { this.commands.push(JSON.parse(payload)); }
    close() { this.readyState = 3; this.dispatch('close'); }
    reply(response) { for (const callback of this.listeners.get('message')) callback({data: JSON.stringify(response)}); }
  }
  vm.runInNewContext(source, {document, window, WebSocket: Socket, URL, console});
  assert.equal(sockets.length, 1);
  const socket = sockets[0];
  assert.equal(socket.commands.length, 0, 'opening a page must not mutate or even send before contact');
  assert.equal(socket.address, 'wss://owner.example/?server_update_ws');
  socket.dispatch('open');
  assert.equal(socket.commands[0].action, 'status');
  socket.reply({State: {stage: 'available', current: {version: '1'}, available: {version: '21'}, manifest_hash: 'signed-release', rollback_available: false}});
  assert.equal(element('[data-update-current]').textContent, '1');
  assert.equal(element('[data-update-available]').textContent, '21');
  assert.equal(element('[data-update-install]').disabled, false);
  return {element, socket, timers, windowEvents};
}

const ordinary = installationPanel(false);
ordinary.element('[data-update-install]').dispatch('click');
assert.equal(ordinary.socket.commands.at(-1).action, 'upgrade');
assert.equal(ordinary.socket.commands.at(-1).csrf, 'session-proof');
assert.equal(ordinary.socket.commands.at(-1).manifest_hash, 'signed-release');
assert.equal(ordinary.element('[data-update-install]').disabled, true);
ordinary.timers[0]();
assert.equal(ordinary.socket.commands.length, 2, 'polling must not queue behind an outstanding operation');
ordinary.socket.reply({State: {stage: 'failed', current: {version: '1'}, previous: {version: '0'}, error: 'Checksum failed', rollback_available: true}});
assert.equal(ordinary.element('[data-update-error]').textContent, 'Checksum failed');
assert.equal(ordinary.element('[data-update-error]').hidden, false);
assert.equal(ordinary.element('[data-update-rollback]').disabled, false);
ordinary.element('[data-update-rollback]').dispatch('click');
assert.equal(ordinary.socket.commands.at(-1).action, 'rollback');
ordinary.socket.reply({State: {stage: 'rolled_back', current: {version: '1'}, cleanup_error: 'Old snapshot could not be removed', rollback_available: true}});
assert.equal(ordinary.element('[data-update-error]').textContent, 'Old snapshot could not be removed');
ordinary.windowEvents.get('pagehide')();
assert.equal(ordinary.element('[data-update-rollback]').disabled, true);

const central = installationPanel(true);
const previewButton = central.element('[data-announcement-preview]');
const sendButton = central.element('[data-announcement-send]');
assert.equal(sendButton.disabled, true);
previewButton.dispatch('click');
assert.equal(central.socket.commands.at(-1).action, 'campaign_preview');
assert.equal(central.socket.commands.at(-1).reason, 'security');
central.socket.reply({Preview: {Version: '21', Reason: 'security', ManifestHash: 'verified-campaign', Recipients: 7, HTML: '<h1>Important release</h1>'}});
assert.equal(sendButton.disabled, false);
assert.equal(central.element('[data-announcement-recipients]').textContent, '7 owners');
assert.equal(central.element('[data-announcement-email]').srcdoc, '<h1>Important release</h1>');
assert.equal(sendButton.listeners.get('click').length, 1, 'rendering must not duplicate command subscriptions');
const reasonSelector = central.element('[data-announcement-reason]');
reasonSelector.value = 'feature';
reasonSelector.dispatch('change');
assert.equal(sendButton.disabled, true, 'a changed reason requires a new preview');
previewButton.dispatch('click');
assert.equal(central.socket.commands.at(-1).reason, 'feature');
central.socket.reply({Preview: {Version: '21', Reason: 'security', ManifestHash: 'stale', Recipients: 7, HTML: 'stale'}});
assert.equal(sendButton.disabled, true, 'a stale preview cannot authorize a different campaign');
previewButton.dispatch('click');
central.socket.reply({Preview: {Version: '21', Reason: 'feature', ManifestHash: 'verified-campaign', Recipients: 7, HTML: 'feature'}});
sendButton.dispatch('click');
assert.equal(central.socket.commands.at(-1).action, 'campaign_start');
assert.equal(central.socket.commands.at(-1).manifest_hash, 'verified-campaign');
const campaign = {Version: '21', Sent: 1, Waiting: 2, Skipped: 3, Failed: 0, ExpiresAt: new Date(Date.now() + 60000).toISOString(), Canceled: false};
central.socket.reply({Campaigns: [campaign]});
assert.equal(sendButton.disabled, true, 'an existing campaign cannot be resent');
const campaignList = central.element('[data-announcement-campaigns]');
assert.equal(campaignList.children[0].textContent, '21: 1 sent; 2 waiting; 3 skipped; 0 failed');
campaignList.children[0].children[0].dispatch('click');
assert.equal(central.socket.commands.at(-1).action, 'campaign_cancel');
assert.equal(central.socket.commands.at(-1).version, '21');
console.log('Server update and central announcement interaction regressions passed');
