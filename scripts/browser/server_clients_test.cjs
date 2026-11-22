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
assert.match(template, /data-server-clients-sort/);
assert.match(template, /data-client-filter-count="attention"/);
assert.match(template, /data-server-context-node-label/);
assert.match(template, /server_clients\.js/);

class Element {
  constructor(dataset = {}) {
    this.dataset = dataset;
    this.hidden = false;
    this.open = false;
    this.style = {};
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
  appendChild(element) { this.items?.push(element); }
}
function createSite(domain, usedBytes, invalidSSL = false) {
  const site = new Element({
    clientSiteDomain: domain, clientSiteUsed: String(usedBytes),
    clientSiteLimit: '10 GB', clientSiteProblem: 'false'
  });
  const progress = new Element();
  progress.one.span = {style: {}};
  progress.querySelector = selector => selector === 'span' ? progress.one.span : null;
  site.one['[data-client-site-progress]'] = progress;
  if (invalidSSL) site.one['[data-client-ssl-invalid]'] = new Element();
  site.all['[data-client-certificate]'] = [];
  return site;
}
function createClient(email, sites, debt, isOverdue) {
  const row = new Element({
    customerEmail: email, clientSearch: email + ' ' + sites.map(s => s.dataset.clientSiteDomain).join(' '),
    clientSiteCount: String(sites.length), clientDebtLabel: debt,
    clientAttention: String(isOverdue), clientOverdue: String(isOverdue),
    clientOverLimit: 'false'
  });
  row.all['[data-client-site-row]'] = sites;
  row.all['[data-client-pane]'] = ['sites','billing','account'].map(clientPane => new Element({clientPane}));
  row.all['[data-client-tab]'] = ['sites','billing','account'].map(clientTab => new Element({clientTab}));
  const footer = new Element();
  footer.one['[data-client-site-count]'] = new Element();
  footer.one['[data-client-show-all]'] = new Element();
  row.one['[data-client-site-footer]'] = footer;
  row.one['[data-client-no-sites]'] = new Element();
  row.one['[data-client-quota-summary]'] = new Element();
  row.one['[data-client-ssl-problems]'] = new Element();
  row.one['[data-client-error-count]'] = new Element();
  row.one['[data-client-domain-search]'] = new Element();
  return row;
}

const sites = Array.from({length: 32}, (_, index) =>
  createSite(index === 12 ? 'aika.sitebrush.com' : 'sample' + index + '.sitebrush.com',
    index === 2 ? 12000000000 : 1000000, index === 3));
const alice = createClient('alice@example.com', sites, '94.03 EUR', true);
const bob = createClient('bob@example.com', [createSite('bob.example.com', 1000000)], '0.00 EUR', false);
const search = new Element();
const sort = new Element();
sort.value = 'debt';
const badgeNames = ['all','attention','overdue','over-limit'];
const badges = badgeNames.map(clientFilterCount => new Element({clientFilterCount}));
const chip = new Element({serverClientFilter: 'all'});
chip.classList.add('is-active');
const list = new Element();
list.items = [];
const workspace = new Element();
workspace.all['[data-server-client-row]'] = [alice,bob];
workspace.all['[data-client-filter-count]'] = badges;
workspace.all['[data-client-account-form]'] = [];
workspace.one['[data-server-clients-search]'] = search;
workspace.one['[data-server-client-filter].is-active'] = chip;
workspace.one['[data-server-clients-sort]'] = sort;
workspace.one['[data-server-clients-list], .expense-simple-clients'] = list;
workspace.one['[data-clients-no-results]'] = new Element();
const document = {
  documentElement: {lang:'ru'},
  querySelectorAll: selector => selector === '[data-expense-server-panel]' ? [workspace] : [],
  querySelector: () => null,
  getElementById: () => null,
  addEventListener() {}
};
const window = {location:{hash:'',search:''}};
new Function('document','window','URLSearchParams','HTMLFormElement',script)(
  document,window,URLSearchParams,function HTMLFormElement(){}
);
const visibleSites = () => sites.filter(site => !site.hidden).length;
assert.equal(visibleSites(),10,'32 sites should show just 10 initially');
assert.equal(alice.one['[data-client-site-footer]'].one['[data-client-site-count]'].textContent,'10 / 32');
assert.equal(alice.dataset.clientProblemCount,'2','quota overage and SSL failure must be counted');
assert.match(alice.one['[data-client-quota-summary]'].textContent,/ \/ /);
assert.equal(badges.find(b=>b.dataset.clientFilterCount==='all').textContent,'2');
assert.equal(badges.find(b=>b.dataset.clientFilterCount==='overdue').textContent,'1');
assert.equal(list.items[0],alice,'debt sort should put the overdue client first');

search.value = 'aika';
workspace.events.input({target:{matches:selector=>selector==='[data-server-clients-search]'}});
assert.equal(visibleSites(),1,'global search should isolate a matching domain inside a large client');
assert.equal(alice.open,true,'domain search should expand its client');
assert.equal(bob.hidden,true);

search.value = '';
workspace.events.input({target:{matches:selector=>selector==='[data-server-clients-search]'}});
assert.equal(visibleSites(),10);
const more = {closest: selector => selector==='[data-client-show-all]' ? {
  closest: () => alice
} : null};
workspace.events.click({target:more});
assert.equal(visibleSites(),32,'Show all must expand the complete list');

const errors = new Element();
errors.checked = true;
alice.one['[data-client-errors-only]'] = errors;
workspace.events.change({target:{
  matches: selector => selector==='[data-client-errors-only]',
  closest: () => alice
}});
assert.equal(visibleSites(),2,'only problems must show over-quota and invalid-SSL sites');
console.log('Server client workspace: 32-site search, filters, quota, sorting and shared dialog structure passed');
