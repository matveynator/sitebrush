(function initializeTestDriveSessionChannel() {
  'use strict';

  window.SiteBrushTestDriveSessionCommand = function sendTestDriveSessionCommand(action, fields) {
    return new Promise(function exchangeTestDriveCommand(resolve, reject) {
      const socketURL = new URL('/?test_drive_ws', window.location.href);
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
          finish(error);
          return;
        }
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
