(function initializePrivateTestDrive() {
  'use strict';
  const rootElement = document.querySelector('[data-test-drive-save]');
  if (!rootElement) return;
  const noticeElement = rootElement.querySelector('[data-trial-notice]');
  const overlayElement = rootElement.querySelector('[data-trial-overlay]');
  const emailFormElement = rootElement.querySelector('[data-trial-email-form]');
  const codeFormElement = rootElement.querySelector('[data-trial-code-form]');
  const activateFormElement = rootElement.querySelector('[data-trial-activate-form]');
  const codeStageElement = rootElement.querySelector('[data-trial-code-stage]');
  const currentEmailElement = rootElement.querySelector('[data-trial-current-email]');
  const errorElement = rootElement.querySelector('[data-trial-error]');
  const reasonElement = rootElement.querySelector('[data-trial-reason]');
  const captchaElement = rootElement.querySelector('[data-trial-captcha]');
  const captchaImageElement = rootElement.querySelector('[data-trial-captcha-image]');
  const captchaIDElement = rootElement.querySelector('[data-trial-captcha-id]');
  const captchaAnswerElement = captchaElement.querySelector('input[name="captcha"]');
  const resendElement = rootElement.querySelector('[data-trial-resend]');
  const defaultErrorText = errorElement.textContent;
  const createdAt = Date.parse(rootElement.dataset.createdAt) || Date.now();
  const trialModel = {email: '', emailDraft: '', changingEmail: false, status: 'private', open: false, required: false, humanVerified: true, retryUntil: 0, error: '', captchaImage: '', captchaID: '', captchaAnswer: '', captchaIssuedAt: 0, captchaLoading: false, prompted: false};

  function render() {
    noticeElement.hidden = trialModel.status === 'activated' || trialModel.open || Date.now() - createdAt < 60000;
    overlayElement.hidden = !trialModel.open || trialModel.status === 'activated';
    emailFormElement.hidden = trialModel.status === 'email_verified' || trialModel.email !== '' && !trialModel.changingEmail;
    captchaElement.hidden = trialModel.humanVerified;
    captchaAnswerElement.required = !trialModel.humanVerified;
    captchaAnswerElement.value = trialModel.captchaAnswer;
    codeStageElement.hidden = trialModel.email === '' || trialModel.status === 'email_verified' || trialModel.changingEmail;
    activateFormElement.hidden = trialModel.status !== 'email_verified';
    rootElement.querySelector('[data-trial-mail-links]').hidden = !trialModel.humanVerified;
    currentEmailElement.textContent = trialModel.email;
    emailFormElement.elements.email.value = trialModel.emailDraft;
    if (trialModel.captchaImage) captchaImageElement.src = trialModel.captchaImage;
    else captchaImageElement.removeAttribute('src');
    captchaIDElement.value = trialModel.captchaID;
    errorElement.textContent = trialModel.error || defaultErrorText;
    errorElement.hidden = trialModel.error === '';
    reasonElement.hidden = !trialModel.required;
    rootElement.querySelector('[data-trial-close]').hidden = trialModel.required;
    const retrySeconds = Math.max(0, Math.ceil((trialModel.retryUntil - Date.now()) / 1000));
    resendElement.disabled = retrySeconds > 0;
    resendElement.textContent = retrySeconds > 0 ? resendElement.dataset.wait.replace('%s', String(retrySeconds)) : resendElement.dataset.label;
  }

  function showError(error) {
    trialModel.error = error && error.message ? error.message : defaultErrorText;
    render();
  }
  function clearError() { trialModel.error = ''; render(); }
  function loadCaptcha() {
    trialModel.captchaLoading = true;
    window.SiteBrushTestDriveSessionCommand('captcha')
      .then(function showCaptcha(challenge) { trialModel.captchaLoading = false; trialModel.captchaImage = challenge.image; trialModel.captchaID = challenge.captcha_id; trialModel.captchaAnswer = ''; trialModel.captchaIssuedAt = Date.now(); render(); })
      .catch(function reportCaptchaError(error) { trialModel.captchaLoading = false; showError(error); });
  }
  function ensureCaptcha() {
    if (!trialModel.humanVerified && !trialModel.captchaLoading && (!trialModel.captchaID || Date.now() - trialModel.captchaIssuedAt > 240000)) loadCaptcha();
  }
  function refreshStatus() {
    return window.SiteBrushTestDriveSessionCommand('status')
      .then(function applyStatus(status) {
        const needsCaptcha = !status.human_verified;
        trialModel.status = status.status;
        trialModel.email = status.email || '';
        if (trialModel.email && !trialModel.changingEmail) trialModel.emailDraft = trialModel.email;
        trialModel.humanVerified = status.human_verified;
        trialModel.retryUntil = Date.now() + (status.retry_after || 0) * 1000;
        if (needsCaptcha && trialModel.open) ensureCaptcha();
        render();
      });
  }
  function openSaveDialog(required) {
    trialModel.open = true;
    trialModel.required = Boolean(required);
    ensureCaptcha();
    render();
  }

  function submitAction(queryFlag, formElement) {
    const formBody = new FormData(formElement);
    if (!trialModel.humanVerified) {
      formBody.set('captcha', trialModel.captchaAnswer);
      formBody.set('captcha_id', trialModel.captchaID);
    }
    if (queryFlag === 'test_drive_email') {
      return window.SiteBrushTestDriveSessionCommand('email', {
        email: String(formBody.get('email') || ''),
        captcha: String(formBody.get('captcha') || ''),
        captcha_id: String(formBody.get('captcha_id') || ''),
        csrf: rootElement.dataset.csrf
      });
    }
    // Activation must use HTTP so the browser accepts the new HttpOnly session cookie.
    formBody.set('account_csrf', rootElement.dataset.csrf);
    return fetch('/?' + queryFlag, {method: 'POST', body: formBody, credentials: 'same-origin'})
      .then(function readAction(response) {
        if (!response.ok) return response.text().then(function reportActionError(body) { throw new Error(body.trim() || defaultErrorText); });
        return response.json();
      });
  }

  emailFormElement.elements.email.addEventListener('input', function updateEmailDraft() {
    trialModel.emailDraft = emailFormElement.elements.email.value;
    render();
  });
  captchaAnswerElement.addEventListener('input', function updateCaptchaAnswer() {
    trialModel.captchaAnswer = captchaAnswerElement.value;
    render();
  });

  emailFormElement.addEventListener('submit', function sendConfirmation(event) {
    event.preventDefault();
    trialModel.emailDraft = emailFormElement.elements.email.value;
    clearError();
    submitAction('test_drive_email', emailFormElement)
      .then(function showCodeStep(result) { trialModel.email = result.email; trialModel.emailDraft = result.email; trialModel.changingEmail = false; trialModel.status = result.status; trialModel.humanVerified = true; trialModel.captchaID = ''; trialModel.retryUntil = Date.now() + result.retry_after * 1000; render(); })
      .catch(function reportEmailError(error) { trialModel.captchaID = ''; showError(error); refreshStatus().catch(showError); });
  });
  codeFormElement.addEventListener('submit', function verifyConfirmation(event) {
    event.preventDefault();
    clearError();
    submitAction('test_drive_verify', codeFormElement)
      .then(function confirmEmail(result) { trialModel.status = result.status; trialModel.email = result.email; trialModel.humanVerified = true; trialModel.captchaID = ''; render(); })
      .catch(function reportInvalidCode(error) { trialModel.captchaID = ''; showError(error); refreshStatus().catch(showError); });
  });
  activateFormElement.addEventListener('submit', function activateSite(event) {
    event.preventDefault();
    clearError();
    submitAction('test_drive_activate', activateFormElement)
      .then(function finishTrial(result) { trialModel.status = result.status; trialModel.open = false; render(); })
      .catch(function reportActivationError(error) { trialModel.captchaID = ''; showError(error); refreshStatus().catch(showError); });
  });
  resendElement.addEventListener('click', function resendCode() {
    trialModel.emailDraft = trialModel.email;
    render();
    clearError();
    submitAction('test_drive_email', emailFormElement)
      .then(function showResendTimer(result) { trialModel.humanVerified = true; trialModel.captchaID = ''; trialModel.retryUntil = Date.now() + result.retry_after * 1000; render(); })
      .catch(function reportResendError(error) { trialModel.captchaID = ''; showError(error); refreshStatus().catch(showError); });
  });
  rootElement.querySelector('[data-trial-change]').addEventListener('click', function changeEmail() {
    trialModel.changingEmail = true;
    trialModel.emailDraft = '';
    render();
    emailFormElement.elements.email.focus();
  });
  rootElement.querySelector('[data-trial-close]').addEventListener('click', function closeDialog() {
    if (trialModel.required) return;
    trialModel.open = false;
    render();
  });
  noticeElement.addEventListener('click', function openFromNotice() { openSaveDialog(false); });
  document.addEventListener('click', function requireEmailForExternalAction(event) {
    if (trialModel.status === 'activated') return;
    const actionElement = event.target.closest('[data-sitebrush-action]');
    if (!actionElement) return;
    const actionName = actionElement.getAttribute('data-sitebrush-action');
    if (!['publish', 'freeze', 'unfreeze', 'copy_site', 'protect_password', 'remove_password_protection', 'delete'].includes(actionName)) return;
    event.preventDefault();
    event.stopImmediatePropagation();
    openSaveDialog(true);
  }, true);
  window.setInterval(function updateTrialReminder() {
    if (trialModel.status === 'activated') return;
    if (Date.now() - createdAt >= 180000 && !trialModel.open && !trialModel.prompted) {
      trialModel.prompted = true;
      openSaveDialog(false);
    }
    render();
  }, 1000);
  window.SiteBrushRequireTestDriveEmail = function requireTestDriveEmail() { openSaveDialog(true); };
  refreshStatus().catch(showError);
  render();
})();
