(function installPublicSitebrushMenu() {
  'use strict';
  if (window.sitebrushRefreshMenu) return;
  function reportGuestAccountStatus(status) {
    document.dispatchEvent(new CustomEvent('sitebrush-account-state', {detail: {status: status}}));
  }
  window.sitebrushRefreshMenu = function refreshPublicMenu(menuPoint) {
    const menuHost = document.getElementById('SiteBrushMenuBox');
    if (!menuHost) return;
    if (typeof WebSocket !== 'function') { reportGuestAccountStatus('unavailable'); return; }
    const menuRoot = menuHost.shadowRoot || menuHost;
    const menuList = menuRoot.querySelector('.SiteBrushMenuList');
    if (!menuList) return;
    const socketAddress = new URL('/?sitebrush_menu_ws', window.location.href);
    socketAddress.protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    reportGuestAccountStatus('pending');
    let answered = false;
    let menuSocket;
    try {
      menuSocket = new WebSocket(socketAddress.href);
    } catch (menuError) {
      reportGuestAccountStatus('unavailable');
      console.error('SiteBrush menu:', menuError);
      return;
    }
    const deadline = window.setTimeout(function closeUnansweredMenu() { menuSocket.close(); }, 5000);
    menuSocket.onopen = function requestMenu() {
      menuSocket.send(JSON.stringify({path: window.location.pathname, admin: Boolean(window.__sitebrushAdminMenuLoaded)}));
    };
    menuSocket.onmessage = function receiveMenu(menuEvent) {
      try {
        const response = JSON.parse(menuEvent.data);
        if (document.getElementById('SiteBrushMenuBox') !== menuHost) return;
        if (response.updating) {
          answered = true;
          const notice = document.createElement('li');
          notice.className = 'SiteBrushContextMenu SiteBrushContextMenuLink';
          notice.textContent = response.message;
          menuList.replaceChildren(notice);
          return;
        }
        if (response.admin === false) {
          answered = true;
          if (window.__sitebrushAdminMenuLoaded && window.sitebrushShowGuestMenu) {
            window.__sitebrushAdminMenuLoaded = false;
            window.sitebrushShowGuestMenu(menuPoint);
          }
          reportGuestAccountStatus('guest');
          return;
        }
        if (response.admin !== true || !response.menu && !window.__sitebrushAdminMenuLoaded) {
          throw new Error('Invalid SiteBrush menu response');
        }
        answered = true;
        if (window.__sitebrushAdminMenuLoaded) return;
        window.__sitebrushContextMenuInitialized = false;
        const menuDocument = new DOMParser().parseFromString(response.menu, 'text/html');
        for (const menuElement of menuDocument.querySelectorAll('style, script')) {
          const installedElement = document.createElement(menuElement.tagName.toLowerCase());
          if (menuElement.src) continue;
          installedElement.textContent = menuElement.textContent;
          (document.body || document.head || document.documentElement).appendChild(installedElement);
        }
        if (!window.__sitebrushAdminMenuLoaded) {
          window.__sitebrushContextMenuInitialized = true;
          throw new Error('SiteBrush administrator menu could not initialize');
        }
        document.dispatchEvent(new MouseEvent('contextmenu', {bubbles: true, cancelable: true, clientX: menuPoint.clientX || 0, clientY: menuPoint.clientY || 0}));
      } catch (menuError) {
        console.error('SiteBrush menu:', menuError);
      } finally {
        menuSocket.close();
      }
    };
    menuSocket.onclose = function finishMenuRequest() {
      window.clearTimeout(deadline);
      if (!answered && document.getElementById('SiteBrushMenuBox') === menuHost) reportGuestAccountStatus('unavailable');
    };
    menuSocket.onerror = function reportMenuFailure() { console.error('SiteBrush menu is unavailable'); };
  };
})();
