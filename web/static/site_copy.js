(function initializeSiteBrushCopySiteModule() {
  if (window.SiteBrushCopySite) {
    return;
  }

  function ensureCopySiteStyles() {
    if (document.getElementById('SiteBrushCopySiteStyles')) {
      return;
    }
    const styleElement = document.createElement('style');
    styleElement.id = 'SiteBrushCopySiteStyles';
    styleElement.textContent = [
      '.SiteBrushCopySiteOverlay{position:fixed;inset:0;z-index:2147483647;display:flex;align-items:center;justify-content:center;background:rgba(15,23,42,.58);font-family:Arial,Helvetica,sans-serif;color:#1f2937}',
      '.SiteBrushCopySiteDialog{width:min(760px,calc(100vw - 28px));max-height:calc(100vh - 28px);overflow:auto;background:#fff;border-radius:12px;box-shadow:0 24px 80px rgba(15,23,42,.32);padding:18px}',
      '.SiteBrushCopySiteHeader{display:flex;align-items:center;justify-content:space-between;gap:12px;margin-bottom:14px}',
      '.SiteBrushCopySiteTitle{display:flex;align-items:center;gap:8px;margin:0;font-size:20px;line-height:1.2}',
      '.SiteBrushCopySiteTitle img{width:24px;height:24px}',
      '.SiteBrushCopySiteClose{border:0;background:transparent;color:#64748b;font-size:26px;line-height:1;cursor:pointer;padding:2px 6px}',
      '.SiteBrushCopySiteForm{display:grid;gap:12px}',
      '.SiteBrushCopySitePrimaryRow{display:grid;grid-template-columns:minmax(0,1fr) auto;gap:8px}',
      '.SiteBrushCopySiteSecondaryGrid{display:grid;grid-template-columns:1fr 1fr;gap:10px}',
      '.SiteBrushCopySiteField{display:grid;gap:5px;font-size:13px;font-weight:700}',
      '.SiteBrushCopySiteInput,.SiteBrushCopySiteSelect{width:100%;box-sizing:border-box;border:1px solid #cbd5e1;border-radius:8px;background:#fff;color:#0f172a;font:inherit;font-weight:400;padding:9px 10px}',
      '.SiteBrushCopySiteCheckbox{display:flex;align-items:center;gap:8px;font-size:14px;font-weight:700}',
      '.SiteBrushCopySiteButton{display:inline-flex;align-items:center;justify-content:center;gap:7px;border:0;border-radius:8px;background:#198754;color:#fff;font:inherit;font-weight:700;padding:9px 12px;cursor:pointer;white-space:nowrap}',
      '.SiteBrushCopySiteButton img{width:18px;height:18px}',
      '.SiteBrushCopySiteButton:disabled{opacity:.58;cursor:not-allowed}',
      '.SiteBrushCopySiteProgress{height:22px;border-radius:999px;background:#e5e7eb;overflow:hidden;margin-top:12px}',
      '.SiteBrushCopySiteProgressBar{height:100%;width:0%;background:#2563eb;color:#fff;text-align:center;font-size:12px;line-height:22px;transition:width .18s ease}',
      '.SiteBrushCopySiteStatus{margin:12px 0 0;color:#111827;font-size:14px;line-height:1.4;overflow-wrap:anywhere}',
      '.SiteBrushCopySiteURL{margin-top:5px;color:#334155;font-size:12px;overflow-wrap:anywhere}',
      '.SiteBrushCopySiteQuota{display:grid;gap:4px;margin-top:12px;border:1px solid #e2e8f0;border-radius:8px;padding:10px;font-size:13px}',
      '.SiteBrushCopySiteQuotaLine{display:flex;justify-content:space-between;gap:12px}',
      '.SiteBrushCopySiteQuotaStatus{font-weight:700}.SiteBrushCopySiteQuotaStatus.is-ok{color:#15803d}.SiteBrushCopySiteQuotaStatus.is-error{color:#b91c1c}',
      '.SiteBrushCopySiteResources{display:grid;gap:0;margin-top:12px;border:1px solid #e2e8f0;border-radius:8px;max-height:270px;overflow:auto}',
      '.SiteBrushCopySiteResource{display:grid;grid-template-columns:auto auto minmax(0,1fr);gap:8px;align-items:start;padding:9px;border-bottom:1px solid #e2e8f0;font-size:13px}',
      '.SiteBrushCopySiteResource:last-child{border-bottom:0}',
      '.SiteBrushCopySiteResourceKind{border-radius:999px;background:#64748b;color:#fff;padding:2px 7px;font-size:11px}',
      '.SiteBrushCopySiteResourceURL{overflow-wrap:anywhere}',
      '.SiteBrushCopySiteResourceMeta{color:#334155;margin-top:3px}',
      '.SiteBrushCopySiteResourceReason{color:#b91c1c;margin-top:3px}',
      '.SiteBrushCopySiteActions{display:flex;justify-content:flex-end;gap:8px;margin-top:14px}',
      '.SiteBrushCopySiteSecondaryButton{border:1px solid #cbd5e1;border-radius:8px;background:#fff;color:#0f172a;font:inherit;font-weight:700;padding:8px 12px;cursor:pointer}',
      '.SiteBrushCopySiteHidden{display:none!important}',
      '@media (max-width:640px){.SiteBrushCopySiteDialog{padding:14px}.SiteBrushCopySitePrimaryRow,.SiteBrushCopySiteSecondaryGrid{grid-template-columns:1fr}.SiteBrushCopySiteActions{flex-direction:column}.SiteBrushCopySiteButton,.SiteBrushCopySiteSecondaryButton{width:100%}}',
      '@media (prefers-color-scheme:dark){.SiteBrushCopySiteDialog{background:#111827;color:#e5e7eb}.SiteBrushCopySiteInput,.SiteBrushCopySiteSelect,.SiteBrushCopySiteSecondaryButton{background:#0f172a;color:#e5e7eb;border-color:#374151}.SiteBrushCopySiteClose,.SiteBrushCopySiteStatus,.SiteBrushCopySiteURL,.SiteBrushCopySiteResourceMeta{color:#e5e7eb}.SiteBrushCopySiteProgress{background:#374151}.SiteBrushCopySiteQuota,.SiteBrushCopySiteResources,.SiteBrushCopySiteResource{border-color:#374151}}'
    ].join('');
    document.head.appendChild(styleElement);
  }

  function textFromConfig(configuration, textName, fallbackText) {
    if (!configuration || !configuration.texts) {
      return fallbackText;
    }
    const configuredText = String(configuration.texts[textName] || '').trim();
    return configuredText === '' ? fallbackText : configuredText;
  }

  function formatSize(sizeBytes, configuration) {
    const normalizedSize = Number(sizeBytes);
    if (!Number.isFinite(normalizedSize) || normalizedSize < 0) {
      return textFromConfig(configuration, 'sizeUnknown', 'unknown');
    }
    if (normalizedSize < 1024) {
      return normalizedSize + ' B';
    }
    if (normalizedSize < 1024 * 1024) {
      return (normalizedSize / 1024).toFixed(1) + ' KB';
    }
    return (normalizedSize / 1024 / 1024).toFixed(1) + ' MB';
  }

  function formatSignedSize(sizeBytes, configuration) {
    const normalizedSize = Number(sizeBytes) || 0;
    if (normalizedSize < 0) {
      return '-' + formatSize(Math.abs(normalizedSize), configuration);
    }
    return formatSize(normalizedSize, configuration);
  }

  function randomProgressToken() {
    return Date.now().toString(36) + '-' + Math.random().toString(36).slice(2, 10);
  }

  function createElement(tagName, className, textContent) {
    const createdElement = document.createElement(tagName);
    if (className) {
      createdElement.className = className;
    }
    if (textContent !== undefined) {
      createdElement.textContent = textContent;
    }
    return createdElement;
  }

  function addOption(selectElement, optionValue, optionText) {
    const optionElement = document.createElement('option');
    optionElement.value = optionValue;
    optionElement.textContent = optionText;
    selectElement.appendChild(optionElement);
  }

  function buildLanguageSelect(configuration) {
    const selectElement = createElement('select', 'SiteBrushCopySiteSelect');
    selectElement.name = 'source_language';
    addOption(selectElement, 'auto', textFromConfig(configuration, 'sourceLanguageAuto', 'Auto'));
    addOption(selectElement, 'ru', 'Русский');
    addOption(selectElement, 'en', 'English');
    addOption(selectElement, 'fr', 'Français');
    addOption(selectElement, 'de', 'Deutsch');
    addOption(selectElement, 'es', 'Español');
    addOption(selectElement, 'pt', 'Português');
    addOption(selectElement, 'it', 'Italiano');
    addOption(selectElement, 'sv', 'Svenska');
    addOption(selectElement, 'fi', 'Suomi');
    addOption(selectElement, 'tr', 'Türkçe');
    addOption(selectElement, 'zh', '中文');
    addOption(selectElement, 'ja', '日本語');
    addOption(selectElement, 'he', 'עברית');
    addOption(selectElement, 'fa', 'فارسی');
    addOption(selectElement, 'kk', 'Қазақша');
    addOption(selectElement, 'mn', 'Монгол');
    return selectElement;
  }

  function setProgress(progressBarElement, percentNumber) {
    const boundedPercent = Math.max(0, Math.min(100, Math.round(Number(percentNumber) || 0)));
    progressBarElement.style.width = boundedPercent + '%';
    progressBarElement.textContent = boundedPercent + '%';
  }

  function formRequestBody(formElement) {
    const requestBody = new URLSearchParams();
    for (const formEntry of new FormData(formElement).entries()) {
      requestBody.append(formEntry[0], formEntry[1]);
    }
    return requestBody;
  }

  function appendHiddenField(formElement, fieldName, fieldValue) {
    const hiddenFieldElement = document.createElement('input');
    hiddenFieldElement.type = 'hidden';
    hiddenFieldElement.name = fieldName;
    hiddenFieldElement.value = fieldValue;
    formElement.appendChild(hiddenFieldElement);
  }

  function clearSelectionFields(formElement) {
    const hiddenFieldElements = formElement.querySelectorAll('input[name="import_selection_confirmed"],input[name="import_resource_url"]');
    for (const hiddenFieldElement of hiddenFieldElements) {
      hiddenFieldElement.remove();
    }
  }

  function selectedResourceBytes(resourcesElement) {
    let byteCount = 0;
    const checkboxElements = resourcesElement.querySelectorAll('input[data-sitebrush-copy-resource-url]');
    for (const checkboxElement of checkboxElements) {
      if (checkboxElement.checked) {
        byteCount += Number(checkboxElement.dataset.sitebrushCopyResourceSizeBytes) || 0;
      }
    }
    return byteCount;
  }

  function renderQuotaSummary(quotaElement, resourcesElement, previewPayload, configuration, continueButtonElement) {
    quotaElement.replaceChildren();
    if (!previewPayload) {
      quotaElement.classList.add('SiteBrushCopySiteHidden');
      continueButtonElement.disabled = false;
      return;
    }
    const pageStorageBytes = Number(previewPayload.page_storage_bytes) || 0;
    const currentUsedBytes = Number(previewPayload.current_used_bytes) || 0;
    const limitBytes = Number(previewPayload.limit_bytes) || 0;
    const totalStorageBytes = pageStorageBytes + selectedResourceBytes(resourcesElement);
    const projectedUsedBytes = currentUsedBytes + totalStorageBytes;
    const remainingBytes = limitBytes - projectedUsedBytes;
    const fitsQuota = projectedUsedBytes <= limitBytes;
    const titleElement = createElement('div', '', textFromConfig(configuration, 'quotaSummaryTitle', 'Storage'));
    const currentLineElement = quotaLine(textFromConfig(configuration, 'quotaCurrentUsed', 'Current'), formatSignedSize(currentUsedBytes, configuration));
    const addLineElement = quotaLine(textFromConfig(configuration, 'quotaWillAdd', 'Will add'), formatSignedSize(totalStorageBytes, configuration));
    const afterText = formatSignedSize(projectedUsedBytes, configuration) + ' ' + textFromConfig(configuration, 'quotaAfterImportOf', 'of') + ' ' + formatSignedSize(limitBytes, configuration);
    const afterLineElement = quotaLine(textFromConfig(configuration, 'quotaAfterImport', 'After import'), afterText);
    const remainLineElement = quotaLine(textFromConfig(configuration, 'quotaWillRemain', 'Will remain'), formatSignedSize(Math.max(remainingBytes, 0), configuration));
    const statusElement = createElement('div', 'SiteBrushCopySiteQuotaStatus ' + (fitsQuota ? 'is-ok' : 'is-error'), fitsQuota ? textFromConfig(configuration, 'quotaFits', 'Enough space') : textFromConfig(configuration, 'quotaExceeded', 'Storage limit exceeded'));
    quotaElement.appendChild(titleElement);
    quotaElement.appendChild(currentLineElement);
    quotaElement.appendChild(addLineElement);
    quotaElement.appendChild(afterLineElement);
    quotaElement.appendChild(remainLineElement);
    quotaElement.appendChild(statusElement);
    quotaElement.classList.remove('SiteBrushCopySiteHidden');
    continueButtonElement.disabled = !fitsQuota;
  }

  function quotaLine(labelText, amountText) {
    const lineElement = createElement('div', 'SiteBrushCopySiteQuotaLine');
    lineElement.appendChild(createElement('span', '', labelText));
    lineElement.appendChild(createElement('span', '', amountText));
    return lineElement;
  }

  function appendPreviewResource(resourcesElement, previewResource, configuration, onSelectionChange) {
    const resourceRowElement = createElement('label', 'SiteBrushCopySiteResource');
    const checkboxElement = document.createElement('input');
    checkboxElement.type = 'checkbox';
    checkboxElement.checked = true;
    checkboxElement.dataset.sitebrushCopyResourceUrl = previewResource.url || '';
    checkboxElement.dataset.sitebrushCopyResourceSizeBytes = String(Number(previewResource.size_bytes) || 0);
    checkboxElement.addEventListener('change', onSelectionChange);
    const kindElement = createElement('span', 'SiteBrushCopySiteResourceKind', previewResource.kind || 'file');
    const detailsElement = createElement('div', '');
    detailsElement.appendChild(createElement('div', 'SiteBrushCopySiteResourceURL', previewResource.url || ''));
    detailsElement.appendChild(createElement('div', 'SiteBrushCopySiteResourceMeta', formatSize(previewResource.size_bytes, configuration)));
    resourceRowElement.appendChild(checkboxElement);
    resourceRowElement.appendChild(kindElement);
    resourceRowElement.appendChild(detailsElement);
    resourcesElement.appendChild(resourceRowElement);
  }

  function syncSelectedResources(formElement, resourcesElement) {
    clearSelectionFields(formElement);
    appendHiddenField(formElement, 'import_selection_confirmed', '1');
    const checkboxElements = resourcesElement.querySelectorAll('input[data-sitebrush-copy-resource-url]');
    for (const checkboxElement of checkboxElements) {
      if (checkboxElement.checked) {
        appendHiddenField(formElement, 'import_resource_url', checkboxElement.dataset.sitebrushCopyResourceUrl || '');
      }
    }
  }

  function syncFailedResources(formElement, failedResourceURLs) {
    clearSelectionFields(formElement);
    appendHiddenField(formElement, 'import_selection_confirmed', '1');
    const sortedFailedResourceURLs = Array.from(failedResourceURLs).sort();
    for (const failedResourceURL of sortedFailedResourceURLs) {
      appendHiddenField(formElement, 'import_resource_url', failedResourceURL);
    }
  }

  function openCopySiteModal(configuration) {
    ensureCopySiteStyles();
    const targetPath = String(configuration && configuration.path ? configuration.path : window.location.pathname || '/');
    const overlayElement = createElement('div', 'SiteBrushCopySiteOverlay');
    const dialogElement = createElement('div', 'SiteBrushCopySiteDialog');
    const headerElement = createElement('div', 'SiteBrushCopySiteHeader');
    const titleElement = createElement('h2', 'SiteBrushCopySiteTitle');
    const titleIconElement = document.createElement('img');
    titleIconElement.src = '/p/static/copy.png';
    titleIconElement.alt = '';
    titleElement.appendChild(titleIconElement);
    titleElement.appendChild(document.createTextNode(textFromConfig(configuration, 'title', 'Copy site')));
    const closeButtonElement = createElement('button', 'SiteBrushCopySiteClose', '×');
    closeButtonElement.type = 'button';
    headerElement.appendChild(titleElement);
    headerElement.appendChild(closeButtonElement);

    const formElement = createElement('form', 'SiteBrushCopySiteForm');
    appendHiddenField(formElement, 'path', targetPath);
    const tokenFieldElement = document.createElement('input');
    tokenFieldElement.type = 'hidden';
    tokenFieldElement.name = 'progress_token';
    formElement.appendChild(tokenFieldElement);

    const primaryRowElement = createElement('div', 'SiteBrushCopySitePrimaryRow');
    const sourceUrlElement = createElement('input', 'SiteBrushCopySiteInput');
    sourceUrlElement.name = 'source_url';
    sourceUrlElement.type = 'text';
    sourceUrlElement.inputMode = 'url';
    sourceUrlElement.required = true;
    sourceUrlElement.placeholder = textFromConfig(configuration, 'sourceURLPlaceholder', 'https://example.com/');
    const submitButtonElement = createElement('button', 'SiteBrushCopySiteButton');
    submitButtonElement.type = 'submit';
    const submitIconElement = document.createElement('img');
    submitIconElement.src = '/p/static/copy.png';
    submitIconElement.alt = '';
    submitButtonElement.appendChild(submitIconElement);
    submitButtonElement.appendChild(document.createTextNode(textFromConfig(configuration, 'copyButton', 'Copy')));
    primaryRowElement.appendChild(sourceUrlElement);
    primaryRowElement.appendChild(submitButtonElement);

    const secondaryGridElement = createElement('div', 'SiteBrushCopySiteSecondaryGrid');
    const sourceIPFieldElement = createElement('label', 'SiteBrushCopySiteField');
    sourceIPFieldElement.appendChild(createElement('span', '', textFromConfig(configuration, 'sourceIPLabel', 'Source IP')));
    const sourceIPElement = createElement('input', 'SiteBrushCopySiteInput');
    sourceIPElement.name = 'source_ip';
    sourceIPElement.type = 'text';
    sourceIPElement.placeholder = textFromConfig(configuration, 'sourceIPPlaceholder', '203.0.113.10');
    sourceIPFieldElement.appendChild(sourceIPElement);
    const languageFieldElement = createElement('label', 'SiteBrushCopySiteField');
    languageFieldElement.appendChild(createElement('span', '', textFromConfig(configuration, 'sourceLanguageLabel', 'Preferred language')));
    languageFieldElement.appendChild(buildLanguageSelect(configuration));
    secondaryGridElement.appendChild(sourceIPFieldElement);
    secondaryGridElement.appendChild(languageFieldElement);

    const wholeSiteLabelElement = createElement('label', 'SiteBrushCopySiteCheckbox');
    const wholeSiteElement = document.createElement('input');
    wholeSiteElement.type = 'checkbox';
    wholeSiteElement.name = 'copy_whole_site';
    wholeSiteElement.value = '1';
    wholeSiteLabelElement.appendChild(wholeSiteElement);
    wholeSiteLabelElement.appendChild(createElement('span', '', textFromConfig(configuration, 'copyWholeSite', 'Copy entire website')));

    const statusElement = createElement('p', 'SiteBrushCopySiteStatus', '');
    const urlElement = createElement('div', 'SiteBrushCopySiteURL', '');
    const progressElement = createElement('div', 'SiteBrushCopySiteProgress SiteBrushCopySiteHidden');
    const progressBarElement = createElement('div', 'SiteBrushCopySiteProgressBar', '0%');
    progressElement.appendChild(progressBarElement);
    const quotaElement = createElement('div', 'SiteBrushCopySiteQuota SiteBrushCopySiteHidden');
    const resourcesElement = createElement('div', 'SiteBrushCopySiteResources SiteBrushCopySiteHidden');

    const actionRowElement = createElement('div', 'SiteBrushCopySiteActions');
    const cancelButtonElement = createElement('button', 'SiteBrushCopySiteSecondaryButton', textFromConfig(configuration, 'cancelButton', 'Cancel'));
    cancelButtonElement.type = 'button';
    const continueButtonElement = createElement('button', 'SiteBrushCopySiteButton SiteBrushCopySiteHidden', textFromConfig(configuration, 'continueButton', 'Continue'));
    continueButtonElement.type = 'button';
    actionRowElement.appendChild(cancelButtonElement);
    actionRowElement.appendChild(continueButtonElement);

    formElement.appendChild(primaryRowElement);
    formElement.appendChild(secondaryGridElement);
    formElement.appendChild(wholeSiteLabelElement);
    dialogElement.appendChild(headerElement);
    dialogElement.appendChild(formElement);
    dialogElement.appendChild(statusElement);
    dialogElement.appendChild(urlElement);
    dialogElement.appendChild(progressElement);
    dialogElement.appendChild(quotaElement);
    dialogElement.appendChild(resourcesElement);
    dialogElement.appendChild(actionRowElement);
    overlayElement.appendChild(dialogElement);
    document.body.appendChild(overlayElement);

    let progressStream = null;
    let previewPayload = null;
    let downloadFinishedWithErrors = false;
    let failedResourceURLs = new Set();
    let failedResourceReasons = new Map();
    let importedRedirectPath = '';
    let retryWasAttempted = false;
    let partialImportCanRetry = false;
    let requestIsRunning = false;
    let streamClosedIntentionally = false;
    let retryCountdownTimer = 0;
    let activeDownloadEndpoint = '?grab';

    function closeModal() {
      closeProgressStream();
      stopRetryCountdown();
      overlayElement.remove();
    }

    function closeProgressStream() {
      if (!progressStream) {
        return;
      }
      streamClosedIntentionally = true;
      progressStream.close();
      progressStream = null;
    }

    function finishPartialImport() {
      closeProgressStream();
      stopRetryCountdown();
      if (importedRedirectPath) {
        window.location.href = importedRedirectPath;
        return;
      }
      overlayElement.remove();
    }

    function collectFailedURLs(progressPayload) {
      if (!progressPayload) {
        return;
      }
      const failedReasons = progressPayload.failed_reasons && typeof progressPayload.failed_reasons === 'object' ? progressPayload.failed_reasons : {};
      for (const failedReasonURL of Object.keys(failedReasons)) {
        const normalizedFailedReasonURL = String(failedReasonURL || '').trim();
        const normalizedFailedReason = String(failedReasons[failedReasonURL] || '').trim();
        if (normalizedFailedReasonURL !== '') {
          failedResourceURLs.add(normalizedFailedReasonURL);
          if (normalizedFailedReason !== '') {
            failedResourceReasons.set(normalizedFailedReasonURL, normalizedFailedReason);
          }
        }
      }
      const failedURLs = Array.isArray(progressPayload.failed_urls) ? progressPayload.failed_urls : [];
      for (const failedURL of failedURLs) {
        const normalizedFailedURL = String(failedURL || '').trim();
        if (normalizedFailedURL !== '') {
          failedResourceURLs.add(normalizedFailedURL);
        }
      }
    }

    function stopRetryCountdown() {
      if (!retryCountdownTimer) {
        return;
      }
      window.clearInterval(retryCountdownTimer);
      retryCountdownTimer = 0;
    }

    function renderFailedResources() {
      resourcesElement.replaceChildren();
      const sortedFailedResourceURLs = Array.from(failedResourceURLs).sort();
      cancelButtonElement.textContent = textFromConfig(configuration, 'finishImport', 'Finish import');
      if (sortedFailedResourceURLs.length === 0) {
        resourcesElement.classList.add('SiteBrushCopySiteHidden');
        continueButtonElement.classList.add('SiteBrushCopySiteHidden');
        partialImportCanRetry = false;
        return;
      }
      resourcesElement.classList.remove('SiteBrushCopySiteHidden');
      const titleElement = createElement('div', 'SiteBrushCopySiteResource');
      titleElement.appendChild(createElement('span', 'SiteBrushCopySiteResourceKind', String(sortedFailedResourceURLs.length)));
      titleElement.appendChild(createElement('span', '', ''));
      titleElement.appendChild(createElement('div', 'SiteBrushCopySiteResourceURL', textFromConfig(configuration, 'failedResourcesTitle', 'Failed resources:')));
      resourcesElement.appendChild(titleElement);
      for (const failedResourceURL of sortedFailedResourceURLs) {
        const resourceRowElement = createElement('div', 'SiteBrushCopySiteResource');
        const failedReason = String(failedResourceReasons.get(failedResourceURL) || '').trim();
        const failedBadgeText = textFromConfig(configuration, 'failedResourceBadge', 'failed');
        const detailsElement = createElement('div', '');
        detailsElement.appendChild(createElement('div', 'SiteBrushCopySiteResourceURL', failedResourceURL));
        if (failedReason !== '') {
          detailsElement.appendChild(createElement('div', 'SiteBrushCopySiteResourceReason', failedReason));
        }
        resourceRowElement.dataset.sitebrushFailedResourceUrl = failedResourceURL;
        resourceRowElement.appendChild(createElement('span', 'SiteBrushCopySiteResourceKind', failedBadgeText));
        resourceRowElement.appendChild(createElement('span', '', ''));
        resourceRowElement.appendChild(detailsElement);
        resourcesElement.appendChild(resourceRowElement);
      }
      continueButtonElement.textContent = textFromConfig(configuration, 'retryRemaining', 'Retry remaining');
      continueButtonElement.classList.remove('SiteBrushCopySiteHidden');
      partialImportCanRetry = true;
    }

    function retryStatusText(progressPayload, secondsLeft) {
      const retryAttempt = Number(progressPayload.retry_attempt) || 0;
      const retryTotal = Number(progressPayload.retry_total) || 0;
      const failedTotal = Number(progressPayload.failed_total) || failedResourceURLs.size;
      let statusText = textFromConfig(configuration, 'retryRemaining', 'Retry remaining');
      if (retryAttempt > 0 && retryTotal > 0) {
        statusText += ' ' + retryAttempt + '/' + retryTotal;
      }
      if (secondsLeft > 0) {
        statusText += ': ' + secondsLeft + 's';
      }
      if (failedTotal > 0) {
        statusText += '. ' + textFromConfig(configuration, 'leftWord', 'Left') + ' ' + failedTotal + '.';
      }
      return statusText;
    }

    function startRetryCountdown(progressPayload) {
      stopRetryCountdown();
      let secondsLeft = Math.max(0, Number(progressPayload.retry_delay_seconds) || 0);
      statusElement.textContent = retryStatusText(progressPayload, secondsLeft);
      if (secondsLeft <= 0) {
        return;
      }
      retryCountdownTimer = window.setInterval(function renderRetryCountdown() {
        secondsLeft -= 1;
        statusElement.textContent = retryStatusText(progressPayload, Math.max(secondsLeft, 0));
        if (secondsLeft <= 0) {
          stopRetryCountdown();
        }
      }, 1000);
    }

    function responseIncludesFailureState(downloadPayload) {
      return downloadPayload && Object.prototype.hasOwnProperty.call(downloadPayload, 'failed_total');
    }

    function applyDownloadFailureState(downloadPayload) {
      if (!responseIncludesFailureState(downloadPayload)) {
        return false;
      }
      collectFailedURLs(downloadPayload);
      const failedTotal = Number(downloadPayload.failed_total) || 0;
      if (failedTotal <= 0) {
        failedResourceURLs = new Set();
        failedResourceReasons = new Map();
        stopRetryCountdown();
        return false;
      }
      downloadFinishedWithErrors = true;
      statusElement.textContent = textFromConfig(configuration, retryWasAttempted ? 'partialImportClose' : 'partialImportRetry', 'Imported with errors.');
      setProgress(progressBarElement, 100);
      closeProgressStream();
      renderFailedResources();
      return true;
    }

    function connectProgressStream(progressToken, readyCallback, forDownload) {
      let readyCallbackWasCalled = false;
      streamClosedIntentionally = false;
      progressStream = new EventSource(targetPath + '?grab_events&token=' + encodeURIComponent(progressToken));
      progressStream.onmessage = function onProgressMessage(messageEvent) {
        let progressPayload = null;
        try {
          progressPayload = JSON.parse(messageEvent.data);
        } catch (parseError) {
          statusElement.textContent = textFromConfig(configuration, 'invalidStatusError', 'Invalid status.');
          closeProgressStream();
          return;
        }
        if (progressPayload.stage === 'ready') {
          if (!readyCallbackWasCalled) {
            readyCallbackWasCalled = true;
            readyCallback();
          }
          return;
        }
        if (progressPayload.stage !== 'retry_wait') {
          stopRetryCountdown();
        }
        const completedPercent = Math.max(0, Math.min(100, Number(progressPayload.completed_percent) || 0));
        setProgress(progressBarElement, progressPayload.stage === 'done' ? 100 : completedPercent);
        collectFailedURLs(progressPayload);
        if (progressPayload.stage === 'retry_wait') {
          renderFailedResources();
          continueButtonElement.classList.add('SiteBrushCopySiteHidden');
          partialImportCanRetry = false;
          startRetryCountdown(progressPayload);
          urlElement.textContent = progressPayload.current_url || '';
          return;
        }
        if (progressPayload.stage === 'retrying') {
          statusElement.textContent = retryStatusText(progressPayload, 0);
          urlElement.textContent = progressPayload.current_url || '';
          return;
        }
        if (progressPayload.stage === 'error' && progressPayload.current_url) {
          const failedCurrentURL = String(progressPayload.current_url);
          failedResourceURLs.add(failedCurrentURL);
          const currentError = String(progressPayload.current_error || '').trim();
          if (currentError !== '') {
            failedResourceReasons.set(failedCurrentURL, currentError);
          }
        }
        if (progressPayload.stage === 'downloaded' && progressPayload.current_url) {
          const downloadedURL = String(progressPayload.current_url);
          failedResourceURLs.delete(downloadedURL);
          failedResourceReasons.delete(downloadedURL);
        }
        const downloadedTotal = Number(progressPayload.downloaded_total) || 0;
        const foundTotal = Number(progressPayload.found_total) || 0;
        const remainingTotal = Math.max(foundTotal - downloadedTotal, 0);
        statusElement.textContent = textFromConfig(configuration, 'progressDownloadedPrefix', 'Downloaded') + ' ' + downloadedTotal + ' ' + textFromConfig(configuration, 'fromWord', 'of') + ' ' + foundTotal + '. ' + textFromConfig(configuration, 'leftWord', 'Left') + ' ' + remainingTotal + '.';
        urlElement.textContent = progressPayload.current_url || '';
        if (progressPayload.stage === 'done') {
          stopRetryCountdown();
          statusElement.textContent = forDownload ? textFromConfig(configuration, 'doneOpenPage', 'Done. Opening page...') : textFromConfig(configuration, 'previewResourcesText', 'Checking resources...');
          setProgress(progressBarElement, 100);
          closeProgressStream();
        }
        if (progressPayload.stage === 'partial') {
          stopRetryCountdown();
          downloadFinishedWithErrors = true;
          statusElement.textContent = progressPayload.message || textFromConfig(configuration, retryWasAttempted ? 'partialImportClose' : 'partialImportRetry', 'Imported with errors.');
          setProgress(progressBarElement, 100);
          renderFailedResources();
          closeProgressStream();
        }
        if (progressPayload.stage === 'error' && !progressPayload.current_url) {
          statusElement.textContent = textFromConfig(configuration, 'downloadFailedRetry', 'Download failed.');
          closeProgressStream();
        }
      };
      progressStream.onerror = function onProgressError() {
        if (streamClosedIntentionally || requestIsRunning) {
          return;
        }
        statusElement.textContent = textFromConfig(configuration, 'connectStatusFailed', 'Connection failed.');
      };
    }

    function showPreview(previewResponsePayload) {
      previewPayload = previewResponsePayload;
      resourcesElement.replaceChildren();
      const resourceCount = Number(previewPayload.resource_count) || 0;
      const pageCount = wholeSiteElement.checked ? Math.max(1, Number(previewPayload.page_count) || 1) : 0;
      const targetCount = pageCount + resourceCount;
      statusElement.textContent = textFromConfig(configuration, 'confirmDownloadTextPrefix', 'Ready to download') + ' ' + targetCount + '.';
      urlElement.textContent = previewPayload.source_url || sourceUrlElement.value;
      cancelButtonElement.textContent = textFromConfig(configuration, 'cancelButton', 'Cancel');
      partialImportCanRetry = false;
      continueButtonElement.classList.remove('SiteBrushCopySiteHidden');
      progressElement.classList.add('SiteBrushCopySiteHidden');
      if (resourceCount > 0) {
        resourcesElement.classList.remove('SiteBrushCopySiteHidden');
        for (const previewResource of previewPayload.resources || []) {
          appendPreviewResource(resourcesElement, previewResource, configuration, function onResourceSelectionChange() {
            renderQuotaSummary(quotaElement, resourcesElement, previewPayload, configuration, continueButtonElement);
          });
        }
      } else {
        resourcesElement.classList.add('SiteBrushCopySiteHidden');
      }
      renderQuotaSummary(quotaElement, resourcesElement, previewPayload, configuration, continueButtonElement);
    }

    function requestPreview() {
      if (!formElement.reportValidity()) {
        return;
      }
      const progressToken = randomProgressToken();
      tokenFieldElement.value = progressToken;
      previewPayload = null;
      failedResourceURLs = new Set();
      failedResourceReasons = new Map();
      importedRedirectPath = '';
      retryWasAttempted = false;
      partialImportCanRetry = false;
      resourcesElement.replaceChildren();
      resourcesElement.classList.add('SiteBrushCopySiteHidden');
      quotaElement.classList.add('SiteBrushCopySiteHidden');
      continueButtonElement.classList.add('SiteBrushCopySiteHidden');
      progressElement.classList.remove('SiteBrushCopySiteHidden');
      setProgress(progressBarElement, 0);
      statusElement.textContent = textFromConfig(configuration, 'previewResourcesText', 'Checking resources...');
      urlElement.textContent = sourceUrlElement.value;
      submitButtonElement.disabled = true;
      connectProgressStream(progressToken, function submitPreviewRequest() {
        requestIsRunning = true;
        fetch(targetPath + '?grab_preview', { method: 'POST', body: formRequestBody(formElement), headers: { Accept: 'application/json' } })
          .then(function parsePreviewResponse(previewResponse) {
            if (!previewResponse.ok) {
              return previewResponse.text().then(function throwPreviewError(errorText) {
                throw new Error(errorText || textFromConfig(configuration, 'checkResourcesFailed', 'Check failed.'));
              });
            }
            return previewResponse.json();
          })
          .then(function handlePreviewPayload(previewResponsePayload) {
            requestIsRunning = false;
            submitButtonElement.disabled = false;
            closeProgressStream();
            showPreview(previewResponsePayload);
          })
          .catch(function handlePreviewError(previewError) {
            requestIsRunning = false;
            submitButtonElement.disabled = false;
            closeProgressStream();
            statusElement.textContent = previewError.message || textFromConfig(configuration, 'previewErrorDefault', 'Preview failed.');
          });
      }, false);
    }

    function startDownload() {
      if (!previewPayload) {
        requestPreview();
        return;
      }
      const progressToken = randomProgressToken();
      tokenFieldElement.value = progressToken;
      downloadFinishedWithErrors = false;
      const retryOnlyFailedResources = partialImportCanRetry && failedResourceURLs.size > 0;
      if (retryOnlyFailedResources) {
        retryWasAttempted = true;
        partialImportCanRetry = false;
        activeDownloadEndpoint = '?grab_retry';
        syncFailedResources(formElement, failedResourceURLs);
      } else {
        retryWasAttempted = false;
        activeDownloadEndpoint = '?grab';
        failedResourceURLs = new Set();
        failedResourceReasons = new Map();
        partialImportCanRetry = false;
        syncSelectedResources(formElement, resourcesElement);
      }
      progressElement.classList.remove('SiteBrushCopySiteHidden');
      continueButtonElement.classList.add('SiteBrushCopySiteHidden');
      submitButtonElement.disabled = true;
      cancelButtonElement.disabled = true;
      setProgress(progressBarElement, 0);
      statusElement.textContent = textFromConfig(configuration, 'loadingStarted', 'Loading started...');
      urlElement.textContent = '';
      connectProgressStream(progressToken, function submitDownloadRequest() {
        requestIsRunning = true;
        fetch(targetPath + activeDownloadEndpoint, { method: 'POST', body: formRequestBody(formElement), headers: { Accept: 'application/json' } })
          .then(function parseDownloadResponse(downloadResponse) {
            if (!downloadResponse.ok) {
              return downloadResponse.text().then(function throwDownloadError(errorText) {
                throw new Error(errorText || textFromConfig(configuration, 'loadPageFailed', 'Load failed.'));
              });
            }
            return downloadResponse.json();
          })
          .then(function handleDownloadPayload(downloadPayload) {
            const redirectPath = downloadPayload.redirect || importedRedirectPath || targetPath;
            importedRedirectPath = redirectPath;
            requestIsRunning = false;
            if (applyDownloadFailureState(downloadPayload)) {
              submitButtonElement.disabled = false;
              cancelButtonElement.disabled = false;
              return;
            }
            if (downloadFinishedWithErrors) {
              submitButtonElement.disabled = false;
              cancelButtonElement.disabled = false;
              renderFailedResources();
              return;
            }
            setProgress(progressBarElement, 100);
            statusElement.textContent = textFromConfig(configuration, 'doneOpenPage', 'Done. Opening page...');
            closeProgressStream();
            window.location.href = redirectPath;
          })
          .catch(function handleDownloadError(downloadError) {
            requestIsRunning = false;
            submitButtonElement.disabled = false;
            cancelButtonElement.disabled = false;
            closeProgressStream();
            statusElement.textContent = downloadError.message || textFromConfig(configuration, 'loadPageFailed', 'Load failed.');
          });
      }, true);
    }

    closeButtonElement.addEventListener('click', closeModal);
    cancelButtonElement.addEventListener('click', function onCancelClick() {
      if (importedRedirectPath) {
        finishPartialImport();
        return;
      }
      closeModal();
    });
    continueButtonElement.addEventListener('click', startDownload);
    formElement.addEventListener('submit', function onCopyFormSubmit(submitEvent) {
      submitEvent.preventDefault();
      clearSelectionFields(formElement);
      requestPreview();
    });
    sourceUrlElement.focus();
  }

  window.SiteBrushCopySite = {
    open: openCopySiteModal
  };
})();
