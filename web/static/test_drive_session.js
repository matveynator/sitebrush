(function initializeTestDriveSessionChannel() {
  'use strict';

  window.SiteBrushTestDriveSessionCommand = function sendTestDriveSessionCommand(action, fields, commandEndpoint) {
    return new Promise(function exchangeTestDriveCommand(resolve, reject) {
      const socketURL = new URL('/', window.location.href);
      socketURL.searchParams.set(commandEndpoint || 'test_drive_ws', '');
      socketURL.protocol = socketURL.protocol === 'https:' ? 'wss:' : 'ws:';
      const connection = new WebSocket(socketURL.href);
      let settled = false;

      function finish(error, payload) {
        if (settled) return;
        settled = true;
        connection.close();
        if (error) reject(error);
        else resolve(payload);
      }

      connection.addEventListener('open', function sendCommand() {
        connection.send(JSON.stringify(Object.assign({id: 1, action: action}, fields || {})));
      });
      connection.addEventListener('message', function receiveReply(event) {
        let reply;
        try {
          reply = JSON.parse(event.data);
        } catch (error) {
          finish(error);
          return;
        }
        if (reply.id !== 1 || typeof reply.status !== 'number') {
          finish(new Error());
          return;
        }
        if (reply.status >= 400) {
          const error = new Error(reply.error || '');
          error.status = reply.status;
          error.retryAfter = Number(reply.retry_after) || 0;
          error.captchaRequired = Boolean(reply.captcha_required);
          finish(error);
          return;
        }
        if (!reply.payload || typeof reply.payload !== 'object') {
          finish(new Error());
          return;
        }
        if (reply.captcha_required) reply.payload.captcha_required = true;
        finish(null, reply.payload);
      });
      connection.addEventListener('error', function reportSocketError() {
        finish(new Error());
      });
      connection.addEventListener('close', function reportClosedSocket() {
        finish(new Error());
      });
    });
  };
})();
