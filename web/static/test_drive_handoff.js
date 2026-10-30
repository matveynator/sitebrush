(function preparePrivateTestDriveHandoff() {
  'use strict';
  const handoffForm = document.querySelector('[data-test-drive-handoff]');
  if (!handoffForm) return;
  const continueButton = handoffForm.querySelector('button[type="submit"]');
  const handoffStatus = document.querySelector('[data-test-drive-handoff-status]');
  let addressReady = false;
  handoffForm.addEventListener('submit', function preventUnsafeHandoff(event) {
    if (!addressReady) event.preventDefault();
  });
  if (handoffForm.dataset.local === 'true') {
    addressReady = true;
    continueButton.disabled = false;
    handoffForm.submit();
    return;
  }
  const targetAddress = new URL(handoffForm.action);
  const probeAddress = targetAddress.origin + '/?test_drive_handoff_ready';

  window.setTimeout(function showPendingCertificate() {
    if (!addressReady) handoffStatus.textContent = handoffStatus.dataset.pending;
  }, 15000);

  function checkHTTPSCertificate() {
    fetch(probeAddress, {method: 'GET', mode: 'no-cors', cache: 'no-store', referrerPolicy: 'no-referrer'})
      .then(function handoffWhenReady() {
        addressReady = true;
        continueButton.disabled = false;
        handoffForm.submit();
      })
      .catch(function retryWhenCertificateIsReady() { window.setTimeout(checkHTTPSCertificate, 3000); });
  }

  checkHTTPSCertificate();
})();
