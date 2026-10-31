(function keepPrivateTestDriveOnRenderedSite() {
  'use strict';
  const pageQuery = new URLSearchParams(window.location.search);
  if (!pageQuery.has('edit') && !pageQuery.has('visual')) return;

  const preservedHash = window.location.hash === '#test-drive-copy' || window.location.hash === '#activation'
    ? window.location.hash
    : '';

  // A brand-new Test Drive already has its editor shell loaded, while its
  // rendered / page is not published yet. Turn that shell into the blank
  // rendered-site entry in-place: no second request, no 404 and no redirect
  // loop. The editor itself is hidden by admin_dialog.css; the Test Drive
  // action script can then open the requested modal over this blank page.
  if (preservedHash) {
    window.history.replaceState(null, '', window.location.pathname + preservedHash);
    return;
  }

  window.location.replace(window.location.pathname);
})();
