const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const translations = JSON.parse(fs.readFileSync(path.join(__dirname, '../../web/translations.json'), 'utf8')).en;
const templateSource = fs.readFileSync(path.join(__dirname, '../../web/server_update.html'), 'utf8');
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
    this.style = {};
    this.attributes = new Map();
    this.scrollHeight = 100;
    this.clientHeight = 100;
    this.scrollTop = 0;
  }
  addEventListener(eventName, callback) {
    const listeners = this.listeners.get(eventName) || [];
    listeners.push(callback);
    this.listeners.set(eventName, listeners);
  }
  dispatch(eventName) {
    if (eventName === 'click' && this.disabled) return;
    return Promise.all((this.listeners.get(eventName) || []).map(callback => callback({target: this})));
  }
  append(...children) { this.children.push(...children); }
  setAttribute(name, text) { this.attributes.set(name, text); }
  removeAttribute(name) { this.attributes.delete(name); }
  click() { return this.dispatch('click'); }
  remove() {}
  replaceChildren() { this.children = []; }
}

function installationPanel(central) {
  const elements = new Map();
  function element(selector) {
    if (!elements.has(selector)) elements.set(selector, new Element());
    return elements.get(selector);
  }
  const root = element('root');
  root.dataset = {csrf: 'session-proof', waiting: 'Connecting', unavailable: 'Unavailable', logEmpty: 'No operation events yet.'};
  const announcements = central ? element('announcements') : null;
  if (announcements) {
    announcements.dataset = {recipientCount: '%d owners', campaignCounts: '%s: %d sent; %d waiting; %d skipped; %d failed'};
    announcements.querySelector = element;
    element('[data-announcement-cancel-label]').content = {textContent: 'Cancel'};
  }
  root.querySelector = selector => selector === '[data-announcements]' ? announcements : element(selector);
  root.querySelectorAll = selector => selector === '[data-stage]' ? ['current', 'available', 'preparing', 'downloading', 'verifying', 'inspecting', 'preparing_backup', 'backing_up', 'installing', 'restarting', 'checking', 'activating', 'completed', 'failed', 'rolling_back', 'rolled_back'].map(stage => ({dataset: {stage}, textContent: stage})) : Array.from(templateSource.matchAll(/data-label="([^"]+)">\{\{index \.T "([^"]+)"\}\}/g), match => ({dataset: {label: match[1]}, textContent: translations[match[2]]}));
  let now = Date.parse('2026-10-16T00:00:10Z');
  class TestDate extends Date { static now() { return now; } }
  let clipboardText = '';
  const navigator = {clipboard: {writeText: async text => { clipboardText = text; }}};
  const document = {body: new Element(), documentElement: {lang: 'en'}, querySelector: () => root, createElement: () => new Element(), createTextNode: text => ({textContent: text})};
  const windowEvents = new Map();
  const timers = [];
  const sockets = [];
  const window = {
    location: {href: 'https://owner.example/?server_update', protocol: 'https:'},
    addEventListener: (eventName, callback) => windowEvents.set(eventName, callback),
    setInterval: (callback, delay) => { timers.push({callback, delay}); return timers.length; },
    clearInterval: () => {}, setTimeout: callback => { windowEvents.set('reconnect', callback); }
  };
  class Socket extends Element {
    static OPEN = 1;
    constructor(address) { super(); this.address = address; this.readyState = 1; this.commands = []; sockets.push(this); }
    send(payload) { this.commands.push(JSON.parse(payload)); }
    close() { this.readyState = 3; this.dispatch('close'); }
    reply(response) { if (response.State) response.State = {backup_options: true, ...response.State}; for (const callback of this.listeners.get('message')) callback({data: JSON.stringify(response)}); }
  }
  vm.runInNewContext(source, {document, window, WebSocket: Socket, URL, Blob, navigator, Date: TestDate, console});
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
  return {element, socket, timers, windowEvents, sockets, navigator, clipboard: () => clipboardText, advance: milliseconds => { now += milliseconds; }, poll: () => timers.find(timer => timer.delay === 2000).callback(), tick: () => timers.find(timer => timer.delay === 1000).callback()};
}

