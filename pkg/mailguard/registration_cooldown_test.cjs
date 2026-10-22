'use strict';
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const source = fs.readFileSync(path.join(__dirname, '../../web/static/registration-cooldown.js'), 'utf8');

function openCountdown(remainingSeconds) {
  let monotonicTime = 0;
  let nextRender;
  const listeners = {};
  const countdown = {textContent: ''};
  const retry = {disabled: true};
  const confirmation = {disabled: false};
  const panel = {
    dataset: {retryUnix: String(1800000000 + remainingSeconds), serverUnix: '1800000000', countdownLabel: 'Try again in', readyLabel: 'Try again'},
    querySelector: function selectCountdown() { return countdown; },
    querySelectorAll: function selectRetryButtons() { return [retry]; }
  };
  vm.runInNewContext(source, {
    document: {
      querySelector: function selectPanel() { return panel; },
      addEventListener: function subscribe(eventName, callback) { listeners[eventName] = callback; }
    },
    performance: {now: function readMonotonicTime() { return monotonicTime; }},
    window: {setTimeout: function schedule(callback) { nextRender = callback; }}
  });
  return {
    countdown, retry, confirmation,
    advance: function advanceClock(seconds) { monotonicTime += seconds * 1000; nextRender(); },
    failDelivery: function reportFailure() { listeners['registration-delivery-failed'](); }
  };
}

for (const [seconds, expected] of [[3522, '58:42'], [20220, '5h 37m'], [223200, '2d 14h']]) {
  const page = openCountdown(seconds);
  assert.equal(page.countdown.textContent, 'Try again in ' + expected);
  assert.equal(page.retry.disabled, true);
  assert.equal(page.confirmation.disabled, false);
  page.advance(seconds - 1);
  assert.equal(page.retry.disabled, true);
  page.advance(1);
  assert.equal(page.retry.disabled, false);
  assert.equal(page.countdown.textContent, 'Try again');
}
const failedPage = openCountdown(3600);
failedPage.failDelivery();
assert.equal(failedPage.retry.disabled, false);
assert.equal(failedPage.countdown.textContent, 'Try again');
