const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const path = require('node:path');
const source = fs.readFileSync(path.join(__dirname, '../../web/static/analytics.js'), 'utf8');
function storage() {
    const contents = new Map();
    return {getItem: key => contents.get(key) || null, setItem: (key, text) => contents.set(key, String(text)), removeItem: key => contents.delete(key)};
}
function browser(localStorage, sessionStorage, pathname) {
    let clock = 2000;
    let identity = 0;
    const timers = [], windows = {}, documents = {}, sockets = [], intervals = [], beacons = [];
    class Socket {
        static OPEN = 1; static CLOSING = 2;
        constructor() {this.readyState=0;this.bufferedAmount=0;this.listeners={};this.sent=[];sockets.push(this);}
        addEventListener(name,callback){this.listeners[name]=callback;}
        send(encoded){assert.ok(Buffer.byteLength(encoded)<=16384);this.sent.push(JSON.parse(encoded));}
        close(){this.readyState=3;}
        open(){this.readyState=1;this.listeners.open();}
    }
    const window={WebSocket:Socket,crypto:{getRandomValues:bytes=>bytes.fill(++identity)},setTimeout:(callback,delay)=>{timers.push({callback,delay});return timers.length;},setInterval:callback=>{intervals.push(callback);return intervals.length;},addEventListener:(name,callback)=>{windows[name]=callback;},innerHeight:600,innerWidth:1200,scrollY:0};
    const document={readyState:'complete',visibilityState:'visible',referrer:'https://github.com/project?token=referrer-secret',documentElement:{scrollHeight:1200},body:{scrollHeight:1200},querySelectorAll:()=>[],addEventListener:(name,callback)=>{const previousCallback=documents[name];documents[name]=previousCallback?function documentCallbacks(event){previousCallback(event);callback(event);}:callback;}};
    const location={href:'https://site.example'+pathname+'?token=url-secret&utm_source=github&utm_campaign=launch',pathname,search:'?token=url-secret&utm_source=github&utm_campaign=launch',origin:'https://site.example',host:'site.example',protocol:'https:'};
    vm.runInNewContext(source,{window,document,localStorage,sessionStorage,navigator:{sendBeacon:(address,blob)=>{beacons.push(blob);return true;}},location,URL,URLSearchParams,performance:{now:()=>clock},crypto:window.crypto,WebSocket:Socket,Uint8Array,TextEncoder,Date,console,Blob});
    timers.shift().callback();
    return {window,document,documents,windows,sockets,intervals,beacons,timers,advance:milliseconds=>{clock+=milliseconds;}};
}
(async function validateCollector() {
 const local=storage(),session=storage();const first=browser(local,session,'/');first.sockets[0].open();
 assert.equal(first.sockets[0].sent.length,1);
 assert.equal(first.sockets[0].sent[0].version,2);
 assert.equal(first.sockets[0].sent[0].campaign.Source,'github');
 assert.equal(first.sockets[0].sent[0].referrer,'https://github.com/project');
 assert.equal(first.sockets[0].sent[0].signals[0].kind,'page');
 for(let index=0;index<10;index++)first.window.SiteBrushAnalytics.event('purchase');
 assert.equal(first.sockets[0].sent.length,1,'custom events should batch');
 first.advance(10000);first.intervals[0]();
 assert.equal(first.sockets[0].sent.length,2);
 assert.equal(first.sockets[0].sent[1].signals.length,10);
 assert.ok(!JSON.stringify(first.sockets[0].sent).includes('secret'));
 assert.ok(!first.windows.mousemove,'mousemove must never be observed');
 const form={querySelectorAll:()=>[],nodeType:1,tagName:'FORM',parentElement:null,previousElementSibling:null,getAttribute:name=>name==='data-analytics-key'?'contact':null,closest:()=>form};
 const field={nodeType:1,tagName:'INPUT',parentElement:form,previousElementSibling:null,form,value:'password-secret',getAttribute:()=>null};
 first.documents.focusin({type:'focusin',target:field});first.documents.input({type:'input',target:field});first.documents.submit({type:'submit',target:form});
 first.window.SiteBrushAnalytics.formResult('contact',true);
 first.windows.pagehide();assert.equal(first.beacons.length,1);
 const finalBatch=JSON.parse(await first.beacons[0].text());
 assert.ok(finalBatch.signals.some(signal=>signal.kind==='form-submit'));
 assert.ok(finalBatch.signals.some(signal=>signal.kind==='form-success'));
 assert.ok(!JSON.stringify(finalBatch).includes('password-secret'));
 console.log('browser analytics: batching, safe acquisition, forms, beacon and privacy passed');
})().catch(error=>{console.error(error);process.exitCode=1;});

