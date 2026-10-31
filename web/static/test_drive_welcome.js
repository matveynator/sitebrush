(function initializeTestDriveWelcome() {
  'use strict';
  const welcomeElement = document.querySelector('[data-test-drive-welcome]');
  if (!welcomeElement) return;
  const copyButtonElement = welcomeElement.querySelector('[data-test-drive-copy]');
  const sourceFormElement = welcomeElement.querySelector('[data-test-drive-source-form]');
  const sourceErrorElement = welcomeElement.querySelector('[data-test-drive-source-error]');
  function openPrivateCopy() {
    const copyConfiguration = JSON.parse(welcomeElement.dataset.copyConfig);
    copyConfiguration.csrfToken = welcomeElement.dataset.csrf;
    window.SiteBrushCopySite.open(copyConfiguration);
  }
  copyButtonElement.addEventListener('click', openPrivateCopy);
  sourceFormElement.addEventListener('submit', function changeSource(submitEvent) {
    submitEvent.preventDefault();
    const submitButtonElement = sourceFormElement.querySelector('button[type="submit"]');
    const sourceURL = String(new FormData(sourceFormElement).get('source_url') || '').trim();
    const requestBody = new URLSearchParams();
    requestBody.set('source_url', sourceURL);
    requestBody.set('account_csrf', welcomeElement.dataset.csrf);
    submitButtonElement.disabled = true;
    sourceErrorElement.hidden = true;
    fetch('/?test_drive_source', {method: 'POST', body: requestBody, headers: {'Accept': 'application/json', 'X-SiteBrush-CSRF': welcomeElement.dataset.csrf}})
      .then(function readSourceResponse(response) {
        if (!response.ok) throw new Error(sourceFormElement.dataset.errorText);
        return response.json();
      })
      .then(function showChangedSource() { window.location.reload(); })
      .catch(function showSourceError(error) {
        submitButtonElement.disabled = false;
        sourceErrorElement.textContent = error.message;
        sourceErrorElement.hidden = false;
      });
  });
  const initialConfiguration = JSON.parse(welcomeElement.dataset.copyConfig);
  if (window.location.hash === '#activation') {
    const activationButtonElement = document.querySelector('[data-trial-open]');
    if (activationButtonElement) activationButtonElement.click();
    return;
  }
})();
