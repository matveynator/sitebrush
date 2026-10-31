(function enterPrivateTestDrive() {
  'use strict';
  const handoffLink = document.querySelector('[data-test-drive-handoff]');
  if (!handoffLink) return;
  window.location.replace(handoffLink.href);
})();
