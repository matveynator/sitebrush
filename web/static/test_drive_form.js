(function initializeTestDriveForm() {
  'use strict';
  const formElement = document.querySelector('[data-test-drive-form]');
  if (!formElement) return;
  const nameElement = formElement.querySelector('[data-test-drive-name]');
  const addressElement = formElement.querySelector('[data-test-drive-address]');
  const statusElement = formElement.querySelector('[data-test-drive-domain-status]');
  const domainFieldElement = formElement.querySelector('.test-drive-domain-input');
  const parentDomain = domainFieldElement.querySelector('span').textContent.slice(1);
  const captchaImageElement = formElement.querySelector('[data-test-drive-captcha-image]');
  const captchaTokenElement = formElement.querySelector('[data-test-drive-captcha-id]');
  const captchaRetryElement = formElement.querySelector('[data-test-drive-captcha-retry]');
  const errorElement = formElement.querySelector('[data-test-drive-error]');
  const submitElement = formElement.querySelector('button[type="submit"]');
  const domainInput = window.SiteBrushDomainNameInput;
  const formModel = {
    name: domainInput.normalize(nameElement.value),
    domainState: '',
    checkSequence: 0,
    checkTimer: 0,
    checkController: null,
    captchaImage: '',
    captchaID: '',
    captchaFailed: false,
    captchaLoading: false,
    captchaRetryTimer: 0
  };

  function render() {
    nameElement.value = formModel.name;
    addressElement.textContent = domainInput.address(formModel.name, parentDomain);
    addressElement.dataset.state = formModel.domainState;
    statusElement.dataset.state = formModel.domainState;
    statusElement.textContent = statusElement.dataset[formModel.domainState] || '';
    statusElement.hidden = !statusElement.textContent;
    domainFieldElement.dataset.state = formModel.domainState;
    if (captchaImageElement && formModel.captchaImage) captchaImageElement.src = formModel.captchaImage;
    if (captchaTokenElement) captchaTokenElement.value = formModel.captchaID;
    errorElement.hidden = !formModel.captchaFailed;
    if (captchaRetryElement) captchaRetryElement.disabled = formModel.captchaLoading;
    submitElement.disabled = formModel.captchaFailed || Boolean(captchaImageElement && !formModel.captchaID) || !domainInput.valid(formModel.name) || formModel.domainState === 'checking' || formModel.domainState === 'unavailable';
  }

  function loadCaptcha() {
    if (!captchaImageElement || formModel.captchaLoading) return;
    formModel.captchaLoading = true;
    formModel.captchaFailed = false;
    render();
    fetch('/?trial_site_captcha', {headers: {Accept: 'application/json'}, cache: 'no-store'})
      .then(function readChallenge(response) {
        if (response.status === 429) {
          const retrySeconds = Number(response.headers.get('Retry-After')) || 60;
          formModel.captchaRetryTimer = window.setTimeout(function retryCaptchaAfterLimit() {
            formModel.captchaRetryTimer = 0;
            loadCaptcha();
          }, Math.max(1, retrySeconds) * 1000);
        }
        if (!response.ok) throw new Error();
        return response.json();
      })
      .then(function showChallenge(challenge) {
        formModel.captchaImage = challenge.image;
        formModel.captchaID = challenge.captcha_id;
        formModel.captchaFailed = false;
        formModel.captchaLoading = false;
        render();
      })
      .catch(function showChallengeError() {
        if (!formModel.captchaRetryTimer) {
          formModel.captchaRetryTimer = window.setTimeout(function retryCaptchaAfterFailure() {
            formModel.captchaRetryTimer = 0;
            loadCaptcha();
          }, 3000);
        }
        formModel.captchaFailed = true;
        formModel.captchaLoading = false;
        render();
      });
  }

  function checkDomainAvailability(name, sequence) {
    const checkURL = new URL('/?trial_site_domain_check', window.location.href);
    checkURL.searchParams.set('subdomain', name);
    formModel.checkController = new AbortController();
    fetch(checkURL.toString(), {headers: {Accept: 'application/json'}, signal: formModel.checkController.signal})
      .then(function readAvailability(response) {
        if (response.headers.get('X-SiteBrush-Captcha-Required') === '1' && !captchaImageElement) {
          const retryURL = new URL('/?test_drive', window.location.href);
          retryURL.searchParams.set('subdomain', formModel.name);
          window.location.replace(retryURL.toString());
          return null;
        }
        if (response.status === 429) throw new Error('limited');
        if (!response.ok) throw new Error('failed');
        return response.json();
      })
      .then(function showAvailability(availability) {
        if (!availability) return;
        if (sequence !== formModel.checkSequence || name !== formModel.name) return;
        const expectedDomain = domainInput.address(name, parentDomain);
        formModel.domainState = availability.domain === expectedDomain ? (availability.available ? 'available' : 'unavailable') : 'failed';
        render();
      })
      .catch(function showAvailabilityError(error) {
        if (error.name === 'AbortError' || sequence !== formModel.checkSequence) return;
        formModel.domainState = error.message === 'limited' ? 'limited' : 'failed';
        render();
      });
  }

  function scheduleDomainCheck() {
    if (formModel.checkTimer) clearTimeout(formModel.checkTimer);
    if (formModel.checkController) formModel.checkController.abort();
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
  formElement.addEventListener('submit', function validateDomainBeforeStart(event) {
    if (submitElement.disabled) event.preventDefault();
  });
  if (formModel.name && captchaImageElement) render();
  else scheduleDomainCheck();
  if (!captchaImageElement) return;
  captchaRetryElement.addEventListener('click', function requestNewCaptcha() {
    if (formModel.captchaRetryTimer) {
      window.clearTimeout(formModel.captchaRetryTimer);
      formModel.captchaRetryTimer = 0;
    }
    loadCaptcha();
  });
  loadCaptcha();
})();
