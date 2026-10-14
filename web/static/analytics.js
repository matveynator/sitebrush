/* A page owns a bounded event queue. Navigation never waits for delivery. */
(function sitebrushAnalytics() {
    'use strict';
    const controllerParameters = new URLSearchParams(location.search);
    if (window.SiteBrushAnalyticsStarted || ['analytics','visual','edit','login','register','profile','revisions','revision_preview','preview','page_password'].some(function controllerPage(flag) { return controllerParameters.has(flag); })) return;
    window.SiteBrushAnalyticsStarted = true;
    const publishedLayout = document.currentScript ? document.currentScript.getAttribute('data-analytics-layout') : '';
    const visitorKey = 'sitebrush.analytics.browser.v2';
    const pendingKey = 'sitebrush.analytics.pending.v2';
    const lifetime = 90 * 86400000;
    const model = {
        visitor: '', persistent: false, tab: '', session: '', view: '', sequence: 0, path: '', evidence: null,
        signals: [], limited: false, connection: null, lastSent: -10000, started: 0,
        batches: [], batchesRestored: false, deliveryTimer: null, lastDelivery: -10000,
        lastInteraction: 0, maximumScroll: 0, layout: '', device: '', retryCount: 0, nextConnect: 0,
        forms: new WeakMap(), sections: new WeakSet(), recentClicks: [], timers: [], uriRules: [], matchedRules: new Set(),
        replay: [], replayAllowed: false, replayPreferences: null, replayPolicySession: '', replayScroll: -1, replayScrollAt: 0, replayFields: new Map(), stopped: false
    };

    function randomIdentity() {
        const identityBytes = new Uint8Array(16);
        crypto.getRandomValues(identityBytes);
        return Array.from(identityBytes, function hexadecimal(identityByte) { return identityByte.toString(16).padStart(2, '0'); }).join('');
    }
    function validName(eventName) { return /^[A-Za-z0-9_-]{1,64}$/.test(eventName || ''); }
    function structuralKey(element) {
        if (!element || element.nodeType !== 1) return '';
        const explicitKey = element.getAttribute('data-analytics-key');
        if (validName(explicitKey)) return 'key:' + explicitKey;
        const segments = [];
        let currentElement = element;
        while (currentElement && currentElement.nodeType === 1 && segments.length < 12) {
            let ordinal = 1;
            let previousElement = currentElement.previousElementSibling;
            while (previousElement) { if (previousElement.tagName === currentElement.tagName) ordinal++; previousElement = previousElement.previousElementSibling; }
            segments.unshift(currentElement.tagName.toLowerCase() + ':' + ordinal);
            currentElement = currentElement.parentElement;
        }
        const elementKey = segments.join('>');
        return elementKey.length <= 180 ? elementKey : '';
    }
    function restoreIdentity() {
        model.visitor = randomIdentity();
        model.tab = randomIdentity();
        model.session = randomIdentity();
        try {
            const previousVisitor = JSON.parse(localStorage.getItem(visitorKey) || 'null');
            if (previousVisitor && /^[a-f0-9]{32}$/.test(previousVisitor.identity) && previousVisitor.expires > Date.now() && previousVisitor.expires <= Date.now() + lifetime) model.visitor = previousVisitor.identity;
            localStorage.setItem(visitorKey, JSON.stringify({identity: model.visitor, expires: Date.now() + lifetime}));
            model.persistent = true;
            model.tab = sessionStorage.getItem('sitebrush.analytics.tab') || model.tab;
            sessionStorage.setItem('sitebrush.analytics.tab', model.tab);
            const previousSession = JSON.parse(localStorage.getItem('sitebrush.analytics.session') || 'null');
            if (previousSession && /^[a-f0-9]{32}$/.test(previousSession.identity) && Date.now() - previousSession.last < 1800000) model.session = previousSession.identity;
            localStorage.setItem('sitebrush.analytics.session', JSON.stringify({identity: model.session, last: Date.now()}));
        } catch (storageError) { console.debug('SiteBrush analytics: temporary identity', storageError.name); }
    }
    function sourceEvidence() {
        const parameters = new URLSearchParams(location.search);
        function campaignField(fieldName) {
            const campaignText = parameters.get('utm_' + fieldName) || '';
            return /^[A-Za-z0-9 _.-]{0,64}$/.test(campaignText) ? campaignText : '';
        }
        let referrer = '';
        try { const referrerURL = new URL(document.referrer); if (referrerURL.protocol === 'http:' || referrerURL.protocol === 'https:') { referrerURL.username = ''; referrerURL.password = ''; referrerURL.hash = ''; for (const parameterName of Array.from(referrerURL.searchParams.keys())) { if (/^(token|access_token|id_token|secret|password|passwd|auth|authorization|session|sessionid|session_id|api_key|apikey|key|code|login_challenge|profile_resume|recovery_token|email_confirm|registration_form_token|recovery_code|login_code|password_confirmation_code|gclid|yclid|fbclid)$/i.test(parameterName)) referrerURL.searchParams.delete(parameterName); } referrer = referrerURL.href.slice(0,1024); } } catch (referrerError) { if (document.referrer) referrer = 'unknown-referrer'; }
        return {referrer: referrer, campaign: {Source: campaignField('source'), Medium: campaignField('medium'), Name: campaignField('campaign')}};
    }
    function enqueue(signal) {
        if (model.stopped) return;
        signal.at = Math.floor(performance.now() - model.started);
        if (model.signals.length >= 100) { model.limited = true; return; }
        model.signals.push(signal);
        if (model.connection && model.connection.readyState >= WebSocket.CLOSING && document.visibilityState === 'visible') connect();
        if (model.signals.length >= 20) flush(false);
    }
    function businessEvent(eventName) {
        if (!validName(eventName)) { console.error('SiteBrush analytics: invalid event name'); return; }
        enqueue({kind: 'goal', name: eventName});
    }
    function legacyAction(actionName) { businessEvent(actionName); }
    function formResult(formKey, success) {
        if (!validName(formKey) || typeof success !== 'boolean') { console.error('SiteBrush analytics: invalid form result'); return; }
        enqueue({kind: success ? 'form-success' : 'form-failed', form: 'key:' + formKey});
    }
    function observeConfiguredURI() {
        const parameters = new URLSearchParams(location.search);
        const privateParameters = new Set(['token','access_token','id_token','password','passwd','secret','auth','authorization','session','sessionid','session_id','api_key','apikey','key','code','gclid','yclid','fbclid','login_challenge','profile_resume','recovery_token','email_confirm','registration_form_token','recovery_code','login_code','password_confirmation_code']);
        for (const parameterName of Array.from(parameters.keys())) { if (privateParameters.has(parameterName.toLowerCase()) || parameterName.toLowerCase().startsWith('utm_')) parameters.delete(parameterName); }
        parameters.sort();
        const query = parameters.toString();
        const fragment = /^#[A-Za-z0-9._~-]{1,64}$/.test(location.hash || '') ? location.hash : '';
        const observedURI = location.pathname + (query ? '?' + query : '') + fragment;
        for (const rule of model.uriRules) { if (rule.match === observedURI && !model.matchedRules.has(rule.name)) { model.matchedRules.add(rule.name); enqueue({kind:'goal',name:rule.name}); } }
    }
    function recordInteraction() {
        if (performance.now() - model.lastInteraction > 1800000) { flush(true); startView(); }
        model.lastInteraction = performance.now();
        try { localStorage.setItem('sitebrush.analytics.session', JSON.stringify({identity:model.session,last:Date.now()})); } catch (sessionError) { /* Temporary sessions require no persistence. */ }
    }
    function recordScroll() {
        recordInteraction();
        const pageHeight = Math.max(document.documentElement.scrollHeight, document.body ? document.body.scrollHeight : 0, window.innerHeight);
        const depth = Math.min(100, Math.floor((window.scrollY + window.innerHeight) / pageHeight * 100 / 10) * 10);
        if (depth > model.maximumScroll) { model.maximumScroll = depth; enqueue({kind:'scroll',scroll:depth}); }
    }
    function recordClick(clickEvent) {
        recordInteraction();
        const clickedElement = clickEvent.target.closest ? clickEvent.target.closest('*') : null;
        const elementKey = structuralKey(clickedElement);
        if (!elementKey) return;
        enqueue({kind:'click',element:elementKey});
        const actionElement = clickedElement.closest('[sitebrush-data-analytics-action],[data-analytics-action],a[href]');
        if (actionElement) {
            const actionName = actionElement.getAttribute('sitebrush-data-analytics-action') || actionElement.getAttribute('data-analytics-action');
            if (actionName) businessEvent(actionName);
            if (actionElement.hasAttribute('download') || /\.(zip|gz|tgz|tar|bz2|xz|7z|rar|pdf|exe|msi|dmg|pkg|deb|rpm|appimage|apk|epub|docx?|xlsx?|pptx?)$/i.test(new URL(actionElement.href,location.href).pathname)) enqueue({kind:'download',name:'download',element:structuralKey(actionElement)});
            else if (actionElement.hasAttribute('href')) {
                try { if (new URL(actionElement.getAttribute('href'), location.href).origin !== location.origin) enqueue({kind:'external-link',name:'outbound',element:structuralKey(actionElement)}); } catch (addressError) { /* Invalid destinations are not useful analytics. */ }
            }
        }
        const currentTime = performance.now();
        model.recentClicks = model.recentClicks.filter(function recent(click) { return currentTime - click.at <= 2000; });
        model.recentClicks.push({element:elementKey,at:currentTime});
        if (model.recentClicks.length > 10) model.recentClicks.shift();
        if (model.recentClicks.filter(function sameElement(click) { return click.element === elementKey; }).length === 3) enqueue({kind:'rage-click',element:elementKey});
        if (clickedElement.closest('a,button,input,select,textarea,label,[role="button"],[role="link"],[contenteditable]')) return;
        if (typeof MutationObserver !== 'function' || model.timers.length >= 8) return;
        const initialView = model.view;
        const initialFocus = document.activeElement;
        const initialPath = location.pathname;
        let interfaceChanged = false;
        const changeObserver = new MutationObserver(function changed() { interfaceChanged = true; });
        changeObserver.observe(document.body, {childList:true,subtree:true,attributes:true});
        const timer = window.setTimeout(function checkClickResult() {
            changeObserver.disconnect();
            model.timers = model.timers.filter(function keepTimer(pendingTimer) { return pendingTimer !== timer; });
            if (model.view === initialView && !interfaceChanged && document.activeElement === initialFocus && location.pathname === initialPath) enqueue({kind:'dead-click',element:elementKey});
        }, 1000);
        model.timers.push(timer);
    }
    function formInteraction(formEvent) {
        recordInteraction();
        const fieldElement = formEvent.target;
        const formElement = fieldElement.form || (fieldElement.closest ? fieldElement.closest('form') : null);
        if (!formElement) return;
        const formKey = structuralKey(formElement);
        if (!formKey) return;
        let formState = model.forms.get(formElement);
        if (!formState) { formState = {viewed:false,started:false}; model.forms.set(formElement, formState); }
        if (!formState.viewed) { enqueue({kind:'form-view',form:formKey}); formState.viewed = true; }
        if (formEvent.type !== 'submit') {
            if (formEvent.type === 'input' && formState.lastField === fieldElement) return;
            formState.lastField = fieldElement;
            if (!formState.started) { enqueue({kind:'form-start',form:formKey}); formState.started = true; }
            const fieldKey = structuralKey(fieldElement);
            if (fieldKey) {
                const filled = ['checkbox','radio'].includes(fieldElement.type) ? fieldElement.checked : typeof fieldElement.value === 'string' && fieldElement.value.length > 0;
                enqueue({kind:'field',form:formKey,field:fieldKey,filled:filled});
            }
        } else { if (!formState.started) { enqueue({kind:'form-start',form:formKey}); formState.started=true; } enqueue({kind:'form-submit',form:formKey}); }
    }
    function observeSections() {
        if (typeof IntersectionObserver !== 'function') return;
        const sectionObserver = new IntersectionObserver(function reached(entries) {
            for (const entry of entries) {
                if (!entry.isIntersecting || model.sections.has(entry.target)) continue;
                model.sections.add(entry.target);
                const sectionKey = structuralKey(entry.target);
                if (entry.target.tagName === 'FORM') {
                    enqueue({kind:'form-view',form:sectionKey});
                    const formState = model.forms.get(entry.target) || {viewed:false,started:false};
                    formState.viewed = true; model.forms.set(entry.target, formState);
                } else enqueue({kind:'section',element:sectionKey});
                sectionObserver.unobserve(entry.target);
            }
        }, {threshold:0});
        for (const section of Array.from(document.querySelectorAll('main section,body > section,form,[data-analytics-section]')).slice(0,200)) sectionObserver.observe(section);
    }
    function layoutSignature() {
        if (/^[a-f0-9]{16}$/.test(publishedLayout || '')) return publishedLayout;
        let signature = 2166136261;
        if (!document.createTreeWalker || !document.body) return signature.toString(16);
        const parentOrdinals = new WeakMap();
        const parentSegments = new WeakMap();
        parentSegments.set(document.body,structuralKey(document.body).split('>'));
        const layoutWalker = document.createTreeWalker(document.body,1);
        let currentElement = layoutWalker.nextNode();
        let inspected = 0;
        while (currentElement && inspected < 1000) {
            inspected++;
            let siblings = parentOrdinals.get(currentElement.parentElement);
            if (!siblings) { siblings = new Map(); parentOrdinals.set(currentElement.parentElement,siblings); }
            const tagName = currentElement.tagName.toLowerCase();
            const ordinal = (siblings.get(tagName) || 0) + 1; siblings.set(tagName,ordinal);
            const ancestorSegments = parentSegments.get(currentElement.parentElement) || [];
            const segments = ancestorSegments.slice(-11).concat(tagName + ':' + ordinal);
            parentSegments.set(currentElement,segments);
            if (!currentElement.closest('[data-sitebrush-admin],script,style')) {
                const explicitKey = currentElement.getAttribute('data-analytics-key');
                let elementKey = validName(explicitKey) ? 'key:' + explicitKey : segments.join('>');
                if (elementKey.length > 180) elementKey = '';
                for (const character of elementKey) signature = Math.imul(signature ^ character.charCodeAt(0),16777619) >>> 0;
            }
            currentElement = layoutWalker.nextNode();
        }
        return signature.toString(16);
    }
    function deviceClass() {
        const userAgent = navigator.userAgent || '';
        const width = Math.min(window.innerWidth || 32768, (window.screen && window.screen.width) || 32768);
        if (/iPad|Tablet|PlayBook|Silk/i.test(userAgent) || (/Android/i.test(userAgent) && !/Mobile/i.test(userAgent))) return 'tablet';
        if (/Mobi|iPhone|iPod|Android/i.test(userAgent) || width < 768) return 'mobile';
        return 'desktop';
    }
    function enqueueReplay(action) {
        if (!model.replayAllowed || model.stopped) return;
        action.at = Math.floor(performance.now()-model.started);
        const previousAction=model.replay[model.replay.length-1];
        if (previousAction && ['resize','scroll'].includes(action.kind) && previousAction.kind===action.kind && action.at-previousAction.at<250) { model.replay[model.replay.length-1]=action;return; }
        if (model.replay.length>=100) { model.limited=true; return; }
        model.replay.push(action);
    }
    async function configureReplay(preferences) {
        if (model.replayPreferences && preferences && model.replayPolicySession===model.session && preferences.enabled===model.replayPreferences.enabled && preferences.sample===model.replayPreferences.sample) return;
        model.replayPolicySession=model.session;
        model.replayPreferences = preferences; model.replayAllowed=false;
        if (!preferences || !preferences.enabled || !crypto.subtle) return;
        try {
            const sessionIdentity = model.session;
            const digest = new Uint8Array(await crypto.subtle.digest('SHA-256',new TextEncoder().encode(sessionIdentity)));
            let sampleBucket = 0;
            for (const digestByte of digest.slice(0,8)) sampleBucket = (sampleBucket*256+digestByte)%100;
            if (sessionIdentity !== model.session || sampleBucket >= preferences.sample) return;
            model.replayAllowed=true;
            enqueueReplay({kind:'navigation',width:Math.min(32768,window.innerWidth),height:Math.min(32768,window.innerHeight)});
        } catch (sampleError) { console.error('SiteBrush replay: sampling unavailable',sampleError.name); }
    }
    function replayInteraction(interactionEvent) {
        if (!model.replayAllowed) return;
        const targetElement = interactionEvent.target;
        if (!targetElement || !targetElement.tagName) return;
        const elementKey = structuralKey(targetElement); if (!elementKey) return;
        let kind = interactionEvent.type === 'touchstart' ? 'touch' : interactionEvent.type === 'focusin' ? 'focus' : interactionEvent.type === 'input' ? 'field' : 'click';
        let filled=false;
        if (kind==='field') { filled=['checkbox','radio'].includes(targetElement.type) ? targetElement.checked : typeof targetElement.value==='string' && targetElement.value.length>0; if (model.replayFields.get(elementKey)===filled) return;model.replayFields.set(elementKey,filled); }
        enqueueReplay({kind:kind,element:elementKey,filled:filled});
    }
    function replayScroll() {
        if (!model.replayAllowed || performance.now()-model.replayScrollAt<1000) return;
        const extent = Math.max(1,document.documentElement.scrollHeight-window.innerHeight);
        const scrollPercent = Math.min(100,Math.max(0,Math.round(window.scrollY*100/extent)));
        if (Math.abs(scrollPercent-model.replayScroll)<5) return;
        model.replayScroll=scrollPercent;model.replayScrollAt=performance.now();enqueueReplay({kind:'scroll',scroll:scrollPercent});
    }
    function encodeBatch() {
        if (!model.signals.length && !model.replay.length) return null;
        const evidence = model.evidence;
        const envelope = {replay:model.replay.slice(0,20),replay_recording:model.replayAllowed,version:2,visitor:model.visitor,persistent:model.persistent,tab:model.tab,session:model.session,view:model.view,sequence:model.sequence + 1,path:model.path,uri:model.path,layout:model.layout,device:model.device,referrer:evidence.referrer,campaign:evidence.campaign,signals:model.signals.slice(0,20),limited:model.limited};
        while (new TextEncoder().encode(JSON.stringify(envelope)).length > 16384 && envelope.signals.length + envelope.replay.length > 1) { if (envelope.replay.length) envelope.replay.pop(); else envelope.signals.pop(); envelope.limited = true; }
        return envelope;
    }
    function persistBatches() {
        try {
            if (!model.batches.length) sessionStorage.removeItem(pendingKey);
            else sessionStorage.setItem(pendingKey,JSON.stringify(model.batches.map(function retainedBatch(batch) { return {expires:batch.expires,envelope:batch.envelope}; })));
        } catch (storageError) { console.debug('SiteBrush analytics: retry storage unavailable',storageError.name); }
    }
    function restoreBatches() {
        if (model.batchesRestored) return;
        model.batchesRestored = true;
        try {
            const encodedBatches = sessionStorage.getItem(pendingKey) || 'null';
            if (new TextEncoder().encode(encodedBatches).length > 65536) { model.limited = true; persistBatches(); return; }
            const retainedBatches = JSON.parse(encodedBatches);
            const candidates = Array.isArray(retainedBatches) ? retainedBatches : retainedBatches ? [retainedBatches] : [];
            for (const candidate of candidates.slice(0,4)) {
                if (!candidate.envelope || candidate.expires <= Date.now() || candidate.expires > Date.now()+1800000 || candidate.envelope.visitor !== model.visitor) continue;
                model.batches.push({expires:candidate.expires,envelope:candidate.envelope,sentAt:null});
            }
        } catch (storageError) { console.debug('SiteBrush analytics: retry storage invalid',storageError.name); }
        persistBatches();
    }
    function scheduleDelivery(delay) {
        if (model.stopped || model.deliveryTimer !== null) return;
        model.deliveryTimer = window.setTimeout(function deliverScheduledBatch() { model.deliveryTimer = null; deliverPendingBatch(); },delay);
    }
    function deliverPendingBatch() {
        while (model.batches.length && model.batches[0].expires <= Date.now()) { model.batches.shift(); model.limited = true; persistBatches(); }
        if (!model.batches.length || model.stopped) return;
        if (!model.connection || model.connection.readyState >= WebSocket.CLOSING) { connect(); return; }
        if (model.connection.readyState !== WebSocket.OPEN) return;
        const batch = model.batches[0];
        const elapsed = performance.now()-model.lastDelivery;
        const retryDelay = batch.sentAt === null ? 1000-elapsed : 15000-(performance.now()-batch.sentAt);
        if (retryDelay > 0) { scheduleDelivery(retryDelay); return; }
        if (model.connection.bufferedAmount >= 16384) { scheduleDelivery(1000); return; }
        batch.sentAt = performance.now(); model.lastDelivery = batch.sentAt;
        model.connection.send(JSON.stringify(batch.envelope));
        scheduleDelivery(15000);
    }
    function flush(finalDelivery) {
        if (!finalDelivery && performance.now()-model.lastSent < 1000) return;
        const envelope = encodeBatch();
        if (envelope) {
            if (model.batches.length >= 4) { model.limited = true; deliverPendingBatch(); return; }
            if (finalDelivery && (model.signals.length > envelope.signals.length || model.replay.length > envelope.replay.length)) envelope.limited = true;
            const retainedBatch = {expires:Date.now()+1800000,envelope:envelope,sentAt:null};
            if (new TextEncoder().encode(JSON.stringify([...model.batches,retainedBatch])).length > 65536) { model.limited = true; deliverPendingBatch(); return; }
            model.batches.push(retainedBatch);
            model.sequence = envelope.sequence; model.signals.splice(0,envelope.signals.length); model.replay.splice(0,envelope.replay.length); model.lastSent = performance.now();
            // A successful send only queues bytes in the browser, not in the worker.
            persistBatches();
        }
        if (finalDelivery && navigator.sendBeacon) {
            for (const batch of model.batches) {
                if (!navigator.sendBeacon('/_sitebrush/analytics',new Blob([JSON.stringify(batch.envelope)],{type:'application/json'}))) break;
            }
        } else deliverPendingBatch();
    }
    function connect() {
        if (model.connection && model.connection.readyState < WebSocket.CLOSING || model.stopped || performance.now() < model.nextConnect) return;
        model.nextConnect = performance.now()+15000;
        model.connection = new WebSocket((location.protocol === 'https:' ? 'wss://' : 'ws://')+location.host+'/_sitebrush/analytics');
        model.connection.addEventListener('open',function connected() {
            model.retryCount = 0;
            for (const batch of model.batches) batch.sentAt = null;
            flush(false); deliverPendingBatch();
        });
        model.connection.addEventListener('message',function receivedMessage(messageEvent) {
            try {
                const response = JSON.parse(messageEvent.data);
                if (response.operation === 'ack') {
                    if (response.accepted) {
                        model.batches = model.batches.filter(function unacknowledged(batch) { return batch.envelope.view !== response.view || batch.envelope.sequence !== response.sequence; });
                        persistBatches();
                        if (model.deliveryTimer !== null) { window.clearTimeout(model.deliveryTimer); model.deliveryTimer = null; }
                        scheduleDelivery(Math.max(0,1000-(performance.now()-model.lastDelivery)));
                    } else model.limited = true;
                } else if (response.operation === 'rules') {
                    model.uriRules = Array.isArray(response.rules) ? response.rules.filter(function uriRule(rule) { return rule && validName(rule.name) && typeof rule.match === 'string' && rule.match.startsWith('/'); }) : [];
                    observeConfiguredURI();
                    if (response.replay) configureReplay(response.replay);
                }
            } catch (messageError) { console.debug('SiteBrush analytics: invalid server reply',messageError.name); }
        });
        model.connection.addEventListener('close',function disconnected() {
            model.retryCount++; model.nextConnect = performance.now()+Math.min(60000,15000*model.retryCount);
            if (!model.stopped && model.batches.length) scheduleDelivery(Math.min(60000,15000*model.retryCount));
        });
        model.connection.addEventListener('error',function failed() { console.debug('SiteBrush analytics: connection unavailable'); });
    }
    function startView() {
        restoreIdentity(); restoreBatches(); model.view = randomIdentity(); model.sequence = 0;
        model.nextConnect = 0; model.retryCount = 0;
        model.path = location.pathname.slice(0,256); model.evidence = sourceEvidence();
        model.started = performance.now(); model.lastInteraction = model.started;
        model.maximumScroll = 0; model.signals = []; model.replay=[]; model.replayScroll=-1; model.replayScrollAt=0; model.replayFields=new Map(); model.limited = false; model.matchedRules = new Set();
        model.forms = new WeakMap(); model.sections = new WeakSet(); model.recentClicks = [];
        model.layout = layoutSignature(); model.device = deviceClass();
        enqueue({kind:'page'}); recordScroll(); observeSections(); observeConfiguredURI(); connect(); if (model.replayPreferences) configureReplay(model.replayPreferences);
    }
    function start() {
        if (!window.crypto || !window.WebSocket) return;
        startView();
        window.SiteBrushAnalytics = Object.freeze({action:legacyAction,event:businessEvent,formResult:formResult});
        document.addEventListener('click',recordClick,{capture:true,passive:true});
        for (const replayEventName of ['click','touchstart','focusin','input']) document.addEventListener(replayEventName,replayInteraction,{capture:true,passive:true});
        window.addEventListener('resize',function replayResize() { enqueueReplay({kind:'resize',width:Math.min(32768,window.innerWidth),height:Math.min(32768,window.innerHeight)}); });
        window.addEventListener('error',function replayError() { enqueueReplay({kind:'error'}); });
        document.addEventListener('focusin',formInteraction,{capture:true,passive:true});
        document.addEventListener('input',formInteraction,{capture:true,passive:true});
        document.addEventListener('submit',formInteraction,{capture:true,passive:true});
        let scrollScheduled = false;
        window.addEventListener('scroll',function scheduleScroll() { if (scrollScheduled) return; scrollScheduled = true; window.setTimeout(function sampleScroll() { scrollScheduled = false; recordScroll(); replayScroll(); },250); },{passive:true});
        window.addEventListener('popstate',function navigated() { flush(true); startView(); });
        window.addEventListener('hashchange',observeConfiguredURI);
        document.addEventListener('visibilitychange',function visibilityChanged() { if (document.visibilityState === 'hidden') flush(true); else connect(); });
        window.addEventListener('pagehide',function pageHidden() { flush(true); model.stopped = true; if (model.connection) model.connection.close(); });
        window.addEventListener('pageshow',function pageRestored(pageEvent) { if (pageEvent.persisted) { model.stopped = false; startView(); } });
        window.setInterval(function sendBatch() { if (document.visibilityState === 'visible') flush(false); },10000);
    }
    if (document.readyState === 'complete') window.setTimeout(start,0);
    else window.addEventListener('load',start,{once:true});
})();
