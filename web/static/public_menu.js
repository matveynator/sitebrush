(function installPublicSitebrushMenu() {
  'use strict';
  if (window.sitebrushRefreshMenu) return;
  window.sitebrushRefreshMenu = function refreshPublicMenu(menuPoint) {
    const menuHost = document.getElementById('SiteBrushMenuBox');
    if (!menuHost || typeof WebSocket !== 'function') return;
    const menuRoot = menuHost.shadowRoot || menuHost;
    const menuList = menuRoot.querySelector('.SiteBrushMenuList');
    if (!menuList) return;
    const socketAddress = new URL('/?sitebrush_menu_ws', window.location.href);
    socketAddress.protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const menuSocket = new WebSocket(socketAddress.href);
    const deadline = window.setTimeout(function closeUnansweredMenu() { menuSocket.close(); }, 5000);
    menuSocket.onopen = function requestMenu() {
      menuSocket.send(JSON.stringify({path: window.location.pathname, admin: Boolean(window.__sitebrushAdminMenuLoaded)}));
    };
    menuSocket.onmessage = function receiveMenu(menuEvent) {
      try {
        const response = JSON.parse(menuEvent.data);
        if (document.getElementById('SiteBrushMenuBox') !== menuHost) return;
        if (response.updating) {
          const notice = document.createElement('li');
          notice.className = 'SiteBrushContextMenu SiteBrushContextMenuLink';
          notice.textContent = response.message;
          menuList.replaceChildren(notice);
          return;
        }
        if (!response.menu || window.__sitebrushAdminMenuLoaded) return;
        window.__sitebrushAdminMenuLoaded = true;
        window.__sitebrushContextMenuInitialized = false;
        const menuDocument = new DOMParser().parseFromString(response.menu, 'text/html');
        for (const menuElement of menuDocument.querySelectorAll('style, script')) {
          const installedElement = document.createElement(menuElement.tagName.toLowerCase());
          if (menuElement.src) continue;
          installedElement.textContent = menuElement.textContent;
          document.body.appendChild(installedElement);
        }
        document.dispatchEvent(new MouseEvent('contextmenu', {bubbles: true, cancelable: true, clientX: menuPoint.clientX || 0, clientY: menuPoint.clientY || 0}));
      } catch (menuError) {
        console.error('SiteBrush menu:', menuError);
      } finally {
        menuSocket.close();
      }
    };
    menuSocket.onclose = function finishMenuRequest() { window.clearTimeout(deadline); };
    menuSocket.onerror = function reportMenuFailure() { console.error('SiteBrush menu is unavailable'); };
  };
})();
