(function prepareAccountCodeFields() {
  'use strict';
  const fragment = new URLSearchParams(window.location.hash.slice(1));
  const mailedCode = fragment.get('account-code') || '';
  const codeField = document.querySelector('input[autocomplete="one-time-code"]');
  if (fragment.has('account-code')) {
    history.replaceState(null, '', window.location.pathname + window.location.search);
  }
  if (!codeField) return;
  codeField.setAttribute('enterkeyhint', 'done');
  codeField.setAttribute('dir', 'ltr');
  var submittedFullCode = false;

  function normalizeAndSubmitFullCode() {
    var normalizedCode = String(codeField.value || '').replace(/\D/g, '').slice(0, 6);
    if (codeField.value !== normalizedCode) codeField.value = normalizedCode;
    if (normalizedCode.length !== 6 || submittedFullCode || !codeField.form) return;
    submittedFullCode = true;
    codeField.form.requestSubmit();
  }

  codeField.addEventListener('input', normalizeAndSubmitFullCode);
  codeField.addEventListener('change', function reflectAutofill() {
    codeField.dispatchEvent(new Event('input', {bubbles: true}));
  });
  codeField.addEventListener('paste', function reflectPaste() {
    window.setTimeout(normalizeAndSubmitFullCode, 0);
  });
  if (/^[0-9]{6}$/.test(mailedCode)) {
    codeField.value = mailedCode;
    codeField.dispatchEvent(new Event('input', {bubbles: true}));
  }
})();

(function followAccountDelivery() {
  'use strict';
  const panel = document.querySelector('[data-account-delivery-id]');
  if (!panel || !panel.dataset.accountDeliveryId || typeof WebSocket !== 'function') return;
  const endpoint = new URL(window.location.pathname, window.location.href);
  endpoint.protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
  const parameters = new URLSearchParams({mail_delivery_ws: '', id: panel.dataset.accountDeliveryId});
  if (panel.dataset.accountDeliveryProof) parameters.set('login_challenge', panel.dataset.accountDeliveryProof);
  if (panel.dataset.registrationDeliveryProof) parameters.set('registration_form_token', panel.dataset.registrationDeliveryProof);
  endpoint.search = parameters;
  const connection = new WebSocket(endpoint);
  const statusText = panel.querySelector('[data-account-delivery-status]');
  const deliveryLog = panel.querySelector('[data-account-delivery-log]');
  connection.onmessage = function receiveDeliveryStatus(event) {
    try {
      const report = JSON.parse(event.data);
      const terminal = report.status === 'sent' || report.status === 'failed';
      panel.dataset.deliveryState = report.status === 'sent' ? 'success' : report.status === 'failed' ? 'danger' : 'warning';
      statusText.textContent = report.status === 'sent' ? panel.dataset.sent : report.status === 'failed' ? panel.dataset.failed : panel.dataset.pending;
      deliveryLog.textContent = 'status=' + String(report.status) + '\nattempts=' + Number(report.attempts || 0);
      if (report.error) deliveryLog.textContent += '\n' + report.error;
      if (terminal) connection.close();
    } catch (parseError) {
      panel.dataset.deliveryState = 'danger';
      statusText.textContent = panel.dataset.failed;
      connection.close();
    }
  };
  connection.onerror = function reportConnectionFailure() {
    panel.dataset.deliveryState = 'warning';
    statusText.textContent = panel.dataset.pending;
  };
})();

(function confirmAcceptedEmail() {
  'use strict';
  const confirmationForm = document.querySelector('[data-account-auto-confirm]');
  if (confirmationForm) confirmationForm.requestSubmit();
})();
