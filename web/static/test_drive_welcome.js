(function initializeTestDriveWelcome() {
  'use strict';
  const welcomeElement = document.querySelector('[data-test-drive-welcome]');
  if (!welcomeElement) return;
  const copyButtonElement = welcomeElement.querySelector('[data-test-drive-copy]');
  copyButtonElement.addEventListener('click', function openPrivateCopy() {
    const copyConfiguration = JSON.parse(welcomeElement.dataset.copyConfig);
    copyConfiguration.csrfToken = welcomeElement.dataset.csrf;
    window.SiteBrushCopySite.open(copyConfiguration);
  });
})();
