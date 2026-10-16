(() => {
  'use strict';
  const root = document.getElementById('https-progress');
  if (!root) return;
  const texts = Object.fromEntries(Array.from(document.querySelectorAll('#https-texts [data-key]'), el => [el.dataset.key, el.textContent]));
  const text = state => texts['auth_https_' + state] || texts.auth_https_wait;
  const stage = document.getElementById('https-stage');
  const bar = document.getElementById('https-bar');
  const countdown = document.getElementById('https-countdown');
  const error = document.getElementById('https-error');
  const events = document.getElementById('https-events');
  const retry = document.getElementById('https-next-retry');
  let started = Date.now(), retryAt = 0, complete = false;
  const percentages = {queued: 5, checking_dns: 15, requesting: 35, received: 80, applying: 95, ready: 100};
  stage.textContent = text(root.dataset.state);
  // Code confirmation renders this page from POST ?register without changing
  // the address bar. The server URL contains the verified email_confirm token.
  const url = new URL(root.dataset.refreshUrl, location.href);
  if (url.origin !== location.origin) { error.textContent = texts.auth_https_status_error; return; }
  url.searchParams.set('https_status', '1');
  function tick() {
    const remaining = Math.max(0, Math.ceil((started + 600000 - Date.now()) / 1000));
    countdown.textContent = complete ? '0:00' : Math.floor(remaining / 60) + ':' + String(remaining % 60).padStart(2, '0');
    document.getElementById('https-timeout').hidden = remaining > 0 || complete;
    const next = Math.max(0, Math.ceil((retryAt - Date.now()) / 1000));
    retry.textContent = next ? texts.auth_https_next_retry + ' ' + Math.floor(next / 60) + ':' + String(next % 60).padStart(2, '0') : '';
  }
  async function poll() {
    try {
      const response = await fetch(url, {cache: 'no-store', credentials: 'same-origin', signal: AbortSignal.timeout(8000)});
      if (response.status === 404 || response.status === 410) {
        // An expired bearer link cannot refresh its lease. Offer a fresh email
        // confirmation rather than treating expiry as a network outage.
        complete = true; retryAt = 0; bar.value = 0;
        stage.textContent = texts.auth_https_expired;
        error.textContent = ''; tick();
        const link = document.createElement('a');
        const restart = new URL(url.pathname, location.origin);
        restart.searchParams.set('register', '');
        link.href = restart.href; link.className = 'btn btn-primary'; link.textContent = texts.setup_create_admin;
        root.append(link); return;
      }
      if (!response.ok) throw new Error(texts.auth_https_status_error);
      const result = await response.json();
      if (result.ready) {
        // Return through the original confirmation URL so TLS setup is explicit.
        complete = true; bar.value = 100; stage.textContent = text('ready'); tick();
        const link = document.createElement('a');
        const target = new URL(result.secure_url, location.href);
        target.searchParams.delete('https_status');
        if (target.protocol !== 'https:' || target.hostname !== location.hostname) throw new Error(texts.auth_https_status_error);
        link.href = target.href; link.className = 'btn btn-primary'; link.textContent = texts.auth_https_continue;
        root.append(link); return;
      }
      const progress = result.progress;
      stage.textContent = text(progress.state);
      error.textContent = progress.error || '';
      const timestamp = Date.parse(progress.started_at);
      if (Number.isFinite(timestamp) && timestamp > 0) {
        const serverNow = Date.parse(result.server_now);
        started = Number.isFinite(serverNow) ? Date.now() - (serverNow - timestamp) : timestamp;
      }
      retryAt = Date.parse(progress.retry_at) || 0;
      if (Object.hasOwn(percentages, progress.state)) bar.value = percentages[progress.state];
      events.replaceChildren(...(progress.events || []).map(event => {
        const item = document.createElement('li');
        item.textContent = new Date(event.at).toLocaleTimeString() + ' — ' + text(event.state);
        return item;
      }));
      tick();
    } catch (_) { error.textContent = texts.auth_https_status_error; }
    setTimeout(poll, 3000);
  }
  setInterval(tick, 1000);
  poll();
})();