async function testUpdatePanel() {
const ordinary = installationPanel(false);
ordinary.element('[data-update-install]').dispatch('click');
assert.equal(ordinary.socket.commands.at(-1).action, 'upgrade_with_options');
assert.equal(ordinary.socket.commands.at(-1).csrf, 'session-proof');
assert.equal(ordinary.socket.commands.at(-1).manifest_hash, 'signed-release');
assert.deepEqual(ordinary.socket.commands.at(-1).backup_policy, {databases: true, analytics: false, files: false, settings: false});
assert.equal(ordinary.element('[data-update-install]').disabled, true);
ordinary.poll();
assert.equal(ordinary.socket.commands.length, 2, 'polling must not queue behind an outstanding operation');
ordinary.socket.reply({State: {stage: 'failed', current: {version: '1'}, previous: {version: '0'}, error: 'Checksum failed', rollback_available: true}});
assert.equal(ordinary.element('[data-update-error]').textContent, 'Checksum failed');
assert.equal(ordinary.element('[data-update-error]').hidden, false);
assert.equal(ordinary.element('[data-update-rollback]').disabled, false);
ordinary.element('[data-update-rollback]').dispatch('click');
assert.equal(ordinary.socket.commands.at(-1).action, 'rollback_with_options');
ordinary.socket.reply({State: {stage: 'rolled_back', current: {version: '1'}, cleanup_error: 'Old snapshot could not be removed', rollback_available: true}});
assert.match(ordinary.element('[data-update-cleanup-error]').textContent, /Old snapshot could not be removed/);
ordinary.windowEvents.get('pagehide')();
assert.equal(ordinary.element('[data-update-rollback]').disabled, true);


const optionsPanel = installationPanel(false);
const fileCheckbox = optionsPanel.element('[data-backup-group="files"]');
assert.equal(fileCheckbox.checked, false, 'only the database backup is selected by default');
fileCheckbox.checked = true;
fileCheckbox.dispatch('change');
assert.equal(optionsPanel.element('[data-update-backup-warning]').hidden, false);
optionsPanel.element('[data-update-install]').dispatch('click');
assert.equal(optionsPanel.socket.commands.at(-1).backup_policy.files, true);
assert.equal(optionsPanel.socket.commands.at(-1).backup_policy.databases, true);
optionsPanel.socket.reply({State: {stage: 'available', current: {version: '1'}, available: {version: '21'}, backup_options: false}});
assert.equal(optionsPanel.element('[data-update-install]').disabled, true, 'old supervisor cannot silently ignore options');
assert.equal(optionsPanel.element('[data-update-launcher-warning]').hidden, false);
optionsPanel.socket.reply({State: {stage: 'completed', current: {version: '21'}, notification_code: 'contact', notification_error: 'dial tcp 46.4.4.46:9877: connect: connection refused'}});
assert.match(optionsPanel.element('[data-update-notification-error]').textContent, /notification service is unavailable/);
assert.doesNotMatch(optionsPanel.element('[data-update-notification-error]').textContent, /public HTTPS/);
optionsPanel.socket.reply({State: {stage: 'completed', current: {version: '21'}, notification_code: 'panel_url', notification_error: 'a public HTTPS owner domain is required'}});
assert.match(optionsPanel.element('[data-update-notification-error]').textContent, /public HTTPS/);
optionsPanel.socket.reply({State: {stage: 'completed', current: {version: '21'}}});
assert.equal(optionsPanel.element('[data-update-notification-error]').hidden, true);

const activePolicyPanel = installationPanel(false);
activePolicyPanel.socket.reply({State: {current: {version: '1'}, busy: true, stage: 'preparing_backup', backup_policy: {databases: true, analytics: true, files: false, settings: true}, progress: {action: 'upgrade', stage: 'preparing_backup'}}});
assert.equal(activePolicyPanel.element('[data-backup-group="files"]').checked, false, 'a reopened page must show the accepted operation policy');
assert.equal(activePolicyPanel.element('[data-backup-group="files"]').disabled, true);
activePolicyPanel.socket.reply({State: {current: {version: '21'}, busy: false, stage: 'completed', progress: {action: 'upgrade', stage: 'completed', finished_at: '2026-10-16T00:00:15Z'}}});
assert.equal(activePolicyPanel.element('[data-backup-group="files"]').checked, false, 'each subsequent operation defaults to a database backup');

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
const campaign = {Version: '21', Sent: 1, Waiting: 2, Skipped: 3, Failed: 0, ExpiresAt: '2027-01-01T00:00:00Z', Canceled: false};
central.socket.reply({Campaigns: [campaign]});
assert.equal(sendButton.disabled, true, 'an existing campaign cannot be resent');
const campaignList = central.element('[data-announcement-campaigns]');
assert.equal(campaignList.children[0].textContent, '21: 1 sent; 2 waiting; 3 skipped; 0 failed');
campaignList.children[0].children[0].dispatch('click');
assert.equal(central.socket.commands.at(-1).action, 'campaign_cancel');
assert.equal(central.socket.commands.at(-1).version, '21');
const noBackupPanel = installationPanel(false);
noBackupPanel.socket.reply({State: {stage: 'stopping', current: {version: '1'}, launcher_version: '1', busy: true, backup_policy: {databases: false, analytics: false, files: false, settings: false}, progress: {action: 'upgrade', stage: 'stopping', from_version: '1', target_version: '2', events: [], work: {}}}});
assert.equal(noBackupPanel.element('[data-update-steps]').children.length, 8, 'disabled backup stages must not appear as pending work');
assert.equal(noBackupPanel.element('[data-update-launcher-version]').textContent, 'Launcher: v.1');
const progressPanel = installationPanel(false);
const progress = {
  action: 'upgrade', from_version: '919', target_version: '920', stage: 'downloading',
  started_at: '2026-10-16T00:00:00Z', stage_at: '2026-10-16T00:00:05Z',
  work: {bytes: 50, total_bytes: 100, speed: 10},
  events: [{sequence: 1, at: '2026-10-16T00:00:00Z', stage: 'preparing', code: 'stage_started'},
    {sequence: 2, at: '2026-10-16T00:00:05Z', stage: 'downloading', code: 'stage_started', detail: '<img src=x onerror=alert(1)>'}]
};
const downloading = {stage: 'downloading', current: {version: '919'}, busy: true, operation: 'operation-one', progress};
progressPanel.socket.reply({ServerVersion: '919', State: downloading});
assert.equal(progressPanel.element('[data-update-percent]').textContent, '13%');
assert.equal(progressPanel.element('[data-update-progress]').attributes.get('aria-valuenow'), '13');
assert.equal(progressPanel.element('[data-update-elapsed]').textContent, '00:00:10');
assert.equal(progressPanel.element('[data-update-stage-elapsed]').textContent, '00:00:05');
assert.equal(progressPanel.element('[data-update-remaining]').textContent, 'This step: about 00:00:05');
const logBefore = progressPanel.element('[data-update-log]').textContent;
assert.match(logBefore, /<img src=x onerror=alert\(1\)>/);
assert.equal(progressPanel.element('[data-update-log]').children.length, 0, 'diagnostics must be rendered as text');
progressPanel.socket.close();
assert.equal(progressPanel.element('[data-update-percent]').textContent, '13%', 'disconnect must preserve confirmed progress');
assert.match(progressPanel.element('[data-update-connection]').textContent, /reconnecting automatically/i);
progressPanel.advance(5000);
progressPanel.tick();
assert.equal(progressPanel.element('[data-update-elapsed]').textContent, '00:00:15');
assert.equal(progressPanel.element('[data-update-remaining]').textContent, 'Calculating…', 'stale transfer speed cannot promise an ETA');
progressPanel.windowEvents.get('reconnect')();
const reopened = progressPanel.sockets.at(-1);
reopened.dispatch('open');
progress.events.push({sequence: 3, at: '2026-10-16T00:00:12Z', stage: 'downloading', code: 'stage_finished'});
reopened.reply({ServerVersion: '919', State: downloading});
assert.equal(progressPanel.element('[data-update-log]').textContent.split('\n').length, 4);
reopened.reply({ServerVersion: '919', State: downloading});
assert.equal(progressPanel.element('[data-update-log]').textContent.split('\n').length, 4, 'repeated snapshots cannot duplicate journal rows');
await progressPanel.element('[data-update-copy]').dispatch('click');
assert.equal(progressPanel.clipboard(), progressPanel.element('[data-update-log]').textContent);
progressPanel.navigator.clipboard.writeText = async () => { throw Error('clipboard unavailable'); };
await progressPanel.element('[data-update-copy]').dispatch('click');
assert.equal(progressPanel.element('[data-update-copy-status]').textContent, translations.update_ui_copy_failed);
progressPanel.element('[data-update-download]').dispatch('click');
const completed = {...progress, stage: 'completed', finished_at: '2026-10-16T00:00:15Z'};
reopened.reply({ServerVersion: '919', State: {...downloading, busy: false, current: {version: '920'}, progress: completed}});
assert.equal(progressPanel.element('[data-update-percent]').textContent, '99%', 'journal version alone does not confirm the running server');
assert.equal(progressPanel.element('[data-update-selected]').hidden, false);
reopened.reply({ServerVersion: '920', State: {...downloading, busy: false, current: {version: '920'}, progress: completed}});
assert.equal(progressPanel.element('[data-update-percent]').textContent, '100%');
progressPanel.advance(5000); progressPanel.tick();
assert.equal(progressPanel.element('[data-update-elapsed]').textContent, '00:00:15', 'finished time must stop the timer');
const recovered = {...completed, stage: 'rolled_back', failed_stage: 'installing', events: [...progress.events, {sequence: 4, at: '2026-10-16T00:00:13Z', stage: 'rolling_back', code: 'stage_started'}]};
reopened.reply({ServerVersion: '919', State: {...downloading, busy: false, stage: 'rolled_back', operation_error: 'unsafe backup entry download/latest', progress: recovered}});
assert.equal(progressPanel.element('[data-update-operation]').textContent, translations.update_ui_auto_rollback);
assert.equal(progressPanel.element('[data-update-percent]').textContent, '100%');
assert.match(progressPanel.element('[data-update-error]').textContent, /unsafe backup entry/);
const restoredPanel = installationPanel(false);
restoredPanel.socket.reply({ServerVersion: '920', State: {...downloading, busy: false, current: {version: '920'}, progress: completed}});
assert.equal(restoredPanel.element('[data-update-percent]').textContent, '100%', 'a fresh page must recover the retained result');
assert.equal(restoredPanel.element('[data-update-elapsed]').textContent, '00:00:15');
restoredPanel.socket.reply({ServerVersion: '920', State: {...downloading, checking: true, current: {version: '920'}, progress: completed}});
assert.equal(restoredPanel.element('[data-update-percent]').textContent, '100%', 'checking the release catalogue must not undo a completed operation');
restoredPanel.socket.reply({ServerVersion: '920', State: {...downloading, busy: false, current: {version: '920'}, progress: completed, notification_error: 'dial tcp:9877 refused'}});
assert.equal(restoredPanel.element('[data-update-notification-error]').hidden, false);
assert.match(restoredPanel.element('[data-update-notification-error]').textContent, /9877 refused/);
assert.equal(restoredPanel.element('[data-update-error]').hidden, true, 'notification failure must not become an operation failure');
const manualProgress = {...completed, action: 'rollback', from_version: '920', target_version: '919', stage: 'preparing', finished_at: '', work: {}, events: []};
restoredPanel.socket.reply({ServerVersion: '920', State: {...downloading, progress: manualProgress}});
assert.equal(restoredPanel.element('[data-update-percent]').textContent, '0%');
assert.equal(restoredPanel.element('[data-update-steps]').children.length, 9, 'manual rollback must omit download and preflight stages');
assert.equal(restoredPanel.element('[data-update-log]').textContent.includes('<img'), false, 'new operation cannot retain the previous journal');
const waitingPanel = installationPanel(false);
waitingPanel.poll();
assert.equal(waitingPanel.element('[data-update-install]').disabled, false, 'background status requests must not block controls');
waitingPanel.element('[data-update-install]').dispatch('click');
assert.equal(waitingPanel.socket.commands.at(-1).action, 'status', 'only one request may be in flight');
assert.equal(waitingPanel.element('[data-update-install]').disabled, true);
waitingPanel.socket.reply({State: {stage: 'available', current: {version: '1'}, available: {version: '22'}, manifest_hash: 'changed-release'}});
assert.equal(waitingPanel.socket.commands.at(-1).action, 'upgrade_with_options');
assert.equal(waitingPanel.socket.commands.at(-1).manifest_hash, 'signed-release', 'queued confirmation must keep the release that was displayed at click time');
const commandErrorPanel = installationPanel(false);
commandErrorPanel.element('[data-update-install]').dispatch('click');
commandErrorPanel.socket.reply({Error: 'version operation is already running'});
assert.equal(commandErrorPanel.element('[data-update-error]').hidden, false);
assert.equal(commandErrorPanel.element('[data-update-error]').textContent, 'version operation is already running');
commandErrorPanel.poll();
commandErrorPanel.socket.reply({State: {stage: 'completed', current: {version: '21'}}});
assert.equal(commandErrorPanel.element('[data-update-error]').hidden, true, 'successful status must clear a resolved command failure');
assert.equal(commandErrorPanel.element('[data-update-error]').textContent, '');
commandErrorPanel.socket.reply({Error: 'temporary command failure'});
commandErrorPanel.socket.reply({Error: '', State: {stage: 'failed', current: {version: '21'}, error: 'backup failed'}});
assert.equal(commandErrorPanel.element('[data-update-error]').textContent, 'backup failed', 'clearing a command failure must preserve the durable state error');
commandErrorPanel.socket.reply({State: {stage: 'rolled_back', current: {version: '1'}, operation_error: 'migration failed'}});
assert.equal(commandErrorPanel.element('[data-update-error]').textContent, 'migration failed', 'automatic rollback must retain the operation failure');
commandErrorPanel.socket.reply({Campaigns: []});
assert.equal(commandErrorPanel.element('[data-update-error]').textContent, 'migration failed', 'successful responses without state must preserve existing durable errors');
console.log('Server update progress, reconnection, journal and announcement regressions passed');
}
testUpdatePanel().catch(error => { console.error(error); process.exitCode = 1; });
