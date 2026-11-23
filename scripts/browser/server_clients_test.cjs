'use strict';
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

const script = fs.readFileSync(path.join(__dirname, '../../web/static/server_clients.js'), 'utf8');
const template = fs.readFileSync(path.join(__dirname, '../../web/expenses.html'), 'utf8');
assert.equal((template.match(/id="clientQuotaModal"/g) || []).length, 1);
assert.equal((template.match(/id="clientDeleteModal"/g) || []).length, 1);
assert.doesNotMatch(template, /id="simple(?:Quota|Delete)/);
for (const action of ['update_site_quota', 'delete_site_now', 'billing_customer_schedule', 'registration_settings', 'demo_settings']) {
  assert.ok(template.includes(action), 'Missing original POST action: ' + action);
}
assert.match(template, /data-client-account-form/);
assert.match(template, /data-client-site-row/);
assert.doesNotMatch(template, /data-server-clients-sort|data-client-tab|data-client-errors-only/);
assert.match(template, /data-client-schedule-toggle/);
assert.match(template, /data-client-ssl-retry/);
assert.match(template, /data-client-filter-count="attention"/);
assert.match(template, /data-server-context-node-label/);
assert.match(template, /server_clients\.js/);

class Element {
  constructor(dataset = {}) {
    this.dataset = dataset;
    this.hidden = false;
    this.open = false;
    this.value = '';
    this.textContent = '';
    this.one = {};
    this.all = {};
    this.events = {};
    this.classes = new Set();
    this.classList = {
      add: name => this.classes.add(name),
      remove: name => this.classes.delete(name),
      contains: name => this.classes.has(name),
      toggle: (name, enabled) => {
        if (enabled === undefined ? !this.classes.has(name) : enabled) this.classes.add(name);
        else this.classes.delete(name);
      }
    };
  }
  querySelector(selector) { return this.one[selector] || null; }
  querySelectorAll(selector) { return this.all[selector] || []; }
  setAttribute(name, value) { this[name] = value; }
  addEventListener(name, callback) { this.events[name] = callback; }
}
function createSite(domain, invalidSSL = false) {
  const site = new Element({clientSiteDomain: domain});
  if (invalidSSL) {
    const certificate = new Element({clientCertificate: domain});
    site.all['[data-client-ssl-invalid]'] = [{closest: () => certificate}];
  }
  return site;
}
function createClient(email, sites, {overdue = false, overLimit = false} = {}) {
  const row = new Element({
    customerEmail: email, clientOverdue: String(overdue), clientOverLimit: String(overLimit)
  });
  row.all['[data-client-site-row]'] = sites;
  if (sites.length > 10) row.one['[data-client-show-all]'] = new Element();
  row.one['[data-client-no-sites]'] = new Element();
  row.one['[data-client-site-total]'] = new Element();
  row.one['[data-client-schedule]'] = new Element();
  row.one['[data-client-schedule]'].hidden = true;
  return row;
}

const sites = Array.from({length: 32}, (_, index) =>
  createSite(index === 12 ? 'aika.sitebrush.com' : 'sample' + index + '.sitebrush.com'));
const alice = createClient('alice@example.com', sites, {overdue: true});
const bob = createClient('bob@example.com', [createSite('bob.example.com')]);
const carol = createClient('carol@example.com', [createSite('carol.example.com')], {overLimit: true});
const sslSite = createSite('dan.example.com', true);
const dan = createClient('dan@example.com', [sslSite]);
const search = new Element();
search.matches = () => false;
const allCount = new Element();
const problemCount = new Element();
const allButton = new Element({serverClientFilter: 'all'});
const problemButton = new Element({serverClientFilter: 'attention'});
const workspace = new Element();
workspace.all['[data-server-client-row]'] = [alice, bob, carol, dan];
workspace.all['[data-server-client-filter]'] = [allButton, problemButton];
workspace.one['[data-client-filter-count="all"]'] = allCount;
workspace.one['[data-client-filter-count="attention"]'] = problemCount;
const accountForm = new Element();
const accountSelect = new Element();
accountSelect.selectedOptions = [{dataset:{}}];
const accountAction = new Element();
accountAction.options = ['queue_account','restore_account','delete_account_now'].map(value=>({value,disabled:false,hidden:false}));
const accountConfirm = new Element();
const accountNotice = new Element();
const accountActions = new Element();
accountForm.one['[data-client-account-select]'] = accountSelect;
accountForm.one['[data-client-account-domain]'] = new Element();
accountForm.one['[data-client-account-confirm-domain]'] = new Element();
accountForm.one['[data-client-account-email]'] = new Element();
accountForm.one['[data-client-account-action]'] = accountAction;
accountForm.one['[data-client-account-confirm]'] = accountConfirm;
accountForm.one['[data-client-account-protected]'] = accountNotice;
accountForm.one['[data-client-account-actions]'] = accountActions;
workspace.all['[data-client-account-form]'] = [accountForm];
workspace.one['[data-server-clients-search]'] = search;
workspace.one['[data-clients-no-results]'] = new Element();
const quotaDialog = new Element();
for (const name of ['domain','title','value']) quotaDialog.one['[data-client-quota-'+name+']'] = new Element();
const deleteDialog = new Element();
for (const name of ['domain','title','size','pending','deadline','confirm']) deleteDialog.one['[data-client-delete-'+name+']'] = new Element();
deleteDialog.one['[data-client-delete-reason]'] = new Element({reasonNonpayment:'Nonpayment',reasonRequest:'Request'});
const deleteAction = new Element();
deleteAction.options = ['queue_site','suspend_site_nonpayment','restore_site','delete_site_now']
  .map(value => ({value,disabled:false,hidden:false}));
deleteDialog.one['[data-client-delete-action]'] = deleteAction;
const dialogs = {'clientQuotaModal':quotaDialog,'clientDeleteModal':deleteDialog};
const document = {
  events:{},
  documentElement: {lang:'ru'},
  querySelectorAll: selector => selector === '[data-expense-server-panel]' ? [workspace] : [],
  querySelector: selector => selector==='#clientQuotaModal'?quotaDialog:
    selector==='#clientDeleteModal'?deleteDialog:null,
  getElementById: id => dialogs[id] || null,
  addEventListener(name,callback){this.events[name]=callback;}
};
const window = {location:{hash:'',search:''}};
new Function('document','window','URLSearchParams','HTMLFormElement',script)(
  document,window,URLSearchParams,function HTMLFormElement(){}
);
const visibleSites = () => sites.filter(site => !site.hidden).length;
assert.equal(visibleSites(),10,'32 sites should show just 10 initially');
assert.equal(alice.one['[data-client-site-total]'].textContent, '32 сайта');
assert.equal(allCount.textContent, '4');
assert.equal(problemCount.textContent, '3', 'overdue, quota and SSL must each flag a client');

search.value = 'aika';
workspace.events.input({target: search});
assert.equal(visibleSites(), 1, 'global search should isolate a matching domain inside a large client');
assert.equal(alice.open, true, 'domain search should expand its client');
assert.equal(bob.hidden, true);

search.value = 'alice@example.com';
workspace.events.input({target: search});
assert.equal(visibleSites(), 10, 'email search should keep the client site list');
assert.equal(bob.hidden, true);

const moreButton = alice.one['[data-client-show-all]'];
assert.equal(moreButton.textContent, 'Показать все 32 сайта ▾');
workspace.events.click({target: {closest: selector => {
  if (selector === '[data-client-show-all]') return moreButton;
  return selector === '[data-server-client-row]' ? alice : null;
}}});
assert.equal(visibleSites(), 32, 'Show all must expand the complete list');
workspace.events.input({target: {
  matches: selector => selector === '[data-client-domain-search]',
  closest: () => alice, value: 'sample31'
}});
assert.equal(visibleSites(), 1, 'local domain search includes sites outside the first ten');

search.value = '';
workspace.events.input({target: search});
workspace.events.click({target: {closest: selector =>
  selector === '[data-server-client-filter]' ? problemButton : null
}});
assert.equal(bob.hidden, true);
assert.ok([alice, carol, dan].every(row => !row.hidden));
workspace.events['site-certificate-renewed']({target: Object.assign(sslSite, {closest: () => dan}),
  detail: 'dan.example.com'});
assert.equal(dan.hidden, true, 'renewed SSL must clear the only problem');
assert.equal(problemCount.textContent, '2');

const scheduleButton = new Element();
scheduleButton.closest = () => alice;
workspace.events.click({target: {closest: selector => selector === '[data-client-schedule-toggle]' ? scheduleButton : null}});
assert.equal(alice.one['[data-client-schedule]'].hidden, false);
assert.equal(scheduleButton['aria-expanded'], 'true');

accountSelect.selectedOptions = [{dataset:{
  domain:'aika.sitebrush.com',email:'alice@example.com',protected:'false',pending:'false'
}}];
workspace.events.change({target:{
  matches: selector => selector==='[data-client-account-select]',
  closest: () => accountForm
}});
assert.equal(accountForm.one['[data-client-account-domain]'].value,'aika.sitebrush.com');
assert.equal(accountForm.one['[data-client-account-email]'].value,'alice@example.com');
assert.equal(accountActions.hidden,false,'unprotected account should expose actions');
assert.equal(accountAction.value,'queue_account');

accountSelect.selectedOptions = [{dataset:{
  domain:'aika.sitebrush.com',email:'alice@example.com',protected:'false',pending:'true'
}}];
workspace.events.change({target:{
  matches: selector => selector==='[data-client-account-select]',
  closest: () => accountForm
}});
assert.equal(accountAction.value,'restore_account','queued account should offer restoration');

accountSelect.selectedOptions = [{dataset:{
  domain:'aika.sitebrush.com',email:'alice@example.com',protected:'true',pending:'false'
}}];
workspace.events.change({target:{
  matches: selector => selector==='[data-client-account-select]',
  closest: () => accountForm
}});
assert.equal(accountActions.hidden,true,'protected account must not show a destructive action');
assert.equal(accountConfirm.disabled,true);

document.events.click({target:{closest:selector=>selector==='[data-client-quota-edit]' ?
  {dataset:{domain:'aika.sitebrush.com',quota:'30 GB'}}:null}});
assert.equal(quotaDialog.one['[data-client-quota-domain]'].value,'aika.sitebrush.com');
assert.equal(quotaDialog.one['[data-client-quota-value]'].value,'30 GB');

document.events.click({target:{closest:selector=>selector==='[data-client-site-delete]' ?
  {dataset:{domain:'aika.sitebrush.com',deletionReason:'',deletionDeadline:'',deletionSize:'42 MB'}}:null}});
assert.equal(deleteDialog.one['[data-client-delete-domain]'].value,'aika.sitebrush.com');
assert.equal(deleteAction.value,'queue_site');
assert.equal(deleteAction.options.find(option=>option.value==='restore_site').disabled,true);

document.events.click({target:{closest:selector=>selector==='[data-client-site-delete]' ?
  {dataset:{domain:'aika.sitebrush.com',deletionReason:'nonpayment',deletionDeadline:'2026-12-01',deletionSize:'42 MB'}}:null}});
assert.equal(deleteAction.value,'restore_site','queued site must offer restoration');
assert.equal(deleteDialog.one['[data-client-delete-pending]'].hidden,false);
assert.equal(deleteDialog.one['[data-client-delete-reason]'].textContent,'Nonpayment');
assert.equal(deleteAction.options.find(option=>option.value==='queue_site').disabled,true);

console.log('Server client workspace: 32-site search, filters, account safety and shared dialogs passed');
