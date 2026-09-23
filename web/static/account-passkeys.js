(function prepareSiteBrushPasskeys() {
  'use strict';

  function passkeyOriginSupported() {
    var hostname = String(window.location.hostname || '').trim().toLowerCase();
    if (!hostname) return false;

    // WebAuthn RP IDs must be DNS names (localhost is explicitly supported by
    // browsers for local development). A literal IPv4/IPv6 address cannot be
    // used as an RP ID, even when SiteBrush serves it over HTTPS.
    var unbracketedHostname = hostname.replace(/^\[|\]$/g, '');
    var isIPv4 = /^(?:\d{1,3}\.){3}\d{1,3}$/.test(unbracketedHostname);
    var isIPv6 = unbracketedHostname.indexOf(':') !== -1;
    return !isIPv4 && !isIPv6;
  }

  function passkeysSupported() {
    return passkeyOriginSupported() &&
      window.isSecureContext &&
      typeof window.PublicKeyCredential === 'function' &&
      navigator.credentials &&
      typeof navigator.credentials.create === 'function' &&
      typeof navigator.credentials.get === 'function';
  }

  function decodeBase64URL(encodedValue) {
    var normalizedValue = String(encodedValue || '').replace(/-/g, '+').replace(/_/g, '/');
    while (normalizedValue.length % 4) normalizedValue += '=';
    var binaryValue = window.atob(normalizedValue);
    var bytes = new Uint8Array(binaryValue.length);
    for (var byteIndex = 0; byteIndex < binaryValue.length; byteIndex += 1) {
      bytes[byteIndex] = binaryValue.charCodeAt(byteIndex);
    }
    return bytes.buffer;
  }

  function encodeBase64URL(binaryValue) {
    if (binaryValue === null || typeof binaryValue === 'undefined') return '';
    var bytes = new Uint8Array(binaryValue);
    var binaryText = '';
    for (var byteIndex = 0; byteIndex < bytes.length; byteIndex += 1) {
      binaryText += String.fromCharCode(bytes[byteIndex]);
    }
    return window.btoa(binaryText).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/g, '');
  }

  function prepareCreationOptions(options) {
    var publicKey = options.publicKey;
    publicKey.challenge = decodeBase64URL(publicKey.challenge);
    publicKey.user.id = decodeBase64URL(publicKey.user.id);
    (publicKey.excludeCredentials || []).forEach(function decodeExcludedCredential(credential) {
      credential.id = decodeBase64URL(credential.id);
    });
    return publicKey;
  }

  function prepareRequestOptions(options) {
    var publicKey = options.publicKey;
    publicKey.challenge = decodeBase64URL(publicKey.challenge);
    (publicKey.allowCredentials || []).forEach(function decodeAllowedCredential(credential) {
      credential.id = decodeBase64URL(credential.id);
    });
    return publicKey;
  }

  function serializeCredential(credential) {
    var response = credential.response;
    var serializedResponse = {
      clientDataJSON: encodeBase64URL(response.clientDataJSON)
    };
    if (response.attestationObject) {
      serializedResponse.attestationObject = encodeBase64URL(response.attestationObject);
      if (typeof response.getTransports === 'function') {
        serializedResponse.transports = response.getTransports();
      }
    }
    if (response.authenticatorData) serializedResponse.authenticatorData = encodeBase64URL(response.authenticatorData);
    if (response.signature) serializedResponse.signature = encodeBase64URL(response.signature);
    if (response.userHandle) serializedResponse.userHandle = encodeBase64URL(response.userHandle);
    return {
      id: credential.id,
      rawId: encodeBase64URL(credential.rawId),
      type: credential.type,
      authenticatorAttachment: credential.authenticatorAttachment || null,
      clientExtensionResults: credential.getClientExtensionResults(),
      response: serializedResponse
    };
  }

  async function readJSON(response) {
    var payload = await response.json();
    if (!response.ok) {
      throw new Error(payload.error || 'passkey request failed');
    }
    return payload;
  }

  async function beginPasskey(startURL, requestOptions) {
    var response = await fetch(startURL, requestOptions || {credentials: 'same-origin'});
    return readJSON(response);
  }

  async function finishPasskey(finishURL, challengeToken, credential, requestOptions) {
    var separator = finishURL.indexOf('?') === -1 ? '?' : '&';
    var response = await fetch(finishURL + separator + 'passkey_token=' + encodeURIComponent(challengeToken), {
      method: 'POST',
      credentials: 'same-origin',
      headers: Object.assign({'Content-Type': 'application/json'}, requestOptions && requestOptions.headers ? requestOptions.headers : {}),
      body: JSON.stringify(serializeCredential(credential))
    });
    return readJSON(response);
  }

  function showPasskeyStatus(button, message) {
    var selector = button.getAttribute('data-passkey-status');
    var statusElement = selector ? document.querySelector(selector) : null;
    if (statusElement) statusElement.textContent = message;
  }

  var loginButton = document.querySelector('[data-passkey-login]');
  if (loginButton) {
    if (!passkeysSupported()) {
      loginButton.hidden = true;
    } else {
      loginButton.addEventListener('click', async function startPasskeyLogin() {
        loginButton.disabled = true;
        try {
          var startURL = loginButton.getAttribute('data-passkey-start');
          var finishURL = loginButton.getAttribute('data-passkey-finish');
          var beginResult = await beginPasskey(startURL, {credentials: 'same-origin'});
          var credential = await navigator.credentials.get({publicKey: prepareRequestOptions(beginResult.options)});
          var finishResult = await finishPasskey(finishURL, beginResult.token, credential);
          window.location.assign(finishResult.redirect || '/');
        } catch (passkeyError) {
          var loginMessage = loginButton.getAttribute('data-passkey-error') || String(passkeyError);
          if (passkeyError && passkeyError.name === 'SecurityError') {
            loginMessage = loginButton.getAttribute('data-passkey-unavailable') || loginMessage;
          }
          showPasskeyStatus(loginButton, loginMessage);
          loginButton.disabled = false;
        }
      });
    }
  }

  var createButton = document.querySelector('[data-passkey-create]');
  if (createButton) {
    if (!passkeysSupported()) {
      createButton.disabled = true;
      showPasskeyStatus(createButton, createButton.getAttribute('data-passkey-unavailable') || '');
    } else {
      createButton.addEventListener('click', async function startPasskeyRegistration() {
        createButton.disabled = true;
        try {
          var csrfToken = createButton.getAttribute('data-account-csrf') || '';
          var headers = {'X-SiteBrush-CSRF': csrfToken};
          var startURL = createButton.getAttribute('data-passkey-start');
          var finishURL = createButton.getAttribute('data-passkey-finish');
          var beginResult = await beginPasskey(startURL, {credentials: 'same-origin', headers: headers});
          var credential = await navigator.credentials.create({publicKey: prepareCreationOptions(beginResult.options)});
          await finishPasskey(finishURL, beginResult.token, credential, {headers: headers});
          var successRedirect = createButton.getAttribute('data-passkey-success-redirect') || '';
          if (successRedirect) {
            window.location.assign(successRedirect);
            return;
          }
          window.location.reload();
        } catch (passkeyError) {
          var createMessage = createButton.getAttribute('data-passkey-error') || String(passkeyError);
          if (passkeyError && passkeyError.name === 'SecurityError') {
            createMessage = createButton.getAttribute('data-passkey-unavailable') || createMessage;
          }
          showPasskeyStatus(createButton, createMessage);
          createButton.disabled = false;
        }
      });
    }
  }
})();