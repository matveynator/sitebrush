(function initializeDomainNameInput() {
  'use strict';

  function normalize(name) {
    return String(name || '').toLowerCase();
  }

  function valid(name) {
    return /^[a-z0-9](?:[a-z0-9-]{0,22}[a-z0-9])?$/.test(name);
  }

  function address(name, parentDomain) {
    return name && parentDomain ? name + '.' + parentDomain : '';
  }

  window.SiteBrushDomainNameInput = Object.freeze({normalize: normalize, valid: valid, address: address});
})();
