(function keepPrivateTestDriveOnRenderedSite() {
  'use strict';
  const preservedHash = window.location.hash === '#test-drive-copy' || window.location.hash === '#activation'
    ? window.location.hash
    : '';
  if (!preservedHash) return;

  // The initial handoff keeps its editor document because the unpublished
  // landing page cannot serve a second request yet. Ordinary editor routes
  // retain their query so owners can edit copied pages.
  document.body.classList.add('test-drive-entry');
  window.history.replaceState(null, '', window.location.pathname + preservedHash);
})();
