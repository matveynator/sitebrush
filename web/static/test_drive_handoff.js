(function enterPrivateTestDrive() {
  'use strict';
  const handoffForm = document.querySelector('[data-test-drive-handoff]');
  if (!handoffForm) return;
  handoffForm.submit();
})();
