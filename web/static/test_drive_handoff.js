(function preparePrivateTestDriveHandoff() {
  'use strict';
  const handoffForm = document.querySelector('[data-test-drive-handoff]');
  if (!handoffForm) return;
  if (handoffForm.dataset.local === 'true') {
    handoffForm.submit();
    return;
  }
  const targetAddress = new URL(handoffForm.action);
  const probeAddress = targetAddress.origin + '/?test_drive_handoff_ready';

  function checkHTTPSCertificate() {
    fetch(probeAddress, {method: 'GET', mode: 'no-cors', cache: 'no-store', referrerPolicy: 'no-referrer'})
      .then(function handoffWhenReady() { handoffForm.submit(); })
      .catch(function retryWhenCertificateIsReady() { window.setTimeout(checkHTTPSCertificate, 3000); });
  }

  checkHTTPSCertificate();
})();
