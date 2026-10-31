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

  function installTestDrivePageActions() {
    const pageQuery = new URLSearchParams(window.location.search);
    if (pageQuery.has('visual') || pageQuery.has('edit') || pageQuery.has('text') || document.querySelector('[data-test-drive-page-actions]')) return;
    if (typeof WebSocket !== 'function') return;
    const socketURL = new URL('/', window.location.href);
    socketURL.searchParams.set('test_drive_ws', '');
    socketURL.protocol = socketURL.protocol === 'https:' ? 'wss:' : 'ws:';
    let statusSocket;
    try { statusSocket = new WebSocket(socketURL.href); }
    catch (socketError) { return; }
    const deadline = window.setTimeout(function expireTestDriveStatus() { statusSocket.close(); }, 4000);
    statusSocket.addEventListener('open', function requestTestDriveStatus() {
      statusSocket.send(JSON.stringify({id: 1, action: 'status'}));
    });
    statusSocket.addEventListener('message', function showTestDriveActions(statusEvent) {
      let reply;
      try { reply = JSON.parse(statusEvent.data); }
      catch (replyError) { statusSocket.close(); return; }
      statusSocket.close();
      if (reply.status !== 200 || !reply.payload || reply.payload.status === 'activated') return;
      const language = String(document.documentElement.lang || navigator.language || 'en').toLowerCase();
      const russian = language === 'ru' || language.startsWith('ru-');
      const labels = russian ? {
        hint: 'Редактирование: правый клик мышью или долгое нажатие на телефоне.',
        copy: 'Скопировать другой сайт',
        save: 'Сохранить Test Drive'
      } : {
        hint: 'Edit: right-click on desktop or long-press on mobile.',
        copy: 'Copy another site',
        save: 'Save Test Drive'
      };
      const stylesheet = document.createElement('link');
      stylesheet.rel = 'stylesheet';
      stylesheet.href = '/p/static/test_drive_page_actions.css';
      stylesheet.dataset.sitebrushOwned = 'true';
      (document.head || document.documentElement).appendChild(stylesheet);
      const actions = document.createElement('aside');
      actions.className = 'SiteBrushTestDrivePageActions';
      actions.dataset.testDrivePageActions = 'true';
      actions.dataset.sitebrushOwned = 'true';
      actions.setAttribute('aria-label', 'SiteBrush Test Drive');
      const hint = document.createElement('p');
      hint.className = 'SiteBrushTestDrivePageActionsHint';
      hint.textContent = labels.hint;
      const copyLink = document.createElement('a');
      copyLink.className = 'SiteBrushTestDrivePageAction';
      copyLink.href = '/?visual#test-drive-copy';
      copyLink.textContent = labels.copy;
      const saveLink = document.createElement('a');
      saveLink.className = 'SiteBrushTestDrivePageAction primary';
      saveLink.href = '/?visual#activation';
      saveLink.textContent = labels.save;
      actions.append(hint, copyLink, saveLink);
      (document.body || document.documentElement).appendChild(actions);
    });
    statusSocket.addEventListener('close', function finishTestDriveStatus() { window.clearTimeout(deadline); });
    statusSocket.addEventListener('error', function ignoreNonTestDrivePage() {});
  }

  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', installTestDrivePageActions, {once: true});
  else installTestDrivePageActions();

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
