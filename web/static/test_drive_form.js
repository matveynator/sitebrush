(function initializeTestDriveForm() {
  'use strict';
  const formElement = document.querySelector('[data-test-drive-form]');
  if (!formElement) return;
  const nameElement = formElement.querySelector('[data-test-drive-name]');
  const sourceElement = formElement.querySelector('[data-test-drive-source]');
  const sourceStepElement = formElement.querySelector('[data-test-drive-source-step]');
  const captchaStepElement = formElement.querySelector('[data-test-drive-captcha-step]');
  const detailsStepElement = formElement.querySelector('[data-test-drive-details-step]');
  const nextElement = formElement.querySelector('[data-test-drive-next]');
  const backElement = formElement.querySelector('[data-test-drive-back]');
  const sourceBackElement = formElement.querySelector('[data-test-drive-source-back]');
  const emailElement = formElement.elements.email;
  const addressElement = formElement.querySelector('[data-test-drive-address]');
  const statusElement = formElement.querySelector('[data-test-drive-domain-status]');
  const domainFieldElement = formElement.querySelector('.test-drive-domain-input');
  const parentDomain = domainFieldElement.querySelector('span').textContent.slice(1);
  const captchaImageElement = formElement.querySelector('[data-test-drive-captcha-image]');
  const captchaElement = formElement.querySelector('[data-test-drive-captcha]');
  const captchaAnswerElement = captchaElement.querySelector('input[name="captcha"]');
  const captchaTokenElement = formElement.querySelector('[data-test-drive-captcha-id]');
  const captchaRetryElement = formElement.querySelector('[data-test-drive-captcha-retry]');
  const captchaConfirmElement = formElement.querySelector('[data-test-drive-captcha-confirm]');
  const errorElement = formElement.querySelector('[data-test-drive-error]');
  const submitElement = formElement.querySelector('button[type="submit"]');
  const domainInput = window.SiteBrushDomainNameInput;
  const formModel = {
    sourceURL: sourceElement.value,
    step: formElement.dataset.showDetails === 'true' ? (formElement.dataset.humanVerified === 'true' ? 'details' : 'captcha') : 'source',
    name: domainInput.normalize(nameElement.value),
    email: emailElement.value,
    captchaAnswer: '',
    domainState: '',
    checkSequence: 0,
    checkTimer: 0,
    humanVerified: formElement.dataset.humanVerified === 'true',
    captchaImage: '',
    captchaID: '',
    captchaFailed: false,
    captchaLoading: false,
    captchaRetryTimer: 0
  };

  function render() {
    sourceElement.value = formModel.sourceURL;
    sourceStepElement.hidden = formModel.step !== 'source';
    captchaStepElement.hidden = formModel.step !== 'captcha';
    detailsStepElement.hidden = formModel.step !== 'details';
    nextElement.disabled = !sourceURLValid(formModel.sourceURL);
    nameElement.value = formModel.name;
    emailElement.value = formModel.email;
    captchaAnswerElement.value = formModel.captchaAnswer;
    addressElement.textContent = domainInput.address(formModel.name, parentDomain);
    addressElement.dataset.state = formModel.domainState;
    statusElement.dataset.state = formModel.domainState;
    statusElement.textContent = statusElement.dataset[formModel.domainState] || '';
    statusElement.hidden = !statusElement.textContent;
    domainFieldElement.dataset.state = formModel.domainState;
    formElement.dataset.humanVerified = String(formModel.humanVerified);
    captchaAnswerElement.required = formModel.step === 'captcha';
    if (captchaImageElement && formModel.captchaImage) captchaImageElement.src = formModel.captchaImage;
    if (captchaTokenElement) captchaTokenElement.value = formModel.captchaID;
    errorElement.hidden = formModel.humanVerified || !formModel.captchaFailed;
    if (captchaRetryElement) captchaRetryElement.disabled = formModel.captchaLoading;
    captchaConfirmElement.disabled = formModel.captchaLoading || formModel.captchaFailed || !formModel.captchaID || !formModel.captchaAnswer.trim();
    submitElement.disabled = formModel.step !== 'details' || !formModel.humanVerified || !domainInput.valid(formModel.name) || formModel.domainState === 'checking' || formModel.domainState === 'unavailable';
  }

  function sourceURLValid(sourceURL) {
    try {
      const candidateURL = sourceURL.includes('://') ? sourceURL : 'https://' + sourceURL;
      const parsedURL = new URL(candidateURL);
      return (parsedURL.protocol === 'http:' || parsedURL.protocol === 'https:') && Boolean(parsedURL.hostname) && !parsedURL.username && !parsedURL.password;
    } catch (error) {
      return false;
    }
  }

  function suggestedDomainName(sourceURL) {
    let sourceHost;
    try {
      const candidateURL = sourceURL.includes('://') ? sourceURL : 'https://' + sourceURL;
      sourceHost = new URL(candidateURL).hostname.toLowerCase().replace(/^www\./, '');
    } catch (error) {
      return '';
    }
    const hostLabels = sourceHost.split('.');
    const proposedLabel = hostLabels[0];
    return proposedLabel.replace(/[^a-z0-9-]/g, '-').replace(/^-+|-+$/g, '').slice(0, 24).replace(/-+$/g, '');
  }

  function loadCaptcha() {
    if (formModel.humanVerified || formModel.captchaLoading) return;
    formModel.captchaLoading = true;
    formModel.captchaFailed = false;
    render();
    window.SiteBrushTestDriveSessionCommand('captcha', {}, 'test_drive_entry_ws')
      .then(function showChallenge(challenge) {
        formModel.captchaImage = challenge.image;
        formModel.captchaID = challenge.captcha_id;
        formModel.captchaAnswer = '';
        formModel.captchaFailed = false;
        formModel.captchaLoading = false;
        render();
      })
      .catch(function showChallengeError(error) {
        if (!formModel.captchaRetryTimer) {
          const retrySeconds = error.status === 429 ? Math.max(1, error.retryAfter || 60) : 3;
          formModel.captchaRetryTimer = window.setTimeout(function retryCaptchaAfterFailure() {
            formModel.captchaRetryTimer = 0;
            loadCaptcha();
          }, retrySeconds * 1000);
        }
        formModel.captchaFailed = true;
        formModel.captchaLoading = false;
        render();
      });
  }

  function restoreCaptcha(required) {
    if (!required || !formModel.humanVerified) return;
    formModel.humanVerified = false;
    formModel.step = 'captcha';
    loadCaptcha();
  }

  function checkDomainAvailability(name, sequence) {
    window.SiteBrushTestDriveSessionCommand('domain_check', {subdomain: name}, 'test_drive_entry_ws')
      .then(function showAvailability(availability) {
        restoreCaptcha(availability.captcha_required);
        if (sequence !== formModel.checkSequence || name !== formModel.name) return;
        const expectedDomain = domainInput.address(name, parentDomain);
        formModel.domainState = availability.domain === expectedDomain ? (availability.available ? 'available' : 'unavailable') : 'failed';
        render();
      })
      .catch(function showAvailabilityError(error) {
        restoreCaptcha(error.captchaRequired);
        if (sequence !== formModel.checkSequence) return;
        formModel.domainState = error.status === 429 ? 'limited' : 'failed';
        render();
      });
  }

  function scheduleDomainCheck() {
    if (formModel.checkTimer) clearTimeout(formModel.checkTimer);
    formModel.checkSequence++;
    if (!formModel.name) formModel.domainState = '';
    else if (!domainInput.valid(formModel.name)) formModel.domainState = 'invalid';
    else {
      formModel.domainState = 'checking';
      const checkedName = formModel.name;
      const sequence = formModel.checkSequence;
      formModel.checkTimer = setTimeout(function checkAfterTyping() {
        formModel.checkTimer = 0;
        checkDomainAvailability(checkedName, sequence);
      }, 450);
    }
    render();
  }

  nameElement.addEventListener('input', function updateName() {
    formModel.name = domainInput.normalize(nameElement.value);
    scheduleDomainCheck();
  });
  sourceElement.addEventListener('input', function updateSource() {
    formModel.sourceURL = sourceElement.value;
    render();
  });
  nextElement.addEventListener('click', function showCaptchaStep() {
    if (!sourceElement.reportValidity() || !sourceURLValid(formModel.sourceURL)) return;
    if (!formModel.name) formModel.name = suggestedDomainName(formModel.sourceURL);
    formModel.step = formModel.humanVerified ? 'details' : 'captcha';
    if (formModel.step === 'captcha') loadCaptcha();
    else scheduleDomainCheck();
    render();
  });
  backElement.addEventListener('click', function showSourceStep() {
    formModel.step = 'source';
    render();
    sourceElement.focus();
  });
  sourceBackElement.addEventListener('click', function showPreviousSourceStep() {
    formModel.step = 'source';
    render();
    sourceElement.focus();
  });
  emailElement.addEventListener('input', function updateEmail() {
    formModel.email = emailElement.value;
    render();
  });
  captchaAnswerElement.addEventListener('input', function updateCaptchaAnswer() {
    formModel.captchaAnswer = captchaAnswerElement.value;
    render();
  });
  captchaConfirmElement.addEventListener('click', function confirmCaptcha() {
    if (captchaConfirmElement.disabled) return;
    captchaConfirmElement.disabled = true;
    window.SiteBrushTestDriveSessionCommand('confirm_captcha', {captcha_id: formModel.captchaID, captcha: formModel.captchaAnswer}, 'test_drive_entry_ws')
      .then(function showAddressAfterCaptcha() {
        formModel.humanVerified = true;
        formModel.step = 'details';
        formModel.captchaFailed = false;
        scheduleDomainCheck();
        nameElement.focus();
      })
      .catch(function showCaptchaFailure() {
        formModel.captchaFailed = true;
        formModel.captchaID = '';
        formModel.captchaAnswer = '';
        render();
        loadCaptcha();
      });
  });
  formElement.addEventListener('submit', function validateDomainBeforeStart(event) {
    if (formModel.step !== 'details' || submitElement.disabled) event.preventDefault();
  });
  if (formModel.step === 'details') scheduleDomainCheck();
  captchaRetryElement.addEventListener('click', function requestNewCaptcha() {
    if (formModel.captchaRetryTimer) {
      window.clearTimeout(formModel.captchaRetryTimer);
      formModel.captchaRetryTimer = 0;
    }
    loadCaptcha();
  });
  if (formModel.step === 'captcha') loadCaptcha();
  render();
})();