// Exercise the embedded renderer directly so pagination follows the production model.
const analyticsTemplate = fs.readFileSync(path.join(__dirname, '../../web/analytics.html'), 'utf8');
function securityElement() {
    return {
        children: [], style: {}, listeners: {}, textContent: '',
        replaceChildren() { this.children = []; },
        appendChild(child) { this.children.push(child); },
        append(...children) { this.children.push(...children); },
        prepend(...children) { this.children.unshift(...children); },
        setAttribute() {},
        addEventListener(name, handler) { this.listeners[name] = handler; }
    };
}
const countryRendererStart = analyticsTemplate.indexOf('    function renderCountries() {');
const countryRendererEnd = analyticsTemplate.indexOf('    if (allDaysButton)', countryRendererStart);
assert.ok(countryRendererStart > 0 && countryRendererEnd > countryRendererStart);
const countryChart = securityElement();
const countryModel = {selectedDay: null, page: 1};
const overallCountries = Array.from({length: 23}, (_, index) => ({Label: 'C' + index, Count: 100 - index}));
const countryContext = {
    countryChart, countryModel, overallCountries,
    countryIPs: {C0: {Count: 2, Known: true}, C1: {Count: 9, Known: false}},
    dailyCountryIPs: {'2026-10-11': {RU: {Count: 1, Known: true}}},
    activityCountries: {'2026-10-11': {RU: 3}},
    countryPeriod: securityElement(), allDaysButton: {textContent: 'All days'},
    calendar: {dataset: {emptyText: 'No data', eventLabel: 'Events', ipLabel: 'IP'}},
    countryName: country => country,
    document: {createElement: () => securityElement()}
};
vm.createContext(countryContext);
vm.runInContext(analyticsTemplate.slice(countryRendererStart, countryRendererEnd) + '\nrenderCountries();', countryContext);
assert.equal(countryChart.children.length, 11);
assert.match(countryChart.children[0].children[2].textContent, /Events: 100.*IP: 2/);
assert.match(countryChart.children[1].children[2].textContent, /IP: No data/);
countryChart.children.at(-1).children.at(-1).listeners.click();
assert.equal(countryModel.page, 2);
assert.equal(countryChart.children[0].children[0].textContent, 'C10');
countryChart.children.at(-1).children.at(-1).listeners.click();
assert.equal(countryChart.children.length, 4);
assert.equal(countryChart.children.at(-1).children.at(-1).disabled, true);
countryModel.selectedDay = '2026-10-11';
countryModel.page = 1;
countryContext.renderCountries();
assert.equal(countryChart.children.length, 2);
assert.match(countryChart.children[0].children[2].textContent, /Events: 3.*IP: 1/);
countryModel.selectedDay = 'missing-day';
countryContext.renderCountries();
assert.equal(countryChart.textContent, 'No data');

