/* Each page owns a bounded, cumulative observation. Delivery never gates navigation. */
(function sitebrushAnalytics() {
    'use strict';
    if (window.SiteBrushAnalyticsStarted) return;
    window.SiteBrushAnalyticsStarted = true;
    const storageKey = 'sitebrush.analytics.browser.v1';
    const lifetime = 90 * 86400000;
    const model = {
        visitor: '', persistent: false, view: '', sequence: 0,
        activeMilliseconds: 0, maximumScroll: 0, lastSample: 0,
        lastInteraction: 0, lastScrollSample: 0, connection: null,
        lastSent: 0, sentActive: 0, sentScroll: 0, firstSent: false, stopped: false, retryCount: 0
    };

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
        return ('utm:' + campaignSource + '/' + (parameters.get('utm_medium') || '') + '/' + (parameters.get('utm_campaign') || '')).slice(0, 128);
    }

    function referrerHost() {
        if (!document.referrer) return '';
        try { return new URL(document.referrer).origin.slice(0, 256); }
        catch (referrerError) { console.debug('SiteBrush analytics: invalid referrer', referrerError.name); return ''; }
    }

    function sendObservation(force) {
        updateActiveTime();
        updateScroll();
        const currentTime = performance.now();
        if (model.firstSent && Math.floor(model.activeMilliseconds) === model.sentActive && model.maximumScroll === model.sentScroll) return;
        if (model.stopped || !model.connection || model.connection.readyState !== WebSocket.OPEN) return;
        if (model.firstSent && currentTime - model.lastSent < (force ? 1100 : 30000)) return;
        if (model.connection.bufferedAmount > 4096) {
            model.stopped = true;
            model.connection.close();
            return;
        }
        model.sequence++;
        const observation = {
            visitor: model.visitor, persistent: model.persistent,
            view: model.view, sequence: model.sequence,
            path: location.pathname.slice(0, 512),
            referrer: referrerHost(), source: sourceDescription(),
            active_ms: Math.floor(model.activeMilliseconds), scroll: model.maximumScroll
        };
        try {
            model.connection.send(JSON.stringify(observation));
            model.firstSent = true;
            model.sentActive = observation.active_ms;
            model.sentScroll = observation.scroll;
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
        model.connection.addEventListener('open', function opened() { sendObservation(true); });
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
        model.view = createIdentity();
        model.sequence = 0;
        model.activeMilliseconds = 0;
        model.maximumScroll = 0;
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
        for (const eventName of ['pointerdown', 'keydown', 'scroll']) {
            window.addEventListener(eventName, recordInteraction, { passive: true });
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
