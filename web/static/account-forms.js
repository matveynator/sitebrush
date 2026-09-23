(function prepareAccountCodeFields() {
  'use strict';
  const settings = document.querySelector('[data-account-code-tools]');
  if (!settings) return;
  const codeFields = document.querySelectorAll('input[autocomplete="one-time-code"]');
  for (const codeField of codeFields) {
    codeField.setAttribute('inputmode', 'numeric');
    codeField.setAttribute('enterkeyhint', 'done');
    codeField.setAttribute('dir', 'ltr');
    codeField.setAttribute('spellcheck', 'false');
    codeField.setAttribute('autocapitalize', 'off');
    const controls = document.createElement('div');
    controls.className = 'auth-code-tools';
    const copyButton = document.createElement('button');
    copyButton.type = 'button';
    copyButton.className = 'btn btn-outline-secondary';
    copyButton.textContent = settings.getAttribute('data-copy-label');
    const copyStatus = document.createElement('span');
    copyStatus.setAttribute('role', 'status');
    copyStatus.setAttribute('aria-live', 'polite');
    controls.append(copyButton, copyStatus);
    const fieldLabel = codeField.closest('label');
    (fieldLabel || codeField).insertAdjacentElement('afterend', controls);
    function updateCodeControls() {
      copyButton.disabled = !/^[0-9]{6}$/.test(codeField.value);
      copyStatus.textContent = '';
    }
    codeField.addEventListener('input', updateCodeControls);
    codeField.addEventListener('change', function reflectAutofill() {
      codeField.dispatchEvent(new Event('input', {bubbles: true}));
    });
    copyButton.addEventListener('click', async function copyEnteredCode() {
      try {
        if (!navigator.clipboard || !window.isSecureContext) throw new Error('Clipboard unavailable');
        await navigator.clipboard.writeText(codeField.value);
        copyStatus.textContent = settings.getAttribute('data-copy-success');
      } catch (copyError) {
        codeField.focus();
        codeField.select();
        copyStatus.textContent = settings.getAttribute('data-copy-error');
      }
    });
    updateCodeControls();
  }
})();
