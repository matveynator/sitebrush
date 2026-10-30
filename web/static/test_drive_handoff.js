(function preparePrivateTestDriveHandoff() {
  'use strict';
  const handoffForm = document.querySelector('[data-test-drive-handoff]');
  if (!handoffForm) return;
  const continueButton = handoffForm.querySelector('button[type="submit"]');
  const handoffStatus = document.querySelector('[data-test-drive-handoff-status]');
  const handoffModel = {addressReady: false, pending: false, retryTimer: 0};

  function render() {
    continueButton.disabled = !handoffModel.addressReady;
    if (handoffModel.pending) handoffStatus.textContent = handoffStatus.dataset.pending;
  }

  handoffForm.addEventListener('submit', function preventUnsafeHandoff(event) {
    if (!handoffModel.addressReady) event.preventDefault();
  });
  if (handoffForm.dataset.local === 'true') {
    handoffModel.addressReady = true;
    render();
    handoffForm.submit();
    return;
  }
  const targetAddress = new URL(handoffForm.action);
  const probeAddress = new URL('/?test_drive_handoff_ready', targetAddress);
  probeAddress.protocol = 'wss:';

  window.setTimeout(function showPendingCertificate() {
    if (!handoffModel.addressReady) {
      handoffModel.pending = true;
      render();
    }
  }, 15000);

  function checkHTTPSCertificate() {
    const readinessConnection = new WebSocket(probeAddress.href);
    const probeDeadline = window.setTimeout(function expireProbe() { readinessConnection.close(); }, 10000);

    function retryWhenCertificateIsReady() {
      window.clearTimeout(probeDeadline);
      if (handoffModel.addressReady || handoffModel.retryTimer) return;
      handoffModel.retryTimer = window.setTimeout(function retryProbe() {
        handoffModel.retryTimer = 0;
        checkHTTPSCertificate();
      }, 3000);
    }

    readinessConnection.addEventListener('message', function handoffWhenReady(event) {
      let readinessReply;
      try { readinessReply = JSON.parse(event.data); }
      catch (replyError) { readinessConnection.close(); retryWhenCertificateIsReady(); return; }
      if (readinessReply.ready === true) {
        handoffModel.addressReady = true;
        window.clearTimeout(probeDeadline);
        render();
        handoffForm.submit();
      }
      readinessConnection.close();
    });
    readinessConnection.addEventListener('error', retryWhenCertificateIsReady);
    readinessConnection.addEventListener('close', retryWhenCertificateIsReady);
  }

  render();
  checkHTTPSCertificate();
})();
