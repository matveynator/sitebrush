/* Each page owns a bounded, cumulative observation. Delivery never gates navigation. */
(function sitebrushAnalytics() {
    'use strict';
    if (window.SiteBrushAnalyticsStarted) return;
    window.SiteBrushAnalyticsStarted = true;
    const storageKey = 'sitebrush.analytics.browser.v1';
    const lifetime = 90 * 86400000;
    const model = {
        tab: '', session: '', actions: [], sentActions: '', sentURI: '', visitor: '', persistent: false, view: '', sequence: 0,
        activeMilliseconds: 0, maximumScroll: 0, lastSample: 0,
        lastInteraction: 0, lastScrollSample: 0, connection: null,
        pendingTimer: null, deliveryAfter: 0, lastSent: 0, sentActive: 0, sentScroll: 0, firstSent: false, stopped: false, retryCount: 0
    };

    function boundedText(text, maximum) {
        let result = ''; let size = 0;
        for (const character of String(text || '')) {
            size += new TextEncoder().encode(character).length;
            if (size > maximum) break;
            result += character;
        }
        return result;
    }

    function createIdentity() {
        const randomBytes = new Uint8Array(16);
        crypto.getRandomValues(randomBytes);
        return Array.from(randomBytes, function hexadecimal(randomByte) {
            return randomByte.toString(16).padStart(2, '0');
        }).join('');
    }

    function restoreVisitor() {
        const currentTime = Date.now();
        model.visitor = createIdentity();
        try {
            const storedText = localStorage.getItem(storageKey);
            const storedVisitor = storedText ? JSON.parse(storedText) : null;
            if (storedVisitor && /^[a-f0-9]{32}$/.test(storedVisitor.identity) &&
                storedVisitor.expires > currentTime && storedVisitor.expires <= currentTime + lifetime) {
                model.visitor = storedVisitor.identity;
            } else {
                localStorage.setItem(storageKey, JSON.stringify({ identity: model.visitor, expires: currentTime + lifetime }));
            }
            model.persistent = true;
        } catch (storageError) {
            console.debug('SiteBrush analytics: browser history unavailable', storageError.name);
        }
    }

    function restoreSession() {
        const now = Date.now();
        try {
            model.tab = sessionStorage.getItem('sitebrush.analytics.tab') || createIdentity();
            sessionStorage.setItem('sitebrush.analytics.tab', model.tab);
            const previous = JSON.parse(localStorage.getItem('sitebrush.analytics.session') || 'null');
            model.session = previous && now - previous.last < 1800000 ? previous.identity : createIdentity();
            localStorage.setItem('sitebrush.analytics.session', JSON.stringify({identity: model.session, last: now}));
        } catch (sessionError) {
            model.tab = model.tab || createIdentity();
            model.session = model.session || createIdentity();
            console.debug('SiteBrush analytics: temporary session', sessionError.name);
        }
    }

    function currentURI() {
        const parameters = new URLSearchParams(location.search);
        const sensitive = new Set(['token', 'access_token', 'id_token', 'secret', 'password', 'passwd', 'auth', 'authorization', 'session', 'sessionid', 'session_id', 'api_key', 'apikey', 'key', 'code', 'gclid', 'yclid', 'fbclid']);
        for (const key of Array.from(parameters.keys())) {
            const lower = key.toLowerCase();
            if (sensitive.has(lower) || lower.startsWith('utm_')) parameters.delete(key);
        }
        parameters.sort();
        const query = parameters.toString();
        const fragment = /^#[A-Za-z0-9._~-]{1,64}$/.test(location.hash || '') ? location.hash : '';
        return location.pathname.slice(0, 256) + (query ? '?' + query : '') + fragment;
    }

    function cleanTarget(address) {
        try {
            const destination = new URL(address, location.href);
            if (destination.protocol !== 'http:' && destination.protocol !== 'https:') return destination.protocol;
            return (destination.origin === location.origin ? '' : destination.hostname) + destination.pathname.slice(0, 120);
        } catch (targetError) { return ''; }
    }

    function recordAction(name, target) {
        if (!/^[a-zA-Z0-9_-]{1,64}$/.test(name)) return;
        recordInteraction();
        const destination = cleanTarget(target || location.pathname);
        const existing = model.actions.find(function matches(action) { return action.name === name && action.target === destination; });
        if (existing) existing.count = Math.min(10000, existing.count + 1);
        else if (model.actions.length < 8) model.actions.push({name: name, target: destination, count: 1});
        sendObservation(true);
    }

    function clicked(clickEvent) {
        const element = clickEvent.target.closest ? clickEvent.target.closest('[sitebrush-data-analytics-action],[data-analytics-action],a[href]') : null;
        if (!element) return;
        const address = element.getAttribute('href') || location.pathname;
        let actionName = element.getAttribute('sitebrush-data-analytics-action') || element.getAttribute('data-analytics-action');
        if (!actionName) {
            if (element.hasAttribute('download')) actionName = 'download';
            else {
                try { actionName = new URL(address, location.href).origin === location.origin ? 'next-page' : 'outbound'; }
                catch (addressError) { return; }
            }
        }
        recordAction(actionName, address);
    }

    function campaignFields() {
        const parameters = new URLSearchParams(location.search);
        function field(name) { return boundedText(parameters.get('utm_' + name), 64); }
        return {Source: field('source'), Medium: field('medium'), Name: field('campaign'), Content: field('content'), Term: field('term'), Google: parameters.has('gclid'), Yandex: parameters.has('yclid'), Facebook: parameters.has('fbclid')};
    }

    function updateActiveTime() {
        const currentTime = performance.now();
        if (document.visibilityState === 'visible') {
            const activeEnd = Math.min(currentTime, model.lastInteraction + 30000);
            model.activeMilliseconds += Math.max(0, activeEnd - model.lastSample);
        }
        model.lastSample = currentTime;
    }

    function updateScroll() {
        const currentTime = performance.now();
        if (currentTime - model.lastScrollSample < 250) return;
        model.lastScrollSample = currentTime;
        const pageHeight = Math.max(document.documentElement.scrollHeight, document.body ? document.body.scrollHeight : 0);
        const bottomPosition = window.scrollY + window.innerHeight;
        model.maximumScroll = Math.max(model.maximumScroll, Math.min(100, Math.floor(bottomPosition * 100 / Math.max(1, pageHeight))));
    }

    function recordInteraction() {
        if (performance.now() - model.lastInteraction > 30 * 60000) startView();
        updateActiveTime();
        model.lastInteraction = performance.now();
        updateScroll();
        connect();
    }

    function sourceDescription() {
        const parameters = new URLSearchParams(location.search);
        const campaignSource = parameters.get('utm_source');
        if (!campaignSource) return '';
        return boundedText('utm:' + campaignSource + '/' + (parameters.get('utm_medium') || '') + '/' + (parameters.get('utm_campaign') || ''), 128);
    }

    function referrerHost() {
        if (!document.referrer) return '';
        try { return (new URL(document.referrer).origin + new URL(document.referrer).pathname).slice(0, 256); }
        catch (referrerError) { console.debug('SiteBrush analytics: invalid referrer', referrerError.name); return ''; }
    }

    function retainPendingAction() {
        if (!model.actions.length) return;
        try { sessionStorage.setItem('sitebrush.analytics.pending', JSON.stringify({expires: Date.now() + 1800000, visitor: model.visitor, view: model.view, sequence: model.sequence + 1, path: location.pathname.slice(0, 512), uri: currentURI(), tab: model.tab, session: model.session, persistent: model.persistent, actions: model.actions, active_ms: Math.floor(model.activeMilliseconds), scroll: model.maximumScroll, language: (navigator.language || '').slice(0,32), page_language: document.documentElement.lang.slice(0,32), source: sourceDescription(), referrer: referrerHost(), campaign: campaignFields()})); }
        catch (pendingError) { console.debug('SiteBrush analytics: pending action unavailable', pendingError.name); }
    }

    function beginDelivery() {
        let pending = null;
        try {
            const pendingText = sessionStorage.getItem('sitebrush.analytics.pending');
            if (pendingText && pendingText.length < 8192) pending = JSON.parse(pendingText);
            sessionStorage.removeItem('sitebrush.analytics.pending');
        } catch (pendingError) { console.debug('SiteBrush analytics: pending action invalid', pendingError.name); }
        if (pending && pending.visitor === model.visitor && pending.view !== model.view && pending.expires > Date.now()) {
            delete pending.expires;
            while (new TextEncoder().encode(JSON.stringify(pending)).length > 4000 && pending.actions.length) {pending.actions = pending.actions.slice(0,-1);pending.limited = true;}
            model.connection.send(JSON.stringify(pending));
            model.lastSent = performance.now(); model.deliveryAfter = model.lastSent + 1100;
            window.setTimeout(function sendCurrentView() { sendObservation(true); }, 1150);
        } else sendObservation(true);
    }

    function sendObservation(force) {
        updateActiveTime();
        updateScroll();
        const currentTime = performance.now();
        if (currentTime < model.deliveryAfter) { if (force) retainPendingAction(); return; }
        const uri = currentURI();
        if (model.firstSent && Math.floor(model.activeMilliseconds) === model.sentActive && model.maximumScroll === model.sentScroll && JSON.stringify(model.actions) === model.sentActions && uri === model.sentURI) return;
        if (model.stopped || !model.connection || model.connection.readyState !== WebSocket.OPEN) { if (force) retainPendingAction(); return; }
        if (model.firstSent && currentTime - model.lastSent < (force ? 1100 : 30000)) {
            if (force && !model.pendingTimer) model.pendingTimer = window.setTimeout(function sendPendingAction() { model.pendingTimer = null; sendObservation(true); }, 1150 - (currentTime - model.lastSent));
            if (force) retainPendingAction();
            return;
        }
        if (model.connection.bufferedAmount > 4096) {
            model.stopped = true;
            model.connection.close();
            return;
        }
        model.sequence++;
        const observation = {
            visitor: model.visitor, persistent: model.persistent,
            tab: model.tab, session: model.session, actions: model.actions,
            language: (navigator.language || '').slice(0, 32), page_language: document.documentElement.lang.slice(0, 32),
            timezone: Intl.DateTimeFormat().resolvedOptions().timeZone || '', offset: new Date().getTimezoneOffset(),
            campaign: campaignFields(),
            view: model.view, sequence: model.sequence,
            path: location.pathname.slice(0, 512), uri: uri,
            referrer: referrerHost(), source: sourceDescription(),
            active_ms: Math.floor(model.activeMilliseconds), scroll: model.maximumScroll
        };
        try {
            let encoded = JSON.stringify(observation);
            while (new TextEncoder().encode(encoded).length > 4000 && observation.actions.length) {
                observation.actions = observation.actions.slice(0, -1); observation.limited = true; encoded = JSON.stringify(observation);
            }
            if (new TextEncoder().encode(encoded).length > 4000) return;
            model.connection.send(encoded);
            model.firstSent = true;
            model.sentActive = observation.active_ms;
            model.sentScroll = observation.scroll;
            model.sentActions = JSON.stringify(model.actions);
            model.sentURI = observation.uri;
            try { sessionStorage.removeItem('sitebrush.analytics.pending'); } catch (pendingError) { console.debug('SiteBrush analytics: pending cleanup unavailable', pendingError.name); }
            model.lastSent = currentTime;
        } catch (connectionError) {
            model.stopped = true;
            console.debug('SiteBrush analytics: delivery stopped', connectionError.name);
        }
    }

    function connect() {
        if (model.stopped || document.visibilityState !== 'visible') return;
        if (model.connection && model.connection.readyState < WebSocket.CLOSING) return;
        model.connection = new WebSocket((location.protocol === 'https:' ? 'wss://' : 'ws://') + location.host + '/_sitebrush/analytics');
        model.connection.addEventListener('open', beginDelivery);
        model.connection.addEventListener('error', function failed() {
            console.debug('SiteBrush analytics: connection unavailable');
        });
        model.connection.addEventListener('close', function closed() {
            if (model.retryCount >= 2 || model.stopped || performance.now() - model.lastInteraction > 30000) return;
            model.retryCount++;
            window.setTimeout(connect, model.retryCount * 15000);
        });
    }

    function startView() {
        restoreSession();
        model.actions = []; model.sentActions = ""; model.sentURI = "";
        model.view = createIdentity();
        model.sequence = 0;
        model.activeMilliseconds = 0;
        model.maximumScroll = 0;
        model.deliveryAfter = 0;
        model.firstSent = false;
        model.lastSample = performance.now();
        model.lastInteraction = model.lastSample;
        model.stopped = false;
        model.retryCount = 0;
        connect();
    }

    function start() {
        if (!window.crypto || !window.WebSocket || new URLSearchParams(location.search).has('analytics')) return;
        restoreVisitor();
        startView();
        window.SiteBrushAnalytics = Object.freeze({action: recordAction});
        document.addEventListener('click', clicked, {capture: true, passive: true});
        for (const eventName of ['pointerdown', 'keydown', 'scroll']) {
            window.addEventListener(eventName, recordInteraction, { passive: true });
        }
        for (const eventName of ['hashchange', 'popstate']) {
            window.addEventListener(eventName, function locationChanged() { recordInteraction(); sendObservation(true); }, { passive: true });
        }
        document.addEventListener('visibilitychange', function visibilityChanged() {
            if (document.visibilityState === 'hidden') {
                // Account for the visible interval before resetting the sampling clock.
                const currentTime = performance.now();
                model.activeMilliseconds += Math.max(0, Math.min(currentTime, model.lastInteraction + 30000) - model.lastSample);
                model.lastSample = currentTime;
                sendObservation(true);
            } else {
                model.lastSample = performance.now();
                if (model.lastSample - model.lastInteraction > 30 * 60000) startView();
                else connect();
            }
        });
        window.addEventListener('pagehide', function pageHidden() {
            sendObservation(true);
            model.stopped = true;
            if (model.connection) model.connection.close();
        });
        window.addEventListener('pageshow', function pageRestored(pageEvent) {
            if (pageEvent.persisted) startView();
        });
        window.setInterval(function sample() { if (document.visibilityState === 'visible') sendObservation(false); }, 30000);
    }

    if (document.readyState === 'complete') window.setTimeout(start, 0);
    else window.addEventListener('load', start, { once: true });
})();
