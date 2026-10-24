(function installSitebrushVisitorServices() {
  'use strict';
  if (window.sitebrushVisitorServicesInstalled) return;
  window.sitebrushVisitorServicesInstalled = true;
  const loaderScript = document.currentScript;
  const menuAsset = loaderScript ? loaderScript.dataset.menu : '';
  const visitorModel = {menuRequested: false, pressTimer: null, pressPoint: null, suppressGestureClick: false, suppressClickUntil: 0};
  function loadGuestMenu(browserEvent) {
    if (!menuAsset || browserEvent.ctrlKey || window.__sitebrushContextMenuInitialized) return;
    if (browserEvent.type === 'contextmenu') browserEvent.preventDefault();
    if (browserEvent.pointerType === 'touch' || browserEvent.pointerType === 'pen' || visitorModel.pressPoint) { visitorModel.suppressGestureClick = true; visitorModel.suppressClickUntil = Date.now() + 1500; }
    if (visitorModel.menuRequested) return;
    visitorModel.menuRequested = true;
    const menuPoint = {clientX: browserEvent.clientX || 0, clientY: browserEvent.clientY || 0, pointerType: browserEvent.pointerType || 'mouse'};
    const menuScript = document.createElement('script');
    menuScript.src = '/p/static/' + menuAsset + (loaderScript ? new URL(loaderScript.src).search : '');
    menuScript.async = true;
    menuScript.fetchPriority = 'high';
    menuScript.dataset.sitebrushOwned = 'true';
    menuScript.onload = function openRequestedMenu() {
      const eventOptions = {bubbles: true, cancelable: true, clientX: menuPoint.clientX, clientY: menuPoint.clientY, pointerType: menuPoint.pointerType};
      const contextMenuEvent = typeof window.PointerEvent === 'function' ? new PointerEvent('contextmenu', eventOptions) : new MouseEvent('contextmenu', eventOptions);
      if (typeof window.PointerEvent !== 'function') Object.defineProperty(contextMenuEvent, 'pointerType', {value: menuPoint.pointerType});
      document.dispatchEvent(contextMenuEvent);
    };
    menuScript.onerror = function reportMenuFailure() {
      visitorModel.menuRequested = false;
      console.error('SiteBrush menu is unavailable');
    };
    (document.head || document.documentElement).appendChild(menuScript);
  }
  function cancelMenuPress() {
    window.clearTimeout(visitorModel.pressTimer);
    visitorModel.pressTimer = null;
    visitorModel.pressPoint = null;
  }
  function beginMenuPress(pointerEvent) {
    visitorModel.suppressGestureClick = false;
    if (window.__sitebrushContextMenuInitialized) return;
    if (pointerEvent.pointerType && pointerEvent.pointerType !== 'touch' && pointerEvent.pointerType !== 'pen') return;
    cancelMenuPress();
    visitorModel.pressPoint = {clientX: pointerEvent.clientX, clientY: pointerEvent.clientY, pointerType: pointerEvent.pointerType || 'touch'};
    visitorModel.pressTimer = window.setTimeout(function openTouchMenu() {
      const pressPoint = visitorModel.pressPoint;
      cancelMenuPress();
      if (!pressPoint) return;
      visitorModel.suppressGestureClick = true;
      visitorModel.suppressClickUntil = Date.now() + 1500;
      loadGuestMenu(pressPoint);
    }, 650);
  }
  function moveMenuPress(pointerEvent) {
    if (!visitorModel.pressPoint) return;
    if (Math.abs(pointerEvent.clientX - visitorModel.pressPoint.clientX) > 12 || Math.abs(pointerEvent.clientY - visitorModel.pressPoint.clientY) > 12) cancelMenuPress();
  }
  if (menuAsset) {
    document.addEventListener('contextmenu', loadGuestMenu, {capture: true});
    if (window.PointerEvent) {
      document.addEventListener('pointerdown', beginMenuPress, {capture: true, passive: true});
      document.addEventListener('pointermove', moveMenuPress, {capture: true, passive: true});
      document.addEventListener('pointerup', cancelMenuPress, {capture: true, passive: true});
      document.addEventListener('pointercancel', cancelMenuPress, {capture: true, passive: true});
    } else {
      document.addEventListener('touchstart', function beginTouchPress(touchEvent) {
        if (touchEvent.touches.length === 1) beginMenuPress(touchEvent.touches[0]);
        else cancelMenuPress();
      }, {capture: true, passive: true});
      document.addEventListener('touchmove', function moveTouchPress(touchEvent) {
        if (touchEvent.touches.length === 1) moveMenuPress(touchEvent.touches[0]);
        else cancelMenuPress();
      }, {capture: true, passive: true});
      document.addEventListener('touchend', cancelMenuPress, {capture: true, passive: true});
      document.addEventListener('touchcancel', cancelMenuPress, {capture: true, passive: true});
    }
    document.addEventListener('click', function preventLongPressNavigation(clickEvent) {
      if (!visitorModel.suppressGestureClick || Date.now() > visitorModel.suppressClickUntil || clickEvent.detail === 0) return;
      visitorModel.suppressGestureClick = false;
      clickEvent.preventDefault();
      clickEvent.stopImmediatePropagation();
    }, {capture: true});
  }
  function loadAnalytics() {
    if (window.SiteBrushAnalyticsStarted) return;
    const analyticsScript = document.createElement('script');
    analyticsScript.src = '/p/static/analytics.js' + (loaderScript ? new URL(loaderScript.src).search : '');
    analyticsScript.async = true;
    analyticsScript.dataset.sitebrushOwned = 'true';
    analyticsScript.dataset.analyticsLayout = loaderScript ? loaderScript.dataset.analyticsLayout || '' : '';
    analyticsScript.onerror = function reportAnalyticsFailure() { console.debug('SiteBrush analytics is unavailable'); };
    (document.head || document.documentElement).appendChild(analyticsScript);
  }
  function scheduleAnalytics() {
    if ('requestIdleCallback' in window) window.requestIdleCallback(loadAnalytics, {timeout: 1500});
    else window.setTimeout(loadAnalytics, 100);
  }
  if (document.readyState === 'complete') scheduleAnalytics();
  else window.addEventListener('load', scheduleAnalytics, {once: true});
})();
