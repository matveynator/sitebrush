(function initializeSiteBrushCopySiteModule() {
  if (window.SiteBrushCopySite && window.SiteBrushPublicTrial) {
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
      '.SiteBrushCopySiteDialog{width:min(760px,calc(100vw - 28px));max-height:calc(100vh - 28px);overflow:auto;background:#333;color:#fff;border:1px solid rgba(149,229,239,.28);border-radius:18px;box-shadow:0 28px 70px rgba(0,0,0,.35);padding:18px}',
      '.SiteBrushPublicTrialForm{display:grid;gap:12px;max-width:640px;padding:18px;border:1px solid rgba(149,229,239,.28);border-radius:18px;background:rgba(0,0,0,.18);color:#fff;font-family:Arial,Helvetica,sans-serif}',
      '.SiteBrushPublicTrialForm p{margin:0;color:#fff;font-size:20px;font-weight:700;line-height:1.25}',
      '.SiteBrushPublicTrialForm label{display:grid;gap:6px;color:#b7b7b7;font-size:13px;font-weight:700}',
      '.SiteBrushPublicTrialForm input{width:100%;box-sizing:border-box;border:1px solid rgba(149,229,239,.28);border-radius:10px;background:rgba(0,0,0,.22);color:#fff;font:inherit;padding:10px 12px}',
      '.SiteBrushPublicTrialForm button{display:inline-flex;align-items:center;justify-content:center;min-height:38px;border:1px solid rgba(149,229,239,.62);border-radius:10px;background:rgba(149,229,239,.18);color:#fff;font:inherit;font-weight:700;padding:9px 14px;cursor:pointer}',
      '.SiteBrushPublicTrialDialog{width:min(1180px,calc(100vw - 28px));height:calc(100vh - 28px);display:grid;grid-template-rows:auto minmax(0,1fr) minmax(0,1fr) auto;overflow:hidden}',
      '.SiteBrushCopySiteHeader{display:flex;align-items:center;justify-content:space-between;gap:12px;margin-bottom:14px}',
      '.SiteBrushCopySiteTitle{display:flex;align-items:center;gap:8px;margin:0;font-size:20px;line-height:1.2}',
      '.SiteBrushCopySiteTitle img{width:24px;height:24px}',
      '.SiteBrushCopySiteClose{border:0;background:transparent;color:#b7b7b7;font-size:26px;line-height:1;cursor:pointer;padding:2px 6px}',
      '.SiteBrushCopySiteForm{display:grid;gap:12px}',
      '.SiteBrushCopySitePrimaryRow{display:grid;grid-template-columns:minmax(0,1fr) auto;gap:8px}',
      '.SiteBrushCopySiteSecondaryGrid{display:grid;grid-template-columns:1fr 1fr;gap:10px}',
      '.SiteBrushCopySiteField{display:grid;gap:5px;font-size:13px;font-weight:700}',
      '.SiteBrushCopySiteInput,.SiteBrushCopySiteSelect{width:100%;box-sizing:border-box;border:1px solid rgba(149,229,239,.28);border-radius:10px;background:rgba(0,0,0,.22);color:#fff;font:inherit;font-weight:400;padding:9px 10px}',
      '.SiteBrushCopySiteCheckbox{display:flex;align-items:center;gap:8px;font-size:14px;font-weight:700}',
      '.SiteBrushCopySiteButton{display:inline-flex;align-items:center;justify-content:center;gap:7px;border:1px solid rgba(149,229,239,.62);border-radius:10px;background:rgba(149,229,239,.18);color:#fff;font:inherit;font-weight:700;padding:9px 12px;cursor:pointer;white-space:nowrap}',
      '.SiteBrushCopySiteButton img{width:18px;height:18px}',
      '.SiteBrushCopySiteButton:disabled{opacity:.58;cursor:not-allowed}',
      '.SiteBrushCopySiteProgress{height:22px;border-radius:999px;background:rgba(0,0,0,.38);overflow:hidden;margin-top:12px}',
      '.SiteBrushCopySiteProgressBar{height:100%;width:0%;background:#95e5ef;color:#252525;text-align:center;font-size:12px;font-weight:700;line-height:22px;transition:width .18s ease}',
      '.SiteBrushCopySiteStatus{margin:12px 0 0;color:#fff;font-size:14px;line-height:1.4;overflow-wrap:anywhere}',
      '.SiteBrushCopySiteURL{margin-top:5px;color:#b7b7b7;font-size:12px;overflow-wrap:anywhere}',
      '.SiteBrushCopySiteQuota{display:grid;gap:4px;margin-top:12px;border:1px solid rgba(149,229,239,.28);border-radius:10px;padding:10px;font-size:13px}',
      '.SiteBrushCopySiteQuotaLine{display:flex;justify-content:space-between;gap:12px}',
      '.SiteBrushCopySiteQuotaStatus{font-weight:700}.SiteBrushCopySiteQuotaStatus.is-ok{color:#8ee99b}.SiteBrushCopySiteQuotaStatus.is-error{color:#ffa500}',
      '.SiteBrushCopySiteResources{display:grid;gap:0;margin-top:12px;border:1px solid rgba(149,229,239,.28);border-radius:10px;max-height:270px;overflow:auto}',
      '.SiteBrushCopySiteResource{display:grid;grid-template-columns:auto auto minmax(0,1fr);gap:8px;align-items:start;padding:9px;border-bottom:1px solid rgba(149,229,239,.18);font-size:13px}',
      '.SiteBrushCopySiteResource:last-child{border-bottom:0}',
      '.SiteBrushCopySiteResourceKind{border-radius:999px;background:rgba(149,229,239,.18);color:#95e5ef;padding:2px 7px;font-size:11px}',
      '.SiteBrushCopySiteResourceURL{overflow-wrap:anywhere}',
      '.SiteBrushCopySiteResourceMeta{color:#b7b7b7;margin-top:3px}',
      '.SiteBrushCopySiteResourceReason{color:#ffa500;margin-top:3px}',
      '.SiteBrushPublicTrialResourceGroup{background:rgba(149,229,239,.08);font-weight:700}',
      '.SiteBrushCopySiteActions{display:flex;justify-content:flex-end;gap:8px;margin-top:14px}',
      '.SiteBrushCopySiteSecondaryButton{border:1px solid rgba(149,229,239,.28);border-radius:10px;background:rgba(0,0,0,.22);color:#fff;font:inherit;font-weight:700;padding:8px 12px;cursor:pointer}',
      '.SiteBrushPublicTrialFrame{width:100%;height:100%;border:1px solid rgba(149,229,239,.28);border-radius:10px;background:#fff}',
      '.SiteBrushPublicTrialStatusPanel{min-height:0;overflow:auto;padding-top:8px}',
      '.SiteBrushPublicTrialMetrics{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:8px;margin-top:12px}',
      '.SiteBrushPublicTrialMetric{display:flex;justify-content:space-between;gap:12px;border:1px solid rgba(149,229,239,.28);border-radius:10px;background:rgba(0,0,0,.18);padding:8px 10px;font-size:13px}',
      '.SiteBrushPublicTrialMetricLabel{color:#b7b7b7}.SiteBrushPublicTrialMetricValue{color:#fff;text-align:right;overflow-wrap:anywhere}',
      '.SiteBrushPublicTrialResult{font-weight:700}',
      '.SiteBrushCopySiteHidden{display:none!important}',
      '@media (max-width:640px){.SiteBrushCopySiteDialog{padding:14px}.SiteBrushCopySitePrimaryRow,.SiteBrushCopySiteSecondaryGrid,.SiteBrushPublicTrialMetrics{grid-template-columns:1fr}.SiteBrushCopySiteActions{flex-direction:column}.SiteBrushCopySiteButton,.SiteBrushCopySiteSecondaryButton{width:100%}}',
      '.SiteBrushCopySiteButton:hover,.SiteBrushCopySiteSecondaryButton:hover,.SiteBrushPublicTrialForm button:hover{border-color:#95e5ef;background:rgba(149,229,239,.28)}',
      '.SiteBrushCopySiteContinueButton{border-color:#2fbf71!important;background:#198754!important;color:#fff!important;box-shadow:0 0 0 1px rgba(25,135,84,.22)}',
      '.SiteBrushCopySiteContinueButton:hover{border-color:#48d589!important;background:#157347!important;color:#fff!important}'
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
    if (normalizedSize < 1024 * 1024 * 1024) {
      return (normalizedSize / 1024 / 1024).toFixed(1) + ' MB';
    }
    if (normalizedSize < 1024 * 1024 * 1024 * 1024) {
      return (normalizedSize / 1024 / 1024 / 1024).toFixed(1) + ' GB';
    }
    return (normalizedSize / 1024 / 1024 / 1024 / 1024).toFixed(1) + ' TB';
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
    const hiddenFieldElements = formElement.querySelectorAll('input[name="import_selection_confirmed"],input[name="import_resource_url"],input[name="import_download_total"],input[name="import_download_total_bytes"]');
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

  function appendDownloadPlanFields(formElement, downloadTotal, downloadTotalBytes) {
    appendHiddenField(formElement, 'import_download_total', String(Math.max(0, Number(downloadTotal) || 0)));
    appendHiddenField(formElement, 'import_download_total_bytes', String(Math.max(0, Number(downloadTotalBytes) || 0)));
  }

  function buildDownloadProgressModel(resourcesElement, wholeSiteImportSelected, downloadPreviewPayload) {
    const selectedResourceURLs = new Set();
    let selectedResourceBytes = 0;
    const checkboxElements = resourcesElement.querySelectorAll('input[data-sitebrush-copy-resource-url]');
    for (const checkboxElement of checkboxElements) {
      if (!checkboxElement.checked) {
        continue;
      }
      const resourceURL = checkboxElement.dataset.sitebrushCopyResourceUrl || '';
      if (resourceURL === '') {
        continue;
      }
      const resourceSizeBytes = Number(checkboxElement.dataset.sitebrushCopyResourceSizeBytes) || 0;
      selectedResourceURLs.add(resourceURL);
      if (resourceSizeBytes > 0) {
        selectedResourceBytes += resourceSizeBytes;
      }
    }
    const pageTargetCount = wholeSiteImportSelected ? Math.max(1, Number(downloadPreviewPayload && downloadPreviewPayload.page_count) || 1) : 0;
    const pageDownloadBytes = wholeSiteImportSelected ? Math.max(0, Number(downloadPreviewPayload && downloadPreviewPayload.page_download_bytes) || 0) : 0;
    return {
      selectedResourceURLs: selectedResourceURLs,
      completedURLs: new Set(),
      downloadedBytesByURL: new Map(),
      totalTargets: pageTargetCount + selectedResourceURLs.size,
      totalBytes: pageDownloadBytes + selectedResourceBytes
    };
  }

  function buildRetryProgressModel(failedResourceURLs) {
    return {
      selectedResourceURLs: new Set(failedResourceURLs),
      completedURLs: new Set(),
      downloadedBytesByURL: new Map(),
      totalTargets: Math.max(1, failedResourceURLs.size),
      totalBytes: 0
    };
  }

  function summarizeDownloadProgress(downloadProgressModel, progressPayload) {
    if (!downloadProgressModel || downloadProgressModel.totalTargets <= 0) {
      const serverDownloadTotal = Number(progressPayload.download_total) || 0;
      const serverDownloadedBytes = Number(progressPayload.downloaded_bytes) || 0;
      const serverDownloadTotalBytes = Number(progressPayload.download_total_bytes) || 0;
      let completedPercent = Number(progressPayload.completed_percent) || 0;
      if (serverDownloadTotalBytes > 0) {
        completedPercent = Math.round(Math.min(serverDownloadedBytes, serverDownloadTotalBytes) * 100 / serverDownloadTotalBytes);
      }
      return {
        completedTotal: Number(progressPayload.downloaded_total) || 0,
        foundTotal: serverDownloadTotal || Number(progressPayload.found_total) || 0,
        completedPercent: Math.max(0, Math.min(100, completedPercent))
      };
    }
    const currentURL = progressPayload.current_url || '';
    if (currentURL !== '') {
      const currentDownloadedBytes = Number(progressPayload.current_downloaded_bytes) || 0;
      const currentSizeBytes = Number(progressPayload.current_size_bytes) || 0;
      let recordedDownloadedBytes = currentDownloadedBytes;
      if (progressPayload.stage === 'downloaded' && currentSizeBytes > 0) {
        recordedDownloadedBytes = currentSizeBytes;
      }
      if (recordedDownloadedBytes > 0) {
        downloadProgressModel.downloadedBytesByURL.set(currentURL, recordedDownloadedBytes);
      }
      if (progressPayload.stage === 'downloaded' || progressPayload.stage === 'error') {
        downloadProgressModel.completedURLs.add(currentURL);
      }
    }
    let completedTotal = Math.min(downloadProgressModel.completedURLs.size, downloadProgressModel.totalTargets);
    let downloadedBytes = 0;
    for (const downloadedByteCount of downloadProgressModel.downloadedBytesByURL.values()) {
      downloadedBytes += Number(downloadedByteCount) || 0;
    }
    if (downloadProgressModel.totalBytes > 0) {
      downloadedBytes = Math.min(downloadedBytes, downloadProgressModel.totalBytes);
    }
    let completedPercent = 0;
    if (downloadProgressModel.totalBytes > 0) {
      completedPercent = Math.round(downloadedBytes * 100 / downloadProgressModel.totalBytes);
    } else {
      completedPercent = Math.round(completedTotal * 100 / downloadProgressModel.totalTargets);
    }
    if (progressPayload.stage === 'done' || progressPayload.stage === 'partial') {
      completedTotal = downloadProgressModel.totalTargets;
      completedPercent = 100;
    }
    return {
      completedTotal: completedTotal,
      foundTotal: downloadProgressModel.totalTargets,
      completedPercent: Math.max(0, Math.min(100, completedPercent))
    };
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

  function syncSelectedResources(formElement, resourcesElement, wholeSiteImportSelected, downloadPreviewPayload) {
    clearSelectionFields(formElement);
    appendHiddenField(formElement, 'import_selection_confirmed', '1');
    const checkboxElements = resourcesElement.querySelectorAll('input[data-sitebrush-copy-resource-url]');
    for (const checkboxElement of checkboxElements) {
      if (checkboxElement.checked) {
        appendHiddenField(formElement, 'import_resource_url', checkboxElement.dataset.sitebrushCopyResourceUrl || '');
      }
    }
    const downloadProgressSummary = buildDownloadProgressModel(resourcesElement, wholeSiteImportSelected, downloadPreviewPayload);
    appendDownloadPlanFields(formElement, downloadProgressSummary.totalTargets, downloadProgressSummary.totalBytes);
  }

  function syncFailedResources(formElement, failedResourceURLs) {
    clearSelectionFields(formElement);
    appendHiddenField(formElement, 'import_selection_confirmed', '1');
    const sortedFailedResourceURLs = Array.from(failedResourceURLs).sort();
    for (const failedResourceURL of sortedFailedResourceURLs) {
      appendHiddenField(formElement, 'import_resource_url', failedResourceURL);
    }
    appendDownloadPlanFields(formElement, sortedFailedResourceURLs.length, 0);
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
    const continueButtonElement = createElement('button', 'SiteBrushCopySiteButton SiteBrushCopySiteContinueButton SiteBrushCopySiteHidden', textFromConfig(configuration, 'continueButton', 'Continue'));
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
    let downloadProgressModel = null;
    let retryWasAttempted = false;
    let partialImportCanRetry = false;
    let requestIsRunning = false;
    let streamClosedIntentionally = false;
    let retryCountdownTimer = 0;
    let activeDownloadEndpoint = '?grab';
    let activeGrabToken = '';
    let downloadCancelRequested = false;

    function setFinishImportButtonMode(finishImportMode) {
      cancelButtonElement.textContent = finishImportMode
        ? textFromConfig(configuration, 'finishImport', 'Finish import')
        : textFromConfig(configuration, 'cancelButton', 'Cancel');
      cancelButtonElement.classList.toggle('SiteBrushCopySiteContinueButton', finishImportMode);
    }

    function setContinueButtonPrimaryAction(primaryAction) {
      continueButtonElement.classList.toggle('SiteBrushCopySiteContinueButton', primaryAction);
    }

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
      window.location.href = importedRedirectPath || targetPath || '/';
    }

    function requestActiveDownloadCancel() {
      if (activeGrabToken === '' || downloadCancelRequested) {
        return;
      }
      downloadCancelRequested = true;
      cancelButtonElement.disabled = true;
      statusElement.textContent = textFromConfig(configuration, 'partialImportClose', 'Finishing import...');
      const cancelRequestBody = new URLSearchParams();
      cancelRequestBody.append('progress_token', activeGrabToken);
      fetch(targetPath + '?grab_cancel', { method: 'POST', body: cancelRequestBody, headers: { Accept: 'application/json' } })
        .catch(function ignoreCancelError() {
          cancelButtonElement.disabled = false;
        });
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
      setFinishImportButtonMode(true);
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
      setContinueButtonPrimaryAction(false);
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
        statusText += '. ' + textFromConfig(configuration, 'retryNextIn', 'Next retry in') + ': ' + secondsLeft + ' ' + textFromConfig(configuration, 'retrySecondsSuffix', 's');
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
        const progressSummary = summarizeDownloadProgress(downloadProgressModel, progressPayload);
        const completedPercent = progressPayload.stage === 'done' ? 100 : progressSummary.completedPercent;
        setProgress(progressBarElement, completedPercent);
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
        const downloadedTotal = progressSummary.completedTotal;
        const foundTotal = progressSummary.foundTotal;
        const remainingTotal = Math.max(foundTotal - downloadedTotal, 0);
        const remainingPercent = Math.max(100 - completedPercent, 0);
        statusElement.textContent = textFromConfig(configuration, 'progressDownloadedPrefix', 'Downloaded') + ' ' + downloadedTotal + ' ' + textFromConfig(configuration, 'fromWord', 'of') + ' ' + foundTotal + ' (' + completedPercent + '%). ' + textFromConfig(configuration, 'leftWord', 'Left') + ' ' + remainingTotal + ' (' + remainingPercent + '%).';
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
      setFinishImportButtonMode(false);
      partialImportCanRetry = false;
      setContinueButtonPrimaryAction(true);
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
      activeGrabToken = '';
      downloadCancelRequested = false;
      previewPayload = null;
      downloadProgressModel = null;
      failedResourceURLs = new Set();
      failedResourceReasons = new Map();
      importedRedirectPath = '';
      retryWasAttempted = false;
      partialImportCanRetry = false;
      resourcesElement.replaceChildren();
      resourcesElement.classList.add('SiteBrushCopySiteHidden');
      quotaElement.classList.add('SiteBrushCopySiteHidden');
      continueButtonElement.classList.add('SiteBrushCopySiteHidden');
      setContinueButtonPrimaryAction(true);
      setFinishImportButtonMode(false);
      progressElement.classList.add('SiteBrushCopySiteHidden');
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
      activeGrabToken = progressToken;
      downloadCancelRequested = false;
      downloadFinishedWithErrors = false;
      const retryOnlyFailedResources = partialImportCanRetry && failedResourceURLs.size > 0;
      if (retryOnlyFailedResources) {
        retryWasAttempted = true;
        partialImportCanRetry = false;
        activeDownloadEndpoint = '?grab_retry';
        syncFailedResources(formElement, failedResourceURLs);
        downloadProgressModel = buildRetryProgressModel(failedResourceURLs);
      } else {
        retryWasAttempted = false;
        activeDownloadEndpoint = '?grab';
        failedResourceURLs = new Set();
        failedResourceReasons = new Map();
        partialImportCanRetry = false;
        syncSelectedResources(formElement, resourcesElement, wholeSiteElement.checked, previewPayload);
        downloadProgressModel = buildDownloadProgressModel(resourcesElement, wholeSiteElement.checked, previewPayload);
      }
      progressElement.classList.remove('SiteBrushCopySiteHidden');
      continueButtonElement.classList.add('SiteBrushCopySiteHidden');
      submitButtonElement.disabled = true;
      cancelButtonElement.disabled = false;
      setFinishImportButtonMode(true);
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
            activeGrabToken = '';
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
            activeGrabToken = '';
            submitButtonElement.disabled = false;
            cancelButtonElement.disabled = false;
            setFinishImportButtonMode(false);
            closeProgressStream();
            statusElement.textContent = downloadError.message || textFromConfig(configuration, 'loadPageFailed', 'Load failed.');
          });
      }, true);
    }

    closeButtonElement.addEventListener('click', closeModal);
    cancelButtonElement.addEventListener('click', function onCancelClick() {
      if (requestIsRunning && activeGrabToken !== '') {
        requestActiveDownloadCancel();
        return;
      }
      if (downloadFinishedWithErrors || importedRedirectPath) {
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

  function publicTrialText(configuration, textName, fallbackText) {
    return textFromConfig(configuration, textName, fallbackText);
  }

  function publicTrialWebSocketURL(endpointPath, progressToken) {
    const eventsURL = new URL(endpointPath || '/', window.location.href);
    eventsURL.search = '';
    eventsURL.searchParams.set('trial_site_ws', '');
    eventsURL.searchParams.set('token', progressToken);
    eventsURL.protocol = eventsURL.protocol === 'https:' ? 'wss:' : 'ws:';
    return eventsURL.toString();
  }

  function publicTrialFetchURL(endpointPath, queryName) {
    const requestURL = new URL(endpointPath || '/', window.location.href);
    requestURL.search = '';
    requestURL.searchParams.set(queryName, '');
    return requestURL.toString();
  }

  function publicTrialPreviewFrameURL(endpointPath, progressToken) {
    const requestURL = new URL(endpointPath || '/', window.location.href);
    requestURL.search = '';
    requestURL.searchParams.set('trial_site_preview_frame', '');
    requestURL.searchParams.set('token', progressToken);
    return requestURL.toString();
  }

  function mergePublicTrialTexts(configuration, textsPayload) {
    if (!textsPayload || !textsPayload.texts) {
      return configuration;
    }
    const nextConfiguration = configuration || {};
    const mergedTexts = {};
    const currentTexts = nextConfiguration.texts || {};
    for (const textName of Object.keys(currentTexts)) {
      mergedTexts[textName] = currentTexts[textName];
    }
    for (const textName of Object.keys(textsPayload.texts)) {
      mergedTexts[textName] = textsPayload.texts[textName];
    }
    nextConfiguration.texts = mergedTexts;
    return nextConfiguration;
  }

  function applyPublicTrialTextsToForm(formElement, configuration) {
    const titleElement = formElement.querySelector('[data-sitebrush-public-trial-title]');
    const labelTextElement = formElement.querySelector('[data-sitebrush-public-trial-label]');
    const buttonElement = formElement.querySelector('button[type="submit"]');
    if (titleElement) {
      titleElement.textContent = publicTrialText(configuration, 'formTitle', 'Enter the website where you want to launch SiteBrush:');
    }
    if (labelTextElement) {
      labelTextElement.textContent = publicTrialText(configuration, 'fieldLabel', 'Website address');
    }
    if (buttonElement) {
      buttonElement.textContent = publicTrialText(configuration, 'checkButton', 'Check website');
    }
  }

  function loadPublicTrialTexts(formElement, configuration) {
    const endpointPath = configuration.endpoint || '/';
    return fetch(publicTrialFetchURL(endpointPath, 'trial_site_texts'), { headers: { Accept: 'application/json' } })
      .then(function parsePublicTrialTexts(textsResponse) {
        if (!textsResponse.ok) {
          throw new Error('texts failed');
        }
        return textsResponse.json();
      })
      .then(function applyLoadedPublicTrialTexts(textsPayload) {
        mergePublicTrialTexts(configuration, textsPayload);
        applyPublicTrialTextsToForm(formElement, configuration);
      })
      .catch(function ignorePublicTrialTextLoad() {});
  }

  function createPublicTrialMetric(labelText, metricValue) {
    const metricElement = createElement('div', 'SiteBrushPublicTrialMetric');
    metricElement.appendChild(createElement('span', 'SiteBrushPublicTrialMetricLabel', labelText));
    metricElement.appendChild(createElement('strong', 'SiteBrushPublicTrialMetricValue', String(metricValue)));
    return metricElement;
  }

  function publicTrialPlanStorageText(previewPayload, configuration) {
    const planPayload = previewPayload && previewPayload.plan ? previewPayload.plan : {};
    const planName = String(planPayload.name || '').trim();
    const quotaBytes = Number(planPayload.quota_bytes) || 0;
    const quotaLabel = String(planPayload.quota_label || '').trim();
    const storageText = quotaLabel !== '' ? quotaLabel : formatSize(quotaBytes, configuration);
    if (planName === '') {
      return storageText;
    }
    return planName + ': ' + storageText;
  }

  function publicTrialPlanUsageText(previewPayload) {
    const planPayload = previewPayload && previewPayload.plan ? previewPayload.plan : {};
    const requiredBytes = Number(previewPayload && previewPayload.required_bytes) || 0;
    const quotaBytes = Number(planPayload.quota_bytes) || 0;
    if (quotaBytes <= 0) {
      return '0%';
    }
    return Math.ceil(requiredBytes * 100 / quotaBytes) + '%';
  }

  function formatPublicTrialText(templateText, firstValue, secondValue) {
    return String(templateText || '').replace('%s', firstValue).replace('%s', secondValue);
  }

  function publicTrialFreeCompatibilityText(previewPayload, configuration) {
    if (previewPayload && previewPayload.fits_free_plan) {
      return publicTrialText(configuration, 'freeFitResult', 'The website fits the free plan.');
    }
    const freePlanPayload = previewPayload && previewPayload.free_plan ? previewPayload.free_plan : {};
    const planPayload = previewPayload && previewPayload.plan ? previewPayload.plan : {};
    const freeQuotaLabel = String(freePlanPayload.quota_label || '').trim() || formatSize(freePlanPayload.quota_bytes, configuration);
    const planName = String(planPayload.name || '').trim() || publicTrialText(configuration, 'paidPlanFallback', 'a paid plan');
    return formatPublicTrialText(publicTrialText(configuration, 'paidFitResult', 'The website does not fit the free plan (%s). We recommend the %s plan.'), freeQuotaLabel, planName);
  }

  function publicTrialRequiredPlanText(previewPayload, configuration) {
    if (!previewPayload || previewPayload.fits_free_plan) {
      return '';
    }
    return publicTrialPlanStorageText(previewPayload, configuration);
  }

  function renderPublicTrialMetrics(metricListElement, configuration, previewPayload, progressState) {
    const resourceCounts = previewPayload && previewPayload.resource_counts ? previewPayload.resource_counts : {};
    const foundPages = previewPayload ? previewPayload.page_count : Number(progressState.found_total) || 0;
    if (!previewPayload) {
      metricListElement.replaceChildren(createPublicTrialMetric(publicTrialText(configuration, 'pages', 'Found pages'), foundPages));
      return;
    }
    const metricElements = [
      createPublicTrialMetric(publicTrialText(configuration, 'pages', 'Found pages'), previewPayload.page_count),
      createPublicTrialMetric(publicTrialText(configuration, 'files', 'Found files'), previewPayload.resource_count),
      createPublicTrialMetric(publicTrialText(configuration, 'images', 'Images'), Number(resourceCounts.images) || 0),
      createPublicTrialMetric(publicTrialText(configuration, 'css', 'CSS'), Number(resourceCounts.css) || 0),
      createPublicTrialMetric(publicTrialText(configuration, 'js', 'JS'), Number(resourceCounts.js) || 0),
      createPublicTrialMetric(publicTrialText(configuration, 'other', 'Other resources'), Number(resourceCounts.other) || 0),
      createPublicTrialMetric(publicTrialText(configuration, 'estimatedSize', 'Estimated total size'), formatSize(previewPayload.total_bytes, configuration)),
      createPublicTrialMetric(publicTrialText(configuration, 'requiredSpace', 'Required disk space'), formatSize(previewPayload.required_bytes, configuration)),
      createPublicTrialMetric(publicTrialText(configuration, 'freeCompatibility', 'Free plan compatibility'), publicTrialFreeCompatibilityText(previewPayload, configuration)),
      createPublicTrialMetric(publicTrialText(configuration, 'planStorage', 'Plan storage'), publicTrialPlanStorageText(previewPayload, configuration)),
      createPublicTrialMetric(publicTrialText(configuration, 'planUsage', 'Storage used'), publicTrialPlanUsageText(previewPayload))
    ];
    const requiredPlanText = publicTrialRequiredPlanText(previewPayload, configuration);
    if (requiredPlanText !== '') {
      metricElements.push(createPublicTrialMetric(publicTrialText(configuration, 'requiredPlan', 'Minimal required paid plan'), requiredPlanText));
    }
    metricListElement.replaceChildren.apply(metricListElement, metricElements);
  }

  function publicTrialResourcePath(resourceURL) {
    try {
      const parsedURL = new URL(resourceURL);
      return parsedURL.pathname || '/';
    } catch (parseError) {
      return '/';
    }
  }

  function publicTrialResourceGroupKey(resourceURL) {
    const resourcePath = publicTrialResourcePath(resourceURL);
    const pathParts = resourcePath.split('/').filter(function keepPathPart(pathPart) { return pathPart !== ''; });
    if (pathParts.length <= 1) {
      return '/';
    }
    return '/' + pathParts.slice(0, pathParts.length - 1).join('/') + '/';
  }

  function publicTrialResourceGroupRows(resources) {
    const groupsByPath = new Map();
    for (const previewResource of resources || []) {
      const groupPath = publicTrialResourceGroupKey(previewResource.url || '');
      const existingGroup = groupsByPath.get(groupPath) || { path: groupPath, count: 0, sizeBytes: 0 };
      existingGroup.count += 1;
      existingGroup.sizeBytes += Number(previewResource.size_bytes) || 0;
      groupsByPath.set(groupPath, existingGroup);
    }
    return Array.from(groupsByPath.values()).filter(function keepUsefulGroup(groupPayload) {
      return groupPayload.count > 1;
    }).sort(function sortGroupRows(leftGroup, rightGroup) {
      return rightGroup.sizeBytes - leftGroup.sizeBytes || leftGroup.path.localeCompare(rightGroup.path);
    });
  }

  function selectedPublicTrialResourcePayload(resourcesElement) {
    const selectedResources = [];
    let selectedBytes = 0;
    const selectedCounts = { images: 0, css: 0, js: 0, other: 0 };
    const checkboxElements = resourcesElement.querySelectorAll('input[data-sitebrush-copy-resource-url]');
    for (const checkboxElement of checkboxElements) {
      if (!checkboxElement.checked) {
        continue;
      }
      const resourceKind = String(checkboxElement.dataset.sitebrushCopyResourceKind || '').toLowerCase();
      const resourceSizeBytes = Number(checkboxElement.dataset.sitebrushCopyResourceSizeBytes) || 0;
      selectedResources.push(checkboxElement.dataset.sitebrushCopyResourceUrl || '');
      selectedBytes += resourceSizeBytes;
      if (resourceKind === 'image') {
        selectedCounts.images += 1;
      } else if (resourceKind === 'style' || resourceKind === 'css' || resourceKind === 'stylesheet') {
        selectedCounts.css += 1;
      } else if (resourceKind === 'script' || resourceKind === 'js' || resourceKind === 'javascript') {
        selectedCounts.js += 1;
      } else {
        selectedCounts.other += 1;
      }
    }
    return { urls: selectedResources, bytes: selectedBytes, counts: selectedCounts };
  }

  function selectedPublicTrialPreviewPayload(previewPayload, resourcesElement) {
    if (!previewPayload) {
      return null;
    }
    const selectedPayload = selectedPublicTrialResourcePayload(resourcesElement);
    const pageBytes = Number(previewPayload.page_bytes) || 0;
    const requiredBytes = pageBytes + selectedPayload.bytes;
    const freePlan = previewPayload.free_plan || {};
    const paidPlan = previewPayload.plan || {};
    const freeQuotaBytes = Number(freePlan.quota_bytes) || 0;
    const fitsFreePlan = freeQuotaBytes > 0 && requiredBytes <= freeQuotaBytes;
    const selectedPlan = fitsFreePlan ? freePlan : paidPlan;
    return {
      source_url: previewPayload.source_url,
      page_count: previewPayload.page_count,
      resource_count: selectedPayload.urls.length,
      resource_counts: selectedPayload.counts,
      total_bytes: requiredBytes,
      required_bytes: requiredBytes,
      fits_free_plan: fitsFreePlan,
      plan: selectedPlan,
      free_plan: freePlan,
      message: previewPayload.message
    };
  }

  function syncPublicTrialGroupState(resourcesElement) {
    const groupCheckboxElements = resourcesElement.querySelectorAll('input[data-sitebrush-public-trial-group-path]');
    for (const groupCheckboxElement of groupCheckboxElements) {
      const groupPath = groupCheckboxElement.dataset.sitebrushPublicTrialGroupPath || '';
      const childCheckboxElements = Array.from(resourcesElement.querySelectorAll('input[data-sitebrush-copy-resource-url]')).filter(function keepGroupChild(childCheckboxElement) {
        return publicTrialResourceGroupKey(childCheckboxElement.dataset.sitebrushCopyResourceUrl || '') === groupPath;
      });
      const checkedCount = childCheckboxElements.filter(function countChecked(childCheckboxElement) { return childCheckboxElement.checked; }).length;
      groupCheckboxElement.checked = childCheckboxElements.length > 0 && checkedCount === childCheckboxElements.length;
      groupCheckboxElement.indeterminate = checkedCount > 0 && checkedCount < childCheckboxElements.length;
    }
  }

  function publicTrialResultText(previewPayload, configuration) {
    if (!previewPayload) {
      return '';
    }
    if (previewPayload.fits_free_plan) {
      return publicTrialText(configuration, 'freeResult', 'Great – this website can be launched on the free SiteBrush plan.');
    }
    return publicTrialText(configuration, 'paidResult', 'This website requires a paid plan, but you can test SiteBrush for free for 1 month. Payment is required only after the trial period.');
  }

  function renderPublicTrialResources(resourcesElement, metricListElement, resultElement, configuration, previewPayload) {
    resourcesElement.replaceChildren();
    const resources = Array.isArray(previewPayload && previewPayload.resources) ? previewPayload.resources : [];
    if (resources.length === 0) {
      resourcesElement.classList.add('SiteBrushCopySiteHidden');
      return;
    }
    function updatePublicTrialSelection() {
      syncPublicTrialGroupState(resourcesElement);
      const selectedPreviewPayload = selectedPublicTrialPreviewPayload(previewPayload, resourcesElement);
      renderPublicTrialMetrics(metricListElement, configuration, selectedPreviewPayload, {});
      resultElement.textContent = publicTrialResultText(selectedPreviewPayload, configuration);
    }
    for (const groupPayload of publicTrialResourceGroupRows(resources)) {
      const groupRowElement = createElement('label', 'SiteBrushCopySiteResource SiteBrushPublicTrialResourceGroup');
      const groupCheckboxElement = document.createElement('input');
      groupCheckboxElement.type = 'checkbox';
      groupCheckboxElement.checked = true;
      groupCheckboxElement.dataset.sitebrushPublicTrialGroupPath = groupPayload.path;
      groupCheckboxElement.addEventListener('change', function onPublicTrialGroupChange() {
        const childCheckboxElements = resourcesElement.querySelectorAll('input[data-sitebrush-copy-resource-url]');
        for (const childCheckboxElement of childCheckboxElements) {
          if (publicTrialResourceGroupKey(childCheckboxElement.dataset.sitebrushCopyResourceUrl || '') === groupPayload.path) {
            childCheckboxElement.checked = groupCheckboxElement.checked;
          }
        }
        updatePublicTrialSelection();
      });
      const detailsElement = createElement('div', '');
      detailsElement.appendChild(createElement('div', 'SiteBrushCopySiteResourceURL', groupPayload.path));
      detailsElement.appendChild(createElement('div', 'SiteBrushCopySiteResourceMeta', groupPayload.count + ' · ' + formatSize(groupPayload.sizeBytes, configuration)));
      groupRowElement.appendChild(groupCheckboxElement);
      groupRowElement.appendChild(createElement('span', 'SiteBrushCopySiteResourceKind', publicTrialText(configuration, 'files', 'Found files')));
      groupRowElement.appendChild(detailsElement);
      resourcesElement.appendChild(groupRowElement);
    }
    for (const previewResource of resources) {
      appendPreviewResource(resourcesElement, previewResource, configuration, updatePublicTrialSelection);
      const checkboxElement = resourcesElement.lastElementChild ? resourcesElement.lastElementChild.querySelector('input[data-sitebrush-copy-resource-url]') : null;
      if (checkboxElement) {
        checkboxElement.dataset.sitebrushCopyResourceKind = previewResource.kind || '';
      }
    }
    resourcesElement.classList.remove('SiteBrushCopySiteHidden');
    updatePublicTrialSelection();
  }

  function openPublicTrialModal(formElement, configuration) {
    ensureCopySiteStyles();
    const endpointPath = configuration.endpoint || '/';
    const progressToken = randomProgressToken();
    const overlayElement = createElement('div', 'SiteBrushCopySiteOverlay SiteBrushPublicTrialOverlay');
    const dialogElement = createElement('div', 'SiteBrushCopySiteDialog SiteBrushPublicTrialDialog');
    const headerElement = createElement('div', 'SiteBrushCopySiteHeader');
    const titleElement = createElement('h2', 'SiteBrushCopySiteTitle', publicTrialText(configuration, 'modalTitle', 'Checking if SiteBrush can be installed on the selected website:'));
    const closeButtonElement = createElement('button', 'SiteBrushCopySiteClose', '×');
    closeButtonElement.type = 'button';
    headerElement.appendChild(titleElement);
    headerElement.appendChild(closeButtonElement);
    const previewFrameElement = createElement('iframe', 'SiteBrushPublicTrialFrame');
    previewFrameElement.setAttribute('sandbox', '');
    previewFrameElement.srcdoc = '<!doctype html><html><body></body></html>';
    previewFrameElement.title = publicTrialText(configuration, 'preparing', 'Preparing the website for SiteBrush...');
    const statusPanelElement = createElement('div', 'SiteBrushPublicTrialStatusPanel');
    const statusElement = createElement('p', 'SiteBrushCopySiteStatus', publicTrialText(configuration, 'analyzing', 'Analyzing the website...'));
    const currentURLElement = createElement('div', 'SiteBrushCopySiteURL', '');
    const progressElement = createElement('div', 'SiteBrushCopySiteProgress');
    const progressBarElement = createElement('div', 'SiteBrushCopySiteProgressBar', '0%');
    const metricListElement = createElement('div', 'SiteBrushPublicTrialMetrics');
    const resourcesElement = createElement('div', 'SiteBrushCopySiteResources SiteBrushCopySiteHidden');
    const failuresElement = createElement('div', 'SiteBrushCopySiteResources SiteBrushCopySiteHidden');
    const resultElement = createElement('p', 'SiteBrushCopySiteStatus SiteBrushPublicTrialResult', '');
    const actionRowElement = createElement('div', 'SiteBrushCopySiteActions');
    const createButtonElement = createElement('button', 'SiteBrushCopySiteButton SiteBrushCopySiteHidden', publicTrialText(configuration, 'createButton', 'Create test website'));
    progressElement.appendChild(progressBarElement);
    statusPanelElement.appendChild(statusElement);
    statusPanelElement.appendChild(currentURLElement);
    statusPanelElement.appendChild(progressElement);
    statusPanelElement.appendChild(metricListElement);
    statusPanelElement.appendChild(resourcesElement);
    statusPanelElement.appendChild(failuresElement);
    statusPanelElement.appendChild(resultElement);
    actionRowElement.appendChild(createButtonElement);
    dialogElement.appendChild(headerElement);
    dialogElement.appendChild(previewFrameElement);
    dialogElement.appendChild(statusPanelElement);
    dialogElement.appendChild(actionRowElement);
    overlayElement.appendChild(dialogElement);
    document.body.appendChild(overlayElement);
    let previewPayload = null;
    let progressSocket = null;
    let previewRequestSubmitted = false;
    let previewFrameReloadTimer = 0;
    let previewStatusTimer = 0;
    let previewStatusRequestRunning = false;
    let progressReconnectTimer = 0;
    let progressReconnectAttempt = 0;
    let progressSocketPhase = 'preview';
    let modalClosed = false;
    let previewComplete = false;

    function closePublicTrialModal() {
      modalClosed = true;
      progressSocketPhase = 'closed';
      if (progressSocket) {
        const socketToClose = progressSocket;
        progressSocket = null;
        socketToClose.close();
      }
      if (previewFrameReloadTimer) {
        window.clearInterval(previewFrameReloadTimer);
        previewFrameReloadTimer = 0;
      }
      if (previewStatusTimer) {
        window.clearInterval(previewStatusTimer);
        previewStatusTimer = 0;
      }
      if (progressReconnectTimer) {
        window.clearTimeout(progressReconnectTimer);
        progressReconnectTimer = 0;
      }
      if (!previewComplete) {
        const cancelBody = new URLSearchParams();
        cancelBody.set('progress_token', progressToken);
        fetch(publicTrialFetchURL(endpointPath, 'trial_site_preview_cancel'), { method: 'POST', body: cancelBody, keepalive: true }).catch(function ignorePreviewCancelError() {});
      }
      overlayElement.remove();
    }

    function publicTrialRetryStatusText(progressState) {
      const retryAttempt = Number(progressState.retry_attempt) || 0;
      const retryTotal = Number(progressState.retry_total) || 0;
      const retryDelaySeconds = Number(progressState.retry_delay_seconds) || 0;
      let statusText = publicTrialText(configuration, 'preparing', 'Preparing the website for SiteBrush...');
      if (retryAttempt > 0 && retryTotal > 0) {
        statusText += ' ' + retryAttempt + '/' + retryTotal;
      }
      if (retryDelaySeconds > 0) {
        statusText += '. ' + publicTrialText(configuration, 'retryNextIn', 'Next retry in') + ': ' + retryDelaySeconds + ' ' + publicTrialText(configuration, 'retrySecondsSuffix', 's');
      }
      return statusText;
    }

    function stopPublicTrialStatusPolling() {
      if (!previewStatusTimer) {
        return;
      }
      window.clearInterval(previewStatusTimer);
      previewStatusTimer = 0;
    }

    function renderPublicTrialFailures(previewResponsePayload) {
      failuresElement.replaceChildren();
      const failedReasons = previewResponsePayload && previewResponsePayload.failed_reasons && typeof previewResponsePayload.failed_reasons === 'object' ? previewResponsePayload.failed_reasons : {};
      const failedURLs = new Set(Array.isArray(previewResponsePayload && previewResponsePayload.failed_urls) ? previewResponsePayload.failed_urls : []);
      for (const failedURL of Object.keys(failedReasons)) {
        failedURLs.add(failedURL);
      }
      if (failedURLs.size === 0) {
        failuresElement.classList.add('SiteBrushCopySiteHidden');
        return;
      }
      const titleElement = createElement('div', 'SiteBrushCopySiteResource');
      titleElement.appendChild(createElement('span', 'SiteBrushCopySiteResourceKind', String(failedURLs.size)));
      titleElement.appendChild(createElement('span', '', ''));
      titleElement.appendChild(createElement('div', 'SiteBrushCopySiteResourceURL', textFromConfig(configuration, 'failedResourcesTitle', 'Failed resources:')));
      failuresElement.appendChild(titleElement);
      for (const failedURL of Array.from(failedURLs).sort()) {
        const failureRowElement = createElement('div', 'SiteBrushCopySiteResource');
        const failureDetailsElement = createElement('div', '');
        failureDetailsElement.appendChild(createElement('div', 'SiteBrushCopySiteResourceURL', failedURL));
        const failureReason = String(failedReasons[failedURL] || '').trim();
        if (failureReason !== '') {
          failureDetailsElement.appendChild(createElement('div', 'SiteBrushCopySiteResourceReason', failureReason));
        }
        failureRowElement.appendChild(createElement('span', 'SiteBrushCopySiteResourceKind', textFromConfig(configuration, 'failedResourceBadge', 'failed')));
        failureRowElement.appendChild(createElement('span', '', ''));
        failureRowElement.appendChild(failureDetailsElement);
        failuresElement.appendChild(failureRowElement);
      }
      failuresElement.classList.remove('SiteBrushCopySiteHidden');
    }

    function applyPublicTrialPreview(previewResponsePayload, terminal) {
      if (modalClosed) {
        return;
      }
      previewPayload = previewResponsePayload;
      if (terminal) {
        previewComplete = true;
        progressSocketPhase = 'done';
        stopPublicTrialStatusPolling();
        setProgress(progressBarElement, 100);
      }
      if (previewFrameReloadTimer && terminal) {
        window.clearInterval(previewFrameReloadTimer);
        previewFrameReloadTimer = 0;
      }
      if (previewPayload.preview_url && previewFrameElement.src !== previewPayload.preview_url) {
        previewFrameElement.src = previewPayload.preview_url;
      }
      renderPublicTrialResources(resourcesElement, metricListElement, resultElement, configuration, previewPayload);
      renderPublicTrialFailures(previewPayload);
      if (!resultElement.textContent) {
        resultElement.textContent = publicTrialResultText(previewPayload, configuration);
      }
      statusElement.textContent = terminal
        ? publicTrialText(configuration, 'partialReady', 'The available pages are ready for testing.')
        : publicTrialText(configuration, 'usableWhileChecking', 'The preview is ready. The remaining pages are still being checked.');
      createButtonElement.classList.remove('SiteBrushCopySiteHidden');
      if (terminal && progressSocket) {
        const socketToClose = progressSocket;
        progressSocket = null;
        socketToClose.close();
      }
    }

    function finishPublicTrialPreview(previewResponsePayload) {
      applyPublicTrialPreview(previewResponsePayload, true);
    }

    function requestPublicTrialPreviewStatus() {
      if (previewComplete || previewStatusRequestRunning || modalClosed) {
        return;
      }
      previewStatusRequestRunning = true;
      const statusURL = new URL(publicTrialFetchURL(endpointPath, 'trial_site_preview_status'));
      statusURL.searchParams.set('token', progressToken);
      fetch(statusURL.toString(), { headers: { Accept: 'application/json' }, cache: 'no-store' })
        .then(function parsePublicTrialStatus(statusResponse) {
          if (!statusResponse.ok && statusResponse.status !== 202) {
            throw new Error(publicTrialText(configuration, 'loadFailed', 'Website analysis failed.'));
          }
          return statusResponse.json();
        })
        .then(function applyPublicTrialStatus(statusPayload) {
          previewStatusRequestRunning = false;
          if (statusPayload.status === 'done' || statusPayload.status === 'partial') {
            finishPublicTrialPreview(statusPayload);
            return;
          }
          if (statusPayload.usable || statusPayload.source_url) {
            applyPublicTrialPreview(statusPayload, false);
          }
          if (statusPayload.status === 'error') {
            progressSocketPhase = 'done';
            stopPublicTrialStatusPolling();
            if (previewFrameReloadTimer) {
              window.clearInterval(previewFrameReloadTimer);
              previewFrameReloadTimer = 0;
            }
            if (progressSocket) {
              const socketToClose = progressSocket;
              progressSocket = null;
              socketToClose.close();
            }
            statusElement.textContent = statusPayload.error || publicTrialText(configuration, 'loadFailed', 'Website analysis failed.');
          }
        })
        .catch(function keepWaitingAfterStatusError() {
          previewStatusRequestRunning = false;
        });
    }

    function startPublicTrialStatusPolling() {
      if (previewStatusTimer || previewComplete || modalClosed) {
        return;
      }
      requestPublicTrialPreviewStatus();
      previewStatusTimer = window.setInterval(requestPublicTrialPreviewStatus, 5000);
    }

    function schedulePublicTrialProgressReconnect(submitPreviewRequest) {
      if (progressReconnectTimer || progressSocketPhase !== 'preview' || modalClosed) {
        return;
      }
      const reconnectDelaySeconds = Math.min(Math.pow(2, progressReconnectAttempt), 10);
      progressReconnectAttempt += 1;
      statusElement.textContent = publicTrialText(configuration, 'reconnecting', 'Restoring the connection. The website check continues...');
      progressReconnectTimer = window.setTimeout(function reconnectPublicTrialProgress() {
        progressReconnectTimer = 0;
        connectProgressSocket(submitPreviewRequest);
      }, reconnectDelaySeconds * 1000);
    }

    function connectProgressSocket(submitPreviewRequest) {
      if (progressSocketPhase !== 'preview' || modalClosed) {
        return;
      }
      const connectedSocket = new WebSocket(publicTrialWebSocketURL(endpointPath, progressToken));
      progressSocket = connectedSocket;
      connectedSocket.onopen = function onPublicTrialProgressOpen() {
        progressReconnectAttempt = 0;
      };
      connectedSocket.onmessage = function onPublicTrialProgressMessage(messageEvent) {
        if (connectedSocket !== progressSocket) {
          return;
        }
        let progressState = null;
        try {
          progressState = JSON.parse(messageEvent.data);
        } catch (parseError) {
          return;
        }
        if (progressState.stage === 'ready') {
          submitPreviewRequest();
          return;
        }
        if (progressState.stage === 'heartbeat') {
          return;
        }
        const foundTotal = Number(progressState.found_total) || 0;
        const downloadedTotal = Number(progressState.downloaded_total) || 0;
        const progressTotal = Math.max(foundTotal, downloadedTotal, 1);
        setProgress(progressBarElement, downloadedTotal * 100 / progressTotal);
        renderPublicTrialMetrics(metricListElement, configuration, previewPayload, progressState);
        if (progressState.stage === 'retry_wait' || progressState.stage === 'retrying' || progressState.stage === 'source_attempt') {
          statusElement.textContent = publicTrialRetryStatusText(progressState);
        } else {
          statusElement.textContent = publicTrialText(configuration, 'preparing', 'Preparing the website for SiteBrush...');
        }
        currentURLElement.textContent = progressState.current_url || '';
        if (progressState.stage === 'done' || progressState.stage === 'partial' || (progressState.stage === 'error' && !progressState.current_url)) {
          progressSocketPhase = 'preview-result';
          requestPublicTrialPreviewStatus();
        }
      };
      connectedSocket.onerror = function onPublicTrialProgressError() {
        if (progressSocketPhase === 'preview') {
          statusElement.textContent = publicTrialText(configuration, 'reconnecting', 'Restoring the connection. The website check continues...');
        }
      };
      connectedSocket.onclose = function onPublicTrialProgressClose() {
        if (connectedSocket !== progressSocket) {
          return;
        }
        progressSocket = null;
        schedulePublicTrialProgressReconnect(submitPreviewRequest);
      };
      window.setTimeout(function submitPublicTrialIfSocketReadyWasMissed() {
        if (!previewPayload && !previewRequestSubmitted) {
          submitPreviewRequest();
        }
      }, 900);
    }

    function appendSelectedPublicTrialResources(requestBody) {
      requestBody.set('import_selection_confirmed', '1');
      const checkboxElements = resourcesElement.querySelectorAll('input[data-sitebrush-copy-resource-url]');
      for (const checkboxElement of checkboxElements) {
        if (checkboxElement.checked) {
          requestBody.append('import_resource_url', checkboxElement.dataset.sitebrushCopyResourceUrl || '');
        }
      }
    }

    function connectCreateProgressSocket(createProgressToken, submitCreateRequest) {
      progressSocketPhase = 'create';
      if (progressSocket) {
        const socketToClose = progressSocket;
        progressSocket = null;
        socketToClose.close();
      }
      let createRequestSubmitted = false;
      progressSocket = new WebSocket(publicTrialWebSocketURL(endpointPath, createProgressToken));
      progressSocket.onmessage = function onPublicTrialCreateProgressMessage(messageEvent) {
        let progressState = null;
        try {
          progressState = JSON.parse(messageEvent.data);
        } catch (parseError) {
          return;
        }
        if (progressState.stage === 'ready') {
          if (!createRequestSubmitted) {
            createRequestSubmitted = true;
            submitCreateRequest();
          }
          return;
        }
        if (progressState.stage === 'heartbeat') {
          return;
        }
        if (progressState.stage === 'retry_wait' || progressState.stage === 'retrying') {
          statusElement.textContent = publicTrialRetryStatusText(progressState);
        } else {
          statusElement.textContent = publicTrialText(configuration, 'creating', 'Creating a test version with the SiteBrush editor...');
        }
        currentURLElement.textContent = progressState.current_url || '';
        const completedPercent = Number(progressState.completed_percent) || 0;
        setProgress(progressBarElement, completedPercent);
      };
      progressSocket.onerror = function onPublicTrialCreateProgressError() {
        statusElement.textContent = publicTrialText(configuration, 'creating', 'Creating a test version with the SiteBrush editor...');
      };
      window.setTimeout(function submitPublicTrialCreateIfSocketReadyWasMissed() {
        if (!createRequestSubmitted) {
          createRequestSubmitted = true;
          submitCreateRequest();
        }
      }, 900);
    }

    function submitPreviewRequest() {
      if (previewRequestSubmitted) {
        return;
      }
      previewRequestSubmitted = true;
      const requestBody = new URLSearchParams();
      requestBody.set('progress_token', progressToken);
      requestBody.set('source_url', String(new FormData(formElement).get('source_url') || ''));
      requestBody.set('async_preview', '1');
      previewFrameElement.removeAttribute('srcdoc');
      previewFrameElement.src = publicTrialPreviewFrameURL(endpointPath, progressToken);
      previewFrameReloadTimer = window.setInterval(function reloadPublicTrialPreviewFrame() {
        if (previewComplete) {
          window.clearInterval(previewFrameReloadTimer);
          previewFrameReloadTimer = 0;
          return;
        }
        previewFrameElement.src = publicTrialPreviewFrameURL(endpointPath, progressToken) + '&refresh=' + encodeURIComponent(String(Date.now()));
      }, 4000);
      startPublicTrialStatusPolling();
      fetch(publicTrialFetchURL(endpointPath, 'trial_site_preview'), { method: 'POST', body: requestBody, headers: { Accept: 'application/json' } })
        .then(function parsePublicTrialPreviewResponse(previewResponse) {
          if (!previewResponse.ok) {
            return previewResponse.text().then(function throwPublicTrialPreviewError(errorText) {
              throw new Error(errorText || publicTrialText(configuration, 'loadFailed', 'Website analysis failed.'));
            });
          }
          return previewResponse.json();
        })
        .then(function renderPublicTrialPreview(previewResponsePayload) {
          if (previewResponsePayload.status === 'running' || previewResponsePayload.status === 'canceling') {
            if (previewResponsePayload.usable || previewResponsePayload.source_url) {
              applyPublicTrialPreview(previewResponsePayload, false);
            }
            return;
          }
          finishPublicTrialPreview(previewResponsePayload);
        })
        .catch(function keepWaitingAfterPreviewRequestError() {
          startPublicTrialStatusPolling();
        });
    }

    function createPublicTrialSite() {
      if (!previewPayload) {
        return;
      }
      createButtonElement.disabled = true;
      statusElement.textContent = publicTrialText(configuration, 'creating', 'Creating a test version with the SiteBrush editor...');
      progressElement.classList.remove('SiteBrushCopySiteHidden');
      setProgress(progressBarElement, 0);
      const requestBody = new URLSearchParams();
      requestBody.set('progress_token', progressToken);
      const createProgressToken = randomProgressToken();
      requestBody.set('create_progress_token', createProgressToken);
      appendSelectedPublicTrialResources(requestBody);
      connectCreateProgressSocket(createProgressToken, function submitPublicTrialCreateRequest() {
        fetch(publicTrialFetchURL(endpointPath, 'trial_site_create'), { method: 'POST', body: requestBody, headers: { Accept: 'application/json' } })
        .then(function parsePublicTrialCreateResponse(createResponse) {
          if (!createResponse.ok) {
            return createResponse.text().then(function throwPublicTrialCreateError(errorText) {
              throw new Error(errorText || publicTrialText(configuration, 'loadFailed', 'Website creation failed.'));
            });
          }
          return createResponse.json();
        })
        .then(function redirectToPublicTrialSite(createPayload) {
          window.location.href = createPayload.redirect || '/';
        })
        .catch(function renderPublicTrialCreateError(createError) {
          createButtonElement.disabled = false;
          statusElement.textContent = createError.message || publicTrialText(configuration, 'loadFailed', 'Website creation failed.');
        });
      });
    }

    closeButtonElement.addEventListener('click', closePublicTrialModal);
    createButtonElement.addEventListener('click', createPublicTrialSite);
    renderPublicTrialMetrics(metricListElement, configuration, null, {});
    connectProgressSocket(submitPreviewRequest);
  }

  function attachPublicTrialForm(formElement, configuration) {
    if (!formElement || formElement.dataset.sitebrushPublicTrialAttached === '1') {
      return;
    }
    const publicTrialConfiguration = configuration || {};
    formElement.dataset.sitebrushPublicTrialAttached = '1';
    applyPublicTrialTextsToForm(formElement, publicTrialConfiguration);
    const textsReady = loadPublicTrialTexts(formElement, publicTrialConfiguration);
    formElement.addEventListener('submit', function onPublicTrialSubmit(submitEvent) {
      submitEvent.preventDefault();
      if (!formElement.reportValidity()) {
        return;
      }
      textsReady.then(function openLocalizedPublicTrialModal() {
        openPublicTrialModal(formElement, publicTrialConfiguration);
      });
    });
  }

  window.SiteBrushCopySite = {
    open: openCopySiteModal
  };
  window.SiteBrushPublicTrial = {
    attach: attachPublicTrialForm
  };
})();
