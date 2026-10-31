(function initializeTestDriveWelcome() {
  'use strict';
  const welcomeElement = document.querySelector('[data-test-drive-welcome]');
  if (!welcomeElement) return;
  const copyButtonElement = welcomeElement.querySelector('[data-test-drive-copy]');
  function openPrivateCopy() {
    const copyConfiguration = JSON.parse(welcomeElement.dataset.copyConfig);
    copyConfiguration.csrfToken = welcomeElement.dataset.csrf;
    window.SiteBrushCopySite.open(copyConfiguration);
  }
  copyButtonElement.addEventListener('click', openPrivateCopy);
  const initialConfiguration = JSON.parse(welcomeElement.dataset.copyConfig);
  if (initialConfiguration.sourceURL) openPrivateCopy();
})();
