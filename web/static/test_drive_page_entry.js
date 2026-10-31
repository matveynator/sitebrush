(function keepPrivateTestDriveOnRenderedSite() {
  'use strict';
  const pageQuery = new URLSearchParams(window.location.search);
  if (!pageQuery.has('edit') && !pageQuery.has('visual')) return;

  const preservedHash = window.location.hash === '#test-drive-copy' || window.location.hash === '#activation'
    ? window.location.hash
    : '';

  // A newly created Test Drive may not have a rendered / page yet. Keep the
  // first copy/save action on the server-rendered Test Drive shell instead of
  // redirecting to an empty site and hitting the normal 404 handler.
  if (preservedHash) {
    window.location.replace('/?test_drive_welcome' + preservedHash);
    return;
  }

  window.location.replace(window.location.pathname);
})();
