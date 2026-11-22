(function initializeServerPublicIPs() {
  const panelElement = document.querySelector('[data-public-ip-panel]');
  if (!panelElement) { return; }
  const addressListElement = panelElement.querySelector('[data-public-ip-addresses]');
  const manualInputElement = panelElement.querySelector('[data-public-ip-manual]');
  const preferredSelectElement = panelElement.querySelector('[data-public-ip-preferred]');
  const statusElement = panelElement.querySelector('[data-public-ip-status]');
  const primaryElement = panelElement.querySelector('[data-public-ip-primary]');
  const saveButtonElement = panelElement.querySelector('[data-public-ip-save]');
  const refreshButtonElement = panelElement.querySelector('[data-public-ip-refresh]');
  const copyButtonElement = panelElement.querySelector('[data-public-ip-copy]');
  const copyLabel = copyButtonElement ? copyButtonElement.textContent : '';
  let copyTimer = null;
  const model = { snapshot: null, busy: true, message: '', failed: false, manual: '', preferred: '', primary: '' };
  const labels = JSON.parse(panelElement.querySelector('[data-public-ip-labels]').textContent);
  const protocol = location.protocol === 'https:' ? 'wss:' : 'ws:';
  const socket = new WebSocket(protocol + '//' + location.host + location.pathname + '?public_ip_settings_ws');

  function render() {
    manualInputElement.disabled = model.busy;
    preferredSelectElement.disabled = model.busy;
    saveButtonElement.disabled = model.busy;
    refreshButtonElement.disabled = model.busy;
    statusElement.textContent = model.message;
    statusElement.classList.toggle('text-danger', model.failed);
    primaryElement.textContent = model.primary || labels.missing;
    if (copyButtonElement) copyButtonElement.disabled = !model.primary;
    primaryElement.classList.toggle('text-danger', Boolean(model.preferred && !model.primary));
    manualInputElement.value = model.manual;
    preferredSelectElement.replaceChildren(new Option(labels.auto, ''));
    addressListElement.replaceChildren();
    const choices = new Set();
    if (model.snapshot) {
      for (const address of model.snapshot.addresses || []) {
        const rowElement = document.createElement('li');
        const sourceLabel = address.source === 'manual' ? labels.manual : address.source === 'outgoing' ? labels.outgoing : address.interface;
        const ipElement = document.createElement('strong');
        ipElement.textContent = address.ip;
        const detailsElement = document.createElement('small');
        detailsElement.textContent = sourceLabel + (address.public ? '' : ' · ' + labels.local) + (address.active ? '' : ' · ' + labels.inactive);
        rowElement.append(ipElement, detailsElement);
        addressListElement.append(rowElement);
        if (address.active && address.public && !choices.has(address.ip)) {
          choices.add(address.ip);
          preferredSelectElement.add(new Option(address.ip, address.ip));
        }
      }
      if (addressListElement.children.length === 0) {
        const empty = document.createElement('li');
        empty.textContent = labels.missing;
        addressListElement.append(empty);
      }
      for (const manualIP of model.manual.split(/[\s,;]+/).filter(Boolean)) {
        if (!choices.has(manualIP)) { choices.add(manualIP); preferredSelectElement.add(new Option(manualIP, manualIP)); }
      }
      if (model.preferred && !choices.has(model.preferred)) {
        preferredSelectElement.add(new Option(model.preferred + ' — ' + labels.missing, model.preferred));
      }
    }
    preferredSelectElement.value = model.preferred;
  }

  function submit(action) {
    if (model.busy || socket.readyState !== WebSocket.OPEN) { return; }
    model.busy = true;
    model.failed = false;
    model.message = labels.working;
    render();
    try {
      socket.send(JSON.stringify({ action: action, csrf: panelElement.dataset.csrf, settings: { manual: model.manual.split(/[\s,;]+/).filter(Boolean), preferred: model.preferred } }));
    } catch (sendError) {
      model.busy = false; model.failed = true; model.message = String(sendError); render();
    }
  }
  socket.addEventListener('open', function load() { model.busy = false; submit('get'); });
  socket.addEventListener('message', function receive(messageEvent) {
    try {
      const response = JSON.parse(messageEvent.data);
      model.busy = false;
      model.failed = Boolean(response.error);
      model.message = response.error || ((response.snapshot.errors || []).join('; ') || labels.ready);
      if (!response.error) {
        model.snapshot = response.snapshot;
        model.manual = (response.snapshot.settings.manual || []).join('\n');
        model.preferred = response.snapshot.settings.preferred || '';
        model.primary = response.primary || '';
      }
    } catch (parseError) { model.busy = false; model.failed = true; model.message = String(parseError); }
    render();
  });
  function disconnected() { model.busy = true; model.failed = true; model.message = labels.disconnected; render(); }
  socket.addEventListener('error', disconnected);
  socket.addEventListener('close', disconnected);
  manualInputElement.addEventListener('input', function changeManual() { model.manual = manualInputElement.value; });
  manualInputElement.addEventListener('change', function updateChoices() { render(); });
  preferredSelectElement.addEventListener('change', function choose() { model.preferred = preferredSelectElement.value; });
  saveButtonElement.addEventListener('click', function save() { submit('save'); });
  refreshButtonElement.addEventListener('click', function refresh() { submit('refresh'); });
  if (copyButtonElement) {
    copyButtonElement.addEventListener('click', async function copyActiveIP() {
      if (!model.primary) return;
      let copied = false;
      try {
        if (navigator.clipboard && navigator.clipboard.writeText) {
          await navigator.clipboard.writeText(model.primary);
          copied = true;
        } else {
          const field = document.createElement('textarea');
          field.value = model.primary;
          field.style.position = 'fixed';
          field.style.opacity = '0';
          document.body.append(field);
          field.select();
          copied = document.execCommand('copy');
          field.remove();
        }
      } catch (copyError) { copied = false; }
      copyButtonElement.textContent = copied
        ? (document.documentElement.lang.startsWith('ru') ? 'Скопировано ✓' : 'Copied ✓')
        : (document.documentElement.lang.startsWith('ru') ? 'Ошибка копирования' : 'Copy failed');
      if (copyTimer !== null) window.clearTimeout(copyTimer);
      copyTimer = window.setTimeout(function restoreIPCopyLabel() {
        copyButtonElement.textContent = copyLabel;
        copyTimer = null;
      }, 2000);
    });
  }
  render();
})();
