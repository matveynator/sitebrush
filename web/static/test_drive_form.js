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
    captchaFailed: false
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
    submitElement.disabled = formModel.captchaFailed || !domainInput.valid(formModel.name) || formModel.domainState === 'checking' || formModel.domainState === 'unavailable';
  }

  function checkDomainAvailability(name, sequence) {
    const checkURL = new URL('/?trial_site_domain_check', window.location.href);
    checkURL.searchParams.set('subdomain', name);
    formModel.checkController = new AbortController();
    fetch(checkURL.toString(), {headers: {Accept: 'application/json'}, signal: formModel.checkController.signal})
      .then(function readAvailability(response) {
        if (response.status === 429) throw new Error('limited');
        if (!response.ok) throw new Error('failed');
        return response.json();
      })
      .then(function showAvailability(availability) {
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
  scheduleDomainCheck();
  if (!captchaImageElement) return;
  fetch('/?trial_site_captcha', {headers: {Accept: 'application/json'}})
    .then(function readChallenge(response) {
      if (!response.ok) throw new Error();
      return response.json();
    })
    .then(function showChallenge(challenge) {
      formModel.captchaImage = challenge.image;
      formModel.captchaID = challenge.captcha_id;
      render();
    })
    .catch(function showChallengeError() {
      formModel.captchaFailed = true;
      render();
    });
})();
