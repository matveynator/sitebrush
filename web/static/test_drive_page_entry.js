(function keepPrivateTestDriveOnRenderedSite() {
  'use strict';
  const pageQuery = new URLSearchParams(window.location.search);
  if (!pageQuery.has('edit') && !pageQuery.has('visual')) return;
  const preservedHash = window.location.hash === '#test-drive-copy' || window.location.hash === '#activation'
    ? window.location.hash
    : '';
  window.location.replace(window.location.pathname + preservedHash);
})();
