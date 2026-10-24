(function installSitebrushUnfreeze() {
  'use strict';
  if (window.SitebrushUnfreeze) return;
  window.SitebrushUnfreeze = {open: openUnfreeze};
  const configurationElement = document.querySelector('[data-sitebrush-unfreeze-configuration]');
  if (configurationElement && configurationElement.dataset.sitebrushUnfreezeConfiguration) {
    try {
      const pageConfiguration = JSON.parse(configurationElement.dataset.sitebrushUnfreezeConfiguration);
      const openButton = document.getElementById('SiteBrushUnfreezeOpen');
      if (openButton) openButton.addEventListener('click', function openFromHistory() { openUnfreeze(pageConfiguration); });
      if (pageConfiguration.open) openUnfreeze(pageConfiguration);
    } catch (configurationError) {
      console.error('SiteBrush restoration configuration failed', configurationError);
      const errorElement = document.createElement('p');errorElement.textContent = configurationError.message;configurationElement.prepend(errorElement);
    }
  }

  function openUnfreeze(configuration) {
    const previousDialog = document.getElementById('SiteBrushUnfreezeDialog');
    if (previousDialog) {
      previousDialog.dispatchEvent(new Event('sitebrush-unfreeze-close'));
      previousDialog.remove();
    }
    const model = {phase: 'loading', preview: null, error: '', offset: 0, socket: null};
    const labels = configuration.labels;
    const overlay = document.createElement('div');
    overlay.id = 'SiteBrushUnfreezeDialog';
    overlay.dataset.sitebrushOwned = 'true';
    const shadow = overlay.attachShadow({mode: 'open'});
    const stylesheet = document.createElement('style');
    stylesheet.textContent = "SITEBRUSH_UNFREEZE_SHARED_STYLES";
    const dialog = document.createElement('section');
    dialog.className = 'technical-page unfreeze-dialog';
    dialog.setAttribute('role', 'dialog');
    dialog.setAttribute('aria-modal', 'true');
    dialog.tabIndex = -1;
    dialog.setAttribute('aria-labelledby', 'SiteBrushUnfreezeTitle');
    shadow.append(stylesheet, dialog);
    (document.body || document.documentElement).appendChild(overlay);
    overlay.addEventListener('sitebrush-unfreeze-close', closeDialog);
    shadow.addEventListener('keydown', function handleUnfreezeKeyboard(event) {
      if (event.key === 'Escape' && model.phase !== 'restoring') {
        event.preventDefault();
        closeDialog();
      }
      if (event.key !== 'Tab') return;
      const controls = Array.from(dialog.querySelectorAll('button,a[href]'));
      if (!controls.length) { event.preventDefault(); dialog.focus(); return; }
      const firstControl = controls[0];
      const lastControl = controls[controls.length - 1];
      if (event.shiftKey && (shadow.activeElement === firstControl || shadow.activeElement === dialog)) {
        event.preventDefault();
        lastControl.focus();
      } else if (!event.shiftKey && shadow.activeElement === lastControl) {
        event.preventDefault();
        firstControl.focus();
      }
    });

    function paragraph(message, className) {
      const paragraphElement = document.createElement('p');
      paragraphElement.textContent = message;
      if (className) paragraphElement.className = className;
      dialog.appendChild(paragraphElement);
    }
    function button(caption, action, className) {
      const buttonElement = document.createElement('button');
      buttonElement.type = 'button';
      buttonElement.textContent = caption;
      buttonElement.className = className || 'btn btn-outline-secondary';
      buttonElement.addEventListener('click', action);
      return buttonElement;
    }
    function closeDialog() {
      if (model.socket) model.socket.close();
      overlay.remove();
    }
    function render() {
      dialog.replaceChildren();
      const heading = document.createElement('h2');
      heading.id = 'SiteBrushUnfreezeTitle';
      heading.textContent = labels.title;
      dialog.appendChild(heading);
      if (model.phase !== 'restoring') {
        const historyParagraph = document.createElement('p');
        historyParagraph.className = 'unfreeze-history-link';
        const historyLink = document.createElement('a');
        historyLink.href = '/?unfreeze&history';
        historyLink.textContent = labels.history;
        historyParagraph.appendChild(historyLink);
        dialog.appendChild(historyParagraph);
      }
      if (model.phase === 'loading') paragraph(labels.loading);
      if (model.phase === 'restoring') paragraph(labels.restoring);
      if (model.error) paragraph(model.error, 'error');
      if (model.phase === 'confirm' && model.preview) {
        paragraph(labels.description);
        paragraph(labels.frozenAt + ' ' + new Date(model.preview.created_at).toLocaleString());
        paragraph(labels.total + ' ' + model.preview.total);
        const changes = document.createElement('ul');
        for (const change of model.preview.changes) {
          const changeElement = document.createElement('li');
          changeElement.textContent = (labels[change.action] || change.action) + ' · ' + (labels[change.kind] || change.kind) + ' · ' + change.path;
          changes.appendChild(changeElement);
        }
        dialog.appendChild(changes);
      }
      const actions = document.createElement('div');
      actions.className = 'actions';
      if (model.phase === 'loading') actions.appendChild(button(labels.keepEditing, closeDialog));
      if (model.phase === 'confirm') {
        actions.appendChild(button(labels.keepEditing, closeDialog));
        if (model.offset > 0) actions.appendChild(button(labels.previous, function previousChanges() { loadPreview(model.offset - 100); }));
        if (model.offset + 100 < model.preview.total) actions.appendChild(button(labels.next, function nextChanges() { loadPreview(model.offset + 100); }));
        actions.appendChild(button(labels.confirm, restore, 'btn btn-danger'));
      } else if (model.phase === 'error') {
        actions.appendChild(button(labels.refresh, function refreshPreview() { loadPreview(0); }));
        actions.appendChild(button(labels.keepEditing, closeDialog));
      }
      dialog.appendChild(actions);
      dialog.focus();
    }
    function exchange(command) {
      return new Promise(function exchangeUnfreeze(resolve, reject) {
        const address = new URL(configuration.path, window.location.href);
        address.search = '?unfreeze_ws';
        address.protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
        const socket = new WebSocket(address.href);
        model.socket = socket;
        let received = false;
        const deadline = window.setTimeout(function closeUnfreezeTimeout() { socket.close(); }, command.action === 'restore' ? 300000 : 60000);
        socket.onopen = function sendUnfreeze() { socket.send(JSON.stringify(Object.assign({}, command, {csrf: configuration.csrf}))); };
        socket.onmessage = function receiveUnfreeze(event) {
          received = true;
          window.clearTimeout(deadline);
          try {
            const response = JSON.parse(event.data);
            if (response.error) reject(new Error(response.error));
            else resolve(response);
          } catch (parseError) { reject(parseError); }
          socket.close();
        };
        socket.onerror = function reportUnfreezeFailure() { console.error('SiteBrush restoration connection failed'); };
        socket.onclose = function finishUnfreeze() { window.clearTimeout(deadline); if (!received) reject(new Error(labels.connectionError)); };
      });
    }
    function showError(error) {
      if (!overlay.isConnected) return;
      model.phase = 'error';
      model.error = error.message || labels.connectionError;
      render();
    }
    function loadPreview(offset) {
      model.phase = 'loading';
      model.error = '';
      render();
      exchange({action: 'preview', offset: offset}).then(function receivedPreview(preview) {
        if (overlay.isConnected) {
          model.preview = preview;
          model.offset = offset;
          model.phase = 'confirm';
          render();
        }
      }).catch(showError);
    }
    function restore() {
      if (model.phase !== 'confirm') return;
      model.phase = 'restoring';
      model.error = '';
      render();
      exchange({action: 'restore', checkpoint_id: model.preview.checkpoint_id, digest: model.preview.digest}).then(function restored(response) {
        if (!response.restored) throw new Error(labels.connectionError);
        window.location.assign(response.return_path || '/');
      }).catch(showError);
    }
    loadPreview(0);
  }
})();