const searchFocusStart = analyticsTemplate.indexOf('(function setupSecuritySearchFocus()');
const searchFocusEnd = analyticsTemplate.indexOf('(function setupReturnToPreviousURI()', searchFocusStart);
let focusedSearch = false;
let scrolledSearch = false;
const searchFocusContext = {
    URLSearchParams,
    window: {location: {hash: '#local-blocks', search: '?local_query=192.0.2.1'}},
    document: {querySelector: selector => {
        assert.equal(selector, searchFocusContext.window.location.hash);
        return {
            querySelector: () => ({name: selector === '#local-blocks' ? 'local_query' : 'global_query', focus: options => {assert.equal(options.preventScroll, true); focusedSearch = true;}}),
            scrollIntoView: () => {scrolledSearch = true;}
        };
    }}
};
vm.runInNewContext(analyticsTemplate.slice(searchFocusStart, searchFocusEnd), searchFocusContext);
assert.equal(focusedSearch, true);
assert.equal(scrolledSearch, true);
focusedSearch = false;
scrolledSearch = false;
searchFocusContext.window.location = {hash: '#global-blocks', search: '?global_query=repository'};
vm.runInNewContext(analyticsTemplate.slice(searchFocusStart, searchFocusEnd), searchFocusContext);
assert.equal(focusedSearch, true);
assert.equal(scrolledSearch, true);
focusedSearch = false;
scrolledSearch = false;
searchFocusContext.window.location = {hash: '#global-blocks', search: '?analytics'};
vm.runInNewContext(analyticsTemplate.slice(searchFocusStart, searchFocusEnd), searchFocusContext);
assert.equal(focusedSearch, false);
assert.equal(scrolledSearch, false);
console.log('security analytics: country pagination, unique IP visibility and targeted search focus passed');

// Verify the annual scale contract across sparse and busy years: only maxima
// use the darkest green, and the legend covers every count without overlap.
const dashboardSource = fs.readFileSync(path.join(__dirname, '../../web/static/analytics_dashboard.js'), 'utf8');
const calendarHelpers = dashboardSource.slice(dashboardSource.indexOf('    function calendarActivityLevel('), dashboardSource.indexOf('    function renderVisitCalendar('));
const calendarContext = {};
vm.runInNewContext(calendarHelpers, calendarContext);
for (const annualMaximum of [0,1,2,3,4,7,100,1000]) {
    const intervals = calendarContext.calendarActivityRanges(annualMaximum);
    for (let visitorCount = 0; visitorCount <= annualMaximum; visitorCount++) {
        const level = calendarContext.calendarActivityLevel(visitorCount,annualMaximum);
        assert.equal(level === 4, annualMaximum > 0 && visitorCount === annualMaximum);
        const containing = intervals.filter(interval => visitorCount >= interval.minimumVisitors && visitorCount <= interval.maximumVisitors);
        assert.equal(containing.length,1,'legend must cover every count exactly once');
        assert.equal(containing[0].level,level,'legend must match the calendar cell');
    }
}
console.log('Analytics calendar: annual maximum, sparse years and exact legend ranges passed');

const signatureHelper = dashboardSource.slice(dashboardSource.indexOf('    function goalRulesSignature('), dashboardSource.indexOf('    function renderGoals('));
const goalContext = {};
vm.runInNewContext(signatureHelper, goalContext);
const flatForm = {Name:'Contact',Kind:'form-submit',Page:'/contact',Element:'key:contact'};
const editedForm = {Name:'Контакты',Primary:true,Steps:[{Name:'Send',Kind:'form-submit',Page:'/contact',Element:'key:contact'}]};
assert.equal(goalContext.goalRulesSignature(flatForm),goalContext.goalRulesSignature(editedForm),'flat and edited goals with the same rules must deduplicate');
assert.notEqual(goalContext.goalRulesSignature(flatForm),goalContext.goalRulesSignature({...flatForm,Page:'/other'}));
console.log('Initial goal suggestions: rule identity survives editor normalization and translated names');

