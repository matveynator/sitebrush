(function installSitebrushVisitorServices() {
  'use strict';
  if (window.sitebrushVisitorServicesInstalled) return;
  window.sitebrushVisitorServicesInstalled = true;
  const loaderScript = document.currentScript;
  const menuAsset = loaderScript ? loaderScript.dataset.menu : '';
  const visitorModel = {menuRequested: false, pressTimer: null};
  function loadGuestMenu(browserEvent) {
    if (!menuAsset || browserEvent.ctrlKey || window.__sitebrushContextMenuInitialized) return;
    if (browserEvent.type === 'contextmenu') browserEvent.preventDefault();
    if (visitorModel.menuRequested) return;
    visitorModel.menuRequested = true;
    const menuPoint = {clientX: browserEvent.clientX || 0, clientY: browserEvent.clientY || 0, pointerType: browserEvent.pointerType || 'mouse'};
    const menuScript = document.createElement('script');
    menuScript.src = '/p/static/' + menuAsset + (loaderScript ? new URL(loaderScript.src).search : '');
    menuScript.async = true;
    menuScript.dataset.sitebrushOwned = 'true';
    menuScript.onload = function openRequestedMenu() {
      const eventOptions = {bubbles: true, cancelable: true, clientX: menuPoint.clientX, clientY: menuPoint.clientY, pointerType: menuPoint.pointerType};
      const contextMenuEvent = typeof window.PointerEvent === 'function' ? new PointerEvent('contextmenu', eventOptions) : new MouseEvent('contextmenu', eventOptions);
      document.dispatchEvent(contextMenuEvent);
    };
    menuScript.onerror = function reportMenuFailure() {
      visitorModel.menuRequested = false;
      console.error('SiteBrush menu is unavailable');
    };
    document.head.appendChild(menuScript);
  }
  if (menuAsset) {
    document.addEventListener('contextmenu', loadGuestMenu, {capture: true});
    document.addEventListener('pointerdown', function beginMenuPress(pointerEvent) {
      if (pointerEvent.pointerType !== 'touch') return;
      visitorModel.pressTimer = window.setTimeout(function openTouchMenu() { loadGuestMenu(pointerEvent); }, 650);
    }, {passive: true});
    for (const endEventName of ['pointerup', 'pointercancel', 'pointermove']) {
      document.addEventListener(endEventName, function cancelMenuPress() { window.clearTimeout(visitorModel.pressTimer); }, {passive: true});
    }
  }
  function loadAnalytics() {
    if (window.SiteBrushAnalyticsStarted) return;
    const analyticsScript = document.createElement('script');
    analyticsScript.src = '/p/static/analytics.js' + (loaderScript ? new URL(loaderScript.src).search : '');
    analyticsScript.async = true;
    analyticsScript.dataset.sitebrushOwned = 'true';
    analyticsScript.dataset.analyticsLayout = loaderScript ? loaderScript.dataset.analyticsLayout || '' : '';
    analyticsScript.onerror = function reportAnalyticsFailure() { console.debug('SiteBrush analytics is unavailable'); };
    document.head.appendChild(analyticsScript);
  }
  function scheduleAnalytics() {
    if ('requestIdleCallback' in window) window.requestIdleCallback(loadAnalytics, {timeout: 1500});
    else window.setTimeout(loadAnalytics, 100);
  }
  if (document.readyState === 'complete') scheduleAnalytics();
  else window.addEventListener('load', scheduleAnalytics, {once: true});
})();
