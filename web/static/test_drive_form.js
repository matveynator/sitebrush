(function initializeTestDriveForm() {
  'use strict';
  const formElement = document.querySelector('[data-test-drive-form]');
  if (!formElement) return;
  const nameElement = formElement.querySelector('[data-test-drive-name]');
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
  const errorElement = formElement.querySelector('[data-test-drive-error]');
  const submitElement = formElement.querySelector('button[type="submit"]');
  const domainInput = window.SiteBrushDomainNameInput;
  const formModel = {
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
    nameElement.value = formModel.name;
    emailElement.value = formModel.email;
    captchaAnswerElement.value = formModel.captchaAnswer;
    addressElement.textContent = domainInput.address(formModel.name, parentDomain);
    addressElement.dataset.state = formModel.domainState;
    statusElement.dataset.state = formModel.domainState;
    statusElement.textContent = statusElement.dataset[formModel.domainState] || '';
    statusElement.hidden = !statusElement.textContent;
    domainFieldElement.dataset.state = formModel.domainState;
    captchaElement.hidden = formModel.humanVerified;
    captchaAnswerElement.required = !formModel.humanVerified;
    if (captchaImageElement && formModel.captchaImage) captchaImageElement.src = formModel.captchaImage;
    if (captchaTokenElement) captchaTokenElement.value = formModel.captchaID;
    errorElement.hidden = formModel.humanVerified || !formModel.captchaFailed;
    if (captchaRetryElement) captchaRetryElement.disabled = formModel.captchaLoading;
    submitElement.disabled = !formModel.humanVerified && (formModel.captchaFailed || !formModel.captchaID) || !domainInput.valid(formModel.name) || formModel.domainState === 'checking' || formModel.domainState === 'unavailable';
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
  emailElement.addEventListener('input', function updateEmail() {
    formModel.email = emailElement.value;
    render();
  });
  captchaAnswerElement.addEventListener('input', function updateCaptchaAnswer() {
    formModel.captchaAnswer = captchaAnswerElement.value;
    render();
  });
  formElement.addEventListener('submit', function validateDomainBeforeStart(event) {
    if (submitElement.disabled) event.preventDefault();
  });
  scheduleDomainCheck();
  captchaRetryElement.addEventListener('click', function requestNewCaptcha() {
    if (formModel.captchaRetryTimer) {
      window.clearTimeout(formModel.captchaRetryTimer);
      formModel.captchaRetryTimer = 0;
    }
    loadCaptcha();
  });
  loadCaptcha();
})();
