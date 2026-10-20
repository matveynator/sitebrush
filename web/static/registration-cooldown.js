(function prepareRegistrationCooldown() {
  'use strict';
  const panel = document.querySelector('[data-registration-cooldown]');
  if (!panel) return;
  const countdown = panel.querySelector('[data-registration-countdown]');
  const retryButtons = panel.querySelectorAll('[data-registration-retry]');
  const model = {
    remainingSeconds: Math.max(0, Number(panel.dataset.retryUnix) - Number(panel.dataset.serverUnix)),
    startedAt: performance.now(),
    deliveryFailed: false
  };

  function formatRemaining(seconds) {
    if (seconds >= 86400) return Math.floor(seconds / 86400) + 'd ' + Math.floor(seconds % 86400 / 3600) + 'h';
    if (seconds >= 3600) return Math.floor(seconds / 3600) + 'h ' + Math.floor(seconds % 3600 / 60) + 'm';
    return String(Math.floor(seconds / 60)).padStart(2, '0') + ':' + String(seconds % 60).padStart(2, '0');
  }

  function render() {
    const elapsedSeconds = Math.floor((performance.now() - model.startedAt) / 1000);
    const seconds = model.deliveryFailed ? 0 : Math.max(0, model.remainingSeconds - elapsedSeconds);
    countdown.textContent = seconds > 0 ? panel.dataset.countdownLabel + ' ' + formatRemaining(seconds) : panel.dataset.readyLabel;
    retryButtons.forEach(function reflectAvailability(button) { button.disabled = seconds > 0; });
    if (seconds > 0) window.setTimeout(render, 1000);
  }

  document.addEventListener('registration-delivery-failed', function releaseFailedDelivery() {
    model.deliveryFailed = true;
    render();
  });
  render();
})();
