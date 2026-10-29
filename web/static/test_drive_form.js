(function initializeTestDriveForm() {
  'use strict';
  const formElement = document.querySelector('[data-test-drive-form]');
  if (!formElement) return;
  const nameElement = formElement.querySelector('[data-test-drive-name]');
  const addressElement = formElement.querySelector('[data-test-drive-address]');
  const domainSuffix = formElement.querySelector('.test-drive-domain-input span').textContent;
  const captchaImageElement = formElement.querySelector('[data-test-drive-captcha-image]');
  const captchaTokenElement = formElement.querySelector('[data-test-drive-captcha-id]');
  const errorElement = formElement.querySelector('[data-test-drive-error]');
  const submitElement = formElement.querySelector('button[type="submit"]');
  const domainInput = window.SiteBrushDomainNameInput;
  const formModel = {name: domainInput.normalize(nameElement.value), captchaImage: '', captchaID: '', captchaFailed: false};

  function render() {
    nameElement.value = formModel.name;
    addressElement.textContent = domainInput.address(formModel.name, domainSuffix.slice(1));
    if (captchaImageElement && formModel.captchaImage) captchaImageElement.src = formModel.captchaImage;
    if (captchaTokenElement) captchaTokenElement.value = formModel.captchaID;
    errorElement.hidden = !formModel.captchaFailed;
    submitElement.disabled = formModel.captchaFailed;
  }

  nameElement.addEventListener('input', function updateName() {
    formModel.name = domainInput.normalize(nameElement.value);
    render();
  });
  render();
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
