(function waitForSiteBrushTrialImport() {
  const modelElement = document.querySelector('[data-sitebrush-import-wait]');
  if (!modelElement) return;
  const returnPath = modelElement.getAttribute('data-return-path') || '/';
  const errorElement = modelElement.querySelector('[data-import-error]');

  function checkImportStatus() {
    fetch('/?trial_import_status', {cache: 'no-store', headers: {Accept: 'application/json'}})
      .then(function readImportStatus(response) {
        if (!response.ok) throw new Error('Import status is temporarily unavailable.');
        return response.json();
      })
      .then(function updateImportStatus(status) {
        if (status.status === 'done') {
          window.location.replace(returnPath);
          return;
        }
        if (status.status === 'error') {
          errorElement.textContent = status.error || 'Import stopped.';
          errorElement.hidden = false;
          return;
        }
        window.setTimeout(checkImportStatus, 2000);
      })
      .catch(function retryImportStatus(error) {
        errorElement.textContent = error.message;
        errorElement.hidden = false;
        window.setTimeout(checkImportStatus, 5000);
      });
  }

  checkImportStatus();
})();