// Exercise the constructor and its actual WebSocket payload at the server limits.
const constructorSource = dashboardSource.slice(dashboardSource.indexOf('    function inputField('), dashboardSource.indexOf('    renderFunnels(); renderGoals();'));
function goalConstructor(configuredGoals, suggestedGoals = []) {
    const constructorElements = new Map();
    const configurationSockets = [];
    const constructorModel = {goals: JSON.parse(JSON.stringify(configuredGoals)), suggestedGoals, saving: false};
    function constructorElement(identifier) {
        if (!constructorElements.has(identifier)) constructorElements.set(identifier, securityElement());
        return constructorElements.get(identifier);
    }
    class ConfigurationSocket {
        constructor() { this.listeners = {}; this.sent = []; configurationSockets.push(this); }
        addEventListener(name, callback) { this.listeners[name] = callback; }
        send(encodedConfiguration) { this.sent.push(JSON.parse(encodedConfiguration)); }
        close() {}
    }
    const constructorContext = {
        model: constructorModel,
        document: {getElementById: constructorElement},
        node: (tagName, text) => Object.assign(securityElement(), {tagName, textContent: text || ''}),
        label: translationKey => translationKey,
        dashboard: {dataset: {csrf: 'constructor-csrf'}},
        location: {protocol: 'https:', host: 'site.example'},
        WebSocket: ConfigurationSocket
    };
    vm.runInNewContext(constructorSource, constructorContext);
    constructorContext.renderGoals();
    return {model: constructorModel, element: constructorElement, sockets: configurationSockets};
}
for (const configuredCount of [0, ...Array.from({length: 25}, (_, goalIndex) => goalIndex + 8)]) {
    const configuredGoals = Array.from({length: configuredCount}, (_, goalIndex) => goalIndex < 8
        ? {Name: 'Goal ' + goalIndex, Primary: goalIndex === 0, Steps: [{Name: 'Viewed', Kind: 'page', Page: '/'}]}
        : {Name: 'Goal ' + goalIndex, Kind: 'event', Match: 'registration', Page: '/signup'});
    const constructor = goalConstructor(configuredGoals);
    assert.equal(JSON.stringify(constructor.model.goals), JSON.stringify(configuredGoals), 'rendering must preserve single-event goals');
    assert.equal(constructor.element('conversion-add-goal').disabled, configuredCount === 32);
    constructor.element('conversion-add-goal').listeners.click();
    assert.equal(constructor.model.goals.length, Math.min(configuredCount + 1, 32), 'manual additions must use all 32 slots');
    assert.equal(constructor.element('conversion-add-goal').disabled, constructor.model.goals.length === 32);
    constructor.element('conversion-save').listeners.click();
    constructor.sockets[0].listeners.open();
    const savedConfiguration = constructor.sockets[0].sent[0];
    assert.equal(savedConfiguration.operation, 'configure');
    assert.equal(savedConfiguration.csrf, 'constructor-csrf');
    assert.deepEqual(savedConfiguration.goals.slice(0, configuredCount), configuredGoals);
    assert.equal(savedConfiguration.goals.filter(goal => goal.Steps && goal.Steps.length).length, Math.min(configuredCount, 8), 'manual additions must not consume the separate eight-funnel capacity');
    if (configuredCount < 32) assert.equal(savedConfiguration.goals.at(-1).Kind, 'page');
}
const suggestedConstructor = goalConstructor(Array.from({length: 7}, (_, goalIndex) => ({Name: 'Existing ' + goalIndex, Kind: 'page', Page: '/'})), [
    {Name: 'Download', Steps: [{Kind: 'download'}]},
    {Name: 'Contact', Steps: [{Kind: 'form-submit'}]}
]);
suggestedConstructor.element('conversion-suggest-goals').listeners.click();
assert.equal(suggestedConstructor.model.goals.length, 8, 'automatic suggestions retain their smaller cap');
assert.equal(suggestedConstructor.element('conversion-suggest-goals').disabled, true);
assert.equal(suggestedConstructor.element('conversion-add-goal').disabled, false);
suggestedConstructor.element('conversion-add-goal').listeners.click();
suggestedConstructor.element('conversion-suggest-goals').listeners.click();
assert.equal(suggestedConstructor.model.goals.length, 9, 'the automatic cap must not restrict manual additions');

const formConstructor = goalConstructor([flatForm]);
const formEditor = formConstructor.element('conversion-goals').children[0];
formEditor.children[1].children.at(-1).listeners.click();
assert.equal(formConstructor.model.goals[0].Steps, undefined, 'a single-event rule cannot be removed as a funnel step');
formEditor.children.at(-1).listeners.click();
assert.equal(formConstructor.model.goals[0].Steps.length, 2);
assert.equal(goalContext.goalRulesSignature({...formConstructor.model.goals[0], Steps: formConstructor.model.goals[0].Steps.slice(0, 1)}), goalContext.goalRulesSignature(flatForm), 'adding a funnel step must preserve the original rule');
console.log('Analytics constructor: 32 manual goals, eight suggestions, unchanged save rules and explicit funnel steps passed');
