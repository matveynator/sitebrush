const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const root = path.resolve(__dirname, '../..');
const labels = JSON.parse(fs.readFileSync(path.join(root, 'web/translations.json'), 'utf8')).ru;
class Element {
  constructor(tag, text = '') { this.tagName = tag; this.textContent = text; this.children = []; this.dataset = {}; this.listeners = {}; this.disabled = false; }
  append(child) { if (child.tagName === '#fragment') this.children.push(...child.children); else this.children.push(child); }
  replaceChildren(...children) { this.children = []; children.forEach(child => this.append(child)); }
  addEventListener(name, handler) { this.listeners[name] = handler; }
  setAttribute(name, contents) { this[name] = contents; }
  click() { if (!this.disabled) this.listeners.click(); }
  scrollIntoView(options) { this.scrollCalls = (this.scrollCalls || 0) + 1; this.scrollOptions = options; }
}
const settings = new Element('div'); settings.dataset = {csrf: 'session-csrf', language: 'ru', mode: 'security'};
const feedback = new Element('p');
const journal = new Element('div'); journal.dataset.mailJournal = 'central';
const counter = new Element('span'); counter.dataset = {mailCount: 'RecoveryComplete', mailServer: 'central'};
const blocked = new Element('div'); const abuse = new Element('div');
const journalAnchor = new Element('section');
const selectorElements = new Map([
 ['[data-mail-settings]', [settings]], ['[data-mail-feedback]', [feedback]], ['[data-mail-journal]', [journal]], ['[data-mail-count]', [counter]], ['[data-mail-blocks]', [blocked]], ['[data-mail-abuse]', [abuse]], ['[data-mail-pagination]', []], ['time[datetime]', []]
]);
const document = {
 querySelector: selector => (selectorElements.get(selector) || [])[0],
 querySelectorAll: selector => selectorElements.get(selector) || [],
 getElementById: identifier => identifier === 'mail-journal-labels' ? new Element('script', JSON.stringify(labels)) : identifier === 'mail-monitor-0' ? journalAnchor : null,
 createElement: tag => new Element(tag),
 createDocumentFragment: () => new Element('#fragment')
};
let connection;
class WebSocket {
  static OPEN = 1;
  constructor(address) { this.address = address; this.handlers = {}; this.readyState = 1; this.sent = []; connection = this; }
  addEventListener(name, handler) { this.handlers[name] = handler; }
  send(payload) { this.sent.push(JSON.parse(payload)); }
}
vm.runInNewContext(fs.readFileSync(path.join(root, 'web/static/mail_journal.js'), 'utf8'), {document, WebSocket, URL, Intl, Date, console, navigator: {language: 'ru'}, window: {location: {href: 'https://sitebrush.com/?analytics&tab=security&mail_server=central#mail-monitor-0', protocol: 'https:'}, setTimeout: () => 1}});
assert.equal(connection.address.searchParams.get('tab'), 'security');
assert.ok(!connection.address.searchParams.has('expenses'), 'security journal stays in security analytics');
function descendants(element) { return [element, ...element.children.flatMap(descendants)]; }
function text(element) { return [element.textContent, ...element.children.map(text)].join(' '); }
function snapshot(stage, steps) {
 return {Central: true, IPDetails: {'127.0.0.1': {Location: labels.mail_journal_local_access, CanBlock: false}}, Blocks: [], Abuse: [], Servers: [{Server: {ID: 'central'}, MailOperations: [{ID: 'operation', OriginInstallationID: 'desktop', MessageID: 'message', FlowID: 'flow', Kind: 'recovery', Recipient: 'owner@example.com', Domain: 'localhost', InitiatorIP: '127.0.0.1', CreatedAt: '2026-10-21T12:00:00.000000000Z', UpdatedAt: '2026-10-21T12:01:00.000000000Z', Stage: stage, Steps: steps}], MailLastDay: {RecoveryComplete: stage === 'recovery_complete' ? 1 : 0}, MailLast90Days: {RecoveryComplete: stage === 'recovery_complete' ? 1 : 0}}]};
}
connection.handlers.message({data: JSON.stringify(snapshot('queued', []))});
assert.equal(journalAnchor.scrollCalls, 1, 'filter results remain visible after the first live snapshot');
assert.equal(journalAnchor.scrollOptions.block, 'start');
const cancel = descendants(journal).find(element => element.tagName === 'button' && element.textContent === labels.mail_journal_cancel);
assert.ok(cancel, 'queued mail offers cancellation'); cancel.click();
assert.equal(connection.sent[0].Action, 'cancel'); assert.equal(connection.sent[0].OperationID, 'operation'); assert.equal(connection.sent[0].CSRF, 'session-csrf');
assert.ok(!descendants(journal).some(element => element.tagName === 'button' && element.textContent === labels.mail_journal_block), 'local access never offers a spam block');
assert.ok(text(journal).includes(labels.mail_journal_local_access), 'local access is labeled instead of inventing a country');
const completedSteps = process.argv[2] ? JSON.parse(process.argv[2]) : [{stage: 'recovery_complete', created_at: '2026-10-21T12:01:00Z'}, {stage: 'accepted', accepted_by: 'recipient_smtp', created_at: '2026-10-21T12:00:30Z', smtp_code: '250', transport: 'transport=netchan netchan_sender=v2.1.0 protocol=quic network=udp destination=203.0.113.8:9876; transport=smtp mx=mx.example.com destination=203.0.113.9:25 protocol=starttls smtp_code=250'}];
connection.handlers.message({data: JSON.stringify(snapshot('recovery_complete', completedSteps))});
assert.equal(journal.children.length, 1, 'one operation renders as one row');
assert.equal(counter.textContent, '1 / 1');
assert.match(text(journal), /NetChan v2.1.0 \/ QUIC \/ UDP/); assert.match(text(journal), /mx.example.com/); assert.match(text(journal), /203.0.113.8:9876/);
assert.ok(!descendants(journal).some(element => element.tagName === 'button' && element.textContent === labels.mail_journal_cancel), 'completed mail cannot be cancelled');
assert.match(text(journal), /2026/); assert.ok(!text(journal).includes('2026-10-21T'), 'timestamps are formatted for humans');
assert.match(text(journal), /SMTP 250/);
assert.ok(!text(journal).includes('mail_monitor_stage_undefined'), 'JSON event stages use their wire names');
connection.handlers.message({data: JSON.stringify(snapshot('attempt', [null, {stage: 'attempt', route: 'local'}, {stage: 'accepted', accepted_by: 'central_relay', created_at: '2026-10-21T12:00:10Z'}, {stage: 'queued', created_at: null}]))});
assert.ok(!feedback.textContent.includes('TypeError'), 'missing timestamps do not break the journal');
assert.match(text(journal), new RegExp(labels.mail_journal_unknown));
assert.ok(!descendants(journal).some(element => element.tagName === 'button' && element.textContent === labels.mail_journal_cancel), 'an SMTP attempt without a known timestamp cannot be cancelled');
connection.handlers.message({data: JSON.stringify(snapshot('retry', [{stage: 'attempt', route: 'local', created_at: '2026-10-21T12:00:20Z'}, {stage: 'retry', route: 'local', created_at: '2026-10-21T12:00:30Z'}]))});
assert.ok(descendants(journal).some(element => element.tagName === 'button' && element.textContent === labels.mail_journal_cancel), 'a scheduled SMTP retry can be cancelled');
const publicSnapshot = snapshot('queued', []);
publicSnapshot.Servers[0].MailOperations[0].InitiatorIP = '8.8.8.8';
publicSnapshot.IPDetails['8.8.8.8'] = {Location: 'Mountain View, California, United States', CanBlock: true};
publicSnapshot.Abuse = [{IP: '8.8.8.8', OriginInstallationID: 'desktop', Attempts: 5, FirstAt: '2026-10-21T12:00:00Z', LastAt: '2026-10-21T12:01:00Z'}];
connection.handlers.message({data: JSON.stringify(publicSnapshot)});
assert.match(text(journal), /Mountain View/); assert.match(text(abuse), /Mountain View/);
const publicBlock = descendants(journal).find(element => element.tagName === 'button' && element.textContent === labels.mail_journal_block);
assert.ok(publicBlock, 'public senders offer a spam block in security analytics');
publicBlock.click(); assert.equal(connection.sent.at(-1).Action, 'block'); assert.equal(connection.sent.at(-1).IP, '8.8.8.8');
publicSnapshot.Blocks = [{IP: '8.8.8.8', Scope: 'global', CreatedAt: '2026-10-21T12:00:00Z'}];
connection.handlers.message({data: JSON.stringify(publicSnapshot)});
assert.match(text(blocked), /Mountain View/);
const publicUnblock = descendants(blocked).find(element => element.tagName === 'button' && element.textContent === labels.mail_journal_unblock);
assert.ok(publicUnblock); publicUnblock.click(); assert.equal(connection.sent.at(-1).Action, 'unblock');
connection.handlers.message({data: JSON.stringify({Error: '<script>untrusted diagnostic</script>'})});
assert.equal(feedback.textContent, '<script>untrusted diagnostic</script>'); assert.equal(feedback.children.length, 0);
assert.equal(journalAnchor.scrollCalls, 1, 'live updates never pull the reader back to the top of the journal');
settings.dataset.mode = 'statistics';
selectorElements.set('[data-mail-journal]', []);
selectorElements.set('[data-mail-blocks]', []);
selectorElements.set('[data-mail-abuse]', []);
vm.runInNewContext(fs.readFileSync(path.join(root, 'web/static/mail_journal.js'), 'utf8'), {document, WebSocket, URL, Intl, Date, console, navigator: {language: 'ru'}, window: {location: {href: 'https://sitebrush.com/?expenses', protocol: 'https:'}, setTimeout: () => 1}});
assert.ok(connection.address.searchParams.has('expenses'));
assert.ok(!connection.address.searchParams.has('analytics'), 'server counters do not request the security journal');
connection.handlers.message({data: JSON.stringify(snapshot('recovery_complete', []))});
assert.equal(counter.textContent, '1 / 1', 'statistics keep live delivery counters');
console.log('mail journal rendering, transport, counters and cancellation passed');
