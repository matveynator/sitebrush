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
      '.SiteBrushCopySiteStatus.is-success{color:#8ee99b}.SiteBrushCopySiteStatus.is-warning{color:#ffbd66}.SiteBrushCopySiteStatus.is-error{color:#ff8f96}',
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
      '.SiteBrushCopySiteActions{display:flex;justify-content:flex-end;gap:8px;margin-top:14px}',
      '.SiteBrushCopySiteSecondaryButton{border:1px solid rgba(149,229,239,.28);border-radius:10px;background:rgba(0,0,0,.22);color:#fff;font:inherit;font-weight:700;padding:8px 12px;cursor:pointer}',
      '.SiteBrushCopySiteHidden{display:none!important}',
      '@media (max-width:640px){.SiteBrushCopySiteDialog{padding:14px}.SiteBrushCopySitePrimaryRow,.SiteBrushCopySiteSecondaryGrid{grid-template-columns:1fr}.SiteBrushCopySiteActions{flex-direction:column}.SiteBrushCopySiteButton,.SiteBrushCopySiteSecondaryButton{width:100%}}',
      '.SiteBrushCopySiteButton:hover,.SiteBrushCopySiteSecondaryButton:hover,.SiteBrushPublicTrialForm button:hover{border-color:#95e5ef;background:rgba(149,229,239,.28)}',
      '.SiteBrushCopySiteContinueButton{border-color:#2fbf71!important;background:#198754!important;color:#fff!important;box-shadow:0 0 0 1px rgba(25,135,84,.22)}',
      '.SiteBrushCopySiteContinueButton:hover{border-color:#48d589!important;background:#157347!important;color:#fff!important}',
      '@media (max-width:640px){.SiteBrushCopySiteOverlay{align-items:flex-start;padding:8px;background:linear-gradient(145deg,rgba(3,45,36,.86),rgba(8,122,85,.82));overflow:auto}.SiteBrushCopySiteDialog{width:100%;max-height:calc(100dvh - 16px);padding:18px;border:1px solid rgba(8,122,85,.25);border-top:5px solid #0c9564;border-radius:20px;background:#fbfffd;color:#12352d;box-shadow:0 18px 48px rgba(3,71,50,.28)}.SiteBrushCopySiteClose{color:#527168}.SiteBrushCopySiteField{color:#315e53}.SiteBrushCopySiteInput,.SiteBrushCopySiteSelect,.SiteBrushPublicTrialForm input{min-height:48px;border:2px solid #9ed8df;border-radius:12px;background:#e8f7fa;color:#12352d;font-size:16px}.SiteBrushCopySiteInput::placeholder,.SiteBrushPublicTrialForm input::placeholder{color:#64827a}.SiteBrushCopySiteInput:focus,.SiteBrushCopySiteSelect:focus,.SiteBrushPublicTrialForm input:focus{border-color:#087a55;outline:0;box-shadow:0 0 0 4px rgba(8,122,85,.2)}.SiteBrushCopySiteButton,.SiteBrushCopySiteContinueButton,.SiteBrushPublicTrialForm button{min-height:48px;border:2px solid #087a55!important;border-radius:12px;background:linear-gradient(135deg,#dff6fb,#bfeaf2)!important;color:#075d42!important;font-size:16px;box-shadow:0 9px 22px rgba(8,122,85,.2)}.SiteBrushCopySiteSecondaryButton{min-height:48px;border:1px solid #75bfcb;border-radius:12px;background:#e8f7fa;color:#076879;font-size:16px}.SiteBrushCopySiteButton:disabled{border-color:#b8cbc6!important;background:#e4ece9!important;color:#71847f!important;opacity:1;box-shadow:none}.SiteBrushCopySiteProgress{height:16px;background:#d9eeea}.SiteBrushCopySiteProgressBar{height:16px;background:linear-gradient(90deg,#087a55,#34c184);color:#fff;line-height:16px}.SiteBrushCopySiteStatus{color:#12352d}.SiteBrushCopySiteStatus.is-success{color:#087a55}.SiteBrushCopySiteStatus.is-warning{color:#a65200}.SiteBrushCopySiteStatus.is-error{color:#a92731}.SiteBrushCopySiteURL,.SiteBrushCopySiteResourceMeta{color:#527168}.SiteBrushCopySiteQuota,.SiteBrushCopySiteResources{border-color:rgba(8,122,85,.24);background:#fff}.SiteBrushCopySiteResource{border-color:rgba(8,122,85,.15)}.SiteBrushCopySiteResourceKind{background:#e8f7fa;color:#076879}.SiteBrushCopySiteResourceReason{color:#a92731}.SiteBrushCopySiteQuotaStatus.is-ok{color:#087a55}.SiteBrushCopySiteQuotaStatus.is-error{color:#a65200}.SiteBrushPublicTrialForm{border:1px solid rgba(8,122,85,.24);border-top:5px solid #0c9564;background:#fbfffd;color:#12352d;box-shadow:0 16px 42px rgba(3,71,50,.18)}.SiteBrushPublicTrialForm p{color:#0a4437}.SiteBrushPublicTrialForm label{color:#527168}}',
      '@media (max-width:640px) and (prefers-color-scheme:dark){.SiteBrushCopySiteOverlay{background:linear-gradient(145deg,rgba(3,45,36,.94),rgba(7,83,61,.92))}.SiteBrushCopySiteDialog,.SiteBrushPublicTrialForm{border-color:rgba(140,232,241,.24);border-top-color:#78efae;background:#0b3b31;color:#f2fff9;box-shadow:0 20px 54px rgba(0,18,14,.52)}.SiteBrushCopySiteTitle,.SiteBrushPublicTrialForm p{color:#f2fff9}.SiteBrushCopySiteClose,.SiteBrushCopySiteField,.SiteBrushPublicTrialForm label,.SiteBrushCopySiteURL,.SiteBrushCopySiteResourceMeta{color:#b9d8cf}.SiteBrushCopySiteInput,.SiteBrushCopySiteSelect,.SiteBrushPublicTrialForm input{border-color:#4b9ead;background:#123f46;color:#f2fff9}.SiteBrushCopySiteInput::placeholder,.SiteBrushPublicTrialForm input::placeholder{color:#a7cbc3}.SiteBrushCopySiteInput:focus,.SiteBrushCopySiteSelect:focus,.SiteBrushPublicTrialForm input:focus{border-color:#78efae;background:#164b50;box-shadow:0 0 0 4px rgba(120,239,174,.2)}.SiteBrushCopySiteButton,.SiteBrushCopySiteContinueButton,.SiteBrushPublicTrialForm button{border-color:#78efae!important;background:linear-gradient(135deg,#bcecf4,#8fd9e6)!important;color:#043f2d!important}.SiteBrushCopySiteSecondaryButton{border-color:#4b9ead;background:#123f46;color:#b9f4fa}.SiteBrushCopySiteProgress{background:#173f37}.SiteBrushCopySiteQuota,.SiteBrushCopySiteResources{border-color:rgba(140,232,241,.24);background:#103f35}.SiteBrushCopySiteResource{border-color:rgba(140,232,241,.16)}.SiteBrushCopySiteResourceKind{background:#123f46;color:#b9f4fa}.SiteBrushCopySiteStatus{color:#f2fff9}.SiteBrushCopySiteStatus.is-success{color:#78efae}.SiteBrushCopySiteStatus.is-warning{color:#ffb65a}.SiteBrushCopySiteStatus.is-error,.SiteBrushCopySiteResourceReason{color:#ff8f96}.SiteBrushCopySiteQuotaStatus.is-ok{color:#78efae}.SiteBrushCopySiteQuotaStatus.is-error{color:#ffb65a}}',
      '@media (max-width:640px){.SiteBrushCopySiteOverlay{background:#c7c8bf}.SiteBrushCopySiteDialog,.SiteBrushPublicTrialForm,.SiteBrushCopySiteQuota,.SiteBrushCopySiteResources{border-color:rgba(31,54,45,.24);border-top-color:#1f362d;background:#f9faf9;color:#12151d}.SiteBrushCopySiteTitle,.SiteBrushPublicTrialForm p{color:#bf3a20}.SiteBrushCopySiteClose,.SiteBrushCopySiteField,.SiteBrushPublicTrialForm label,.SiteBrushCopySiteURL,.SiteBrushCopySiteResourceMeta,.SiteBrushCopySiteStatus{color:#12151d}.SiteBrushCopySiteButton,.SiteBrushCopySiteContinueButton,.SiteBrushPublicTrialForm button{border-color:#1f362d!important;background:#1f362d!important;color:#fff!important;box-shadow:none}.SiteBrushCopySiteSecondaryButton{border-color:#9eb6e3;background:#9eb6e3;color:#fff}.SiteBrushCopySiteInput,.SiteBrushCopySiteSelect,.SiteBrushPublicTrialForm input{border-color:#9eb6e3;background:#9eb6e3;color:#12151d;box-shadow:none}.SiteBrushCopySiteInput::placeholder,.SiteBrushPublicTrialForm input::placeholder{color:#384252}.SiteBrushCopySiteInput:focus,.SiteBrushCopySiteSelect:focus,.SiteBrushPublicTrialForm input:focus{border-color:#1f362d;background:#9eb6e3;box-shadow:0 0 0 4px rgba(31,54,45,.22)}.SiteBrushCopySiteResourceKind{background:#1f362d;color:#dac0a1}}'
    ].join('');
    styleElement.textContent += '@media (max-width:640px){.SiteBrushCopySiteOverlay{background:#1e362d}.SiteBrushCopySiteDialog,.SiteBrushPublicTrialForm,.SiteBrushCopySiteQuota,.SiteBrushCopySiteResources{border-color:rgba(200,67,35,.34);background:#f9faf9;color:#11141c}.SiteBrushCopySiteTitle,.SiteBrushPublicTrialForm p{color:#c84323}.SiteBrushCopySiteClose,.SiteBrushCopySiteField,.SiteBrushPublicTrialForm label,.SiteBrushCopySiteURL,.SiteBrushCopySiteResourceMeta,.SiteBrushCopySiteStatus{color:#11141c}.SiteBrushCopySiteInput,.SiteBrushCopySiteSelect,.SiteBrushPublicTrialForm input{border-color:#9eb6e3;background:#9eb6e3;color:#11141c}.SiteBrushCopySiteInput::placeholder,.SiteBrushPublicTrialForm input::placeholder{color:#fff}.SiteBrushCopySiteInput:focus,.SiteBrushCopySiteSelect:focus,.SiteBrushPublicTrialForm input:focus{border-color:#c84323;background:#9eb6e3;box-shadow:0 0 0 4px rgba(200,67,35,.2)}}';
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

  function configuredEndpoint(configuration, queryName, extraParameters) {
    const endpointURL = new URL(String(configuration && configuration.endpoint ? configuration.endpoint : window.location.href), window.location.href);
    endpointURL.search = '';
    endpointURL.searchParams.set(queryName, '');
    for (const parameterName of Object.keys(extraParameters || {})) {
      endpointURL.searchParams.set(parameterName, String(extraParameters[parameterName]));
    }
    return endpointURL.toString();
  }

  function configuredWebSocketEndpoint(configuration, queryName, extraParameters) {
    const endpointURL = new URL(configuredEndpoint(configuration, queryName, extraParameters));
    endpointURL.protocol = endpointURL.protocol === 'https:' ? 'wss:' : 'ws:';
    return endpointURL.toString();
  }

  function configuredAssetURL(configuration, assetPath) {
    if (!configuration || !configuration.endpoint) {
      return assetPath;
    }
    return new URL(assetPath, configuration.endpoint).toString();
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

  function setCopySiteStatus(statusElement, statusText, statusKind) {
    statusElement.textContent = statusText;
    statusElement.classList.toggle('is-success', statusKind === 'success');
    statusElement.classList.toggle('is-warning', statusKind === 'warning');
    statusElement.classList.toggle('is-error', statusKind === 'error');
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
    const previewQuery = String(configuration && configuration.previewQuery ? configuration.previewQuery : 'grab_preview');
    const downloadQuery = String(configuration && configuration.downloadQuery ? configuration.downloadQuery : 'grab');
    const retryQuery = String(configuration && configuration.retryQuery ? configuration.retryQuery : 'grab_retry');
    const cancelQuery = String(configuration && configuration.cancelQuery ? configuration.cancelQuery : 'grab_cancel');
    const webSocketQuery = String(configuration && configuration.webSocketQuery ? configuration.webSocketQuery : 'grab_ws');
    const publicTrialMode = Boolean(configuration && configuration.publicTrial);
    const overlayElement = createElement('div', 'SiteBrushCopySiteOverlay');
    const dialogElement = createElement('div', 'SiteBrushCopySiteDialog');
    const headerElement = createElement('div', 'SiteBrushCopySiteHeader');
    const titleElement = createElement('h2', 'SiteBrushCopySiteTitle');
    const titleIconElement = document.createElement('img');
    titleIconElement.src = configuredAssetURL(configuration, '/p/static/copy.png');
    titleIconElement.alt = '';
    titleElement.appendChild(titleIconElement);
    titleElement.appendChild(document.createTextNode(textFromConfig(configuration, 'title', 'Copy site')));
    const closeButtonElement = createElement('button', 'SiteBrushCopySiteClose', '×');
    closeButtonElement.type = 'button';
    headerElement.appendChild(titleElement);
    headerElement.appendChild(closeButtonElement);

    const formElement = createElement('form', 'SiteBrushCopySiteForm');
    appendHiddenField(formElement, 'path', targetPath);
    if (publicTrialMode) {
      appendHiddenField(formElement, 'unified_copy', '1');
    }
    const tokenFieldElement = document.createElement('input');
    tokenFieldElement.type = 'hidden';
    tokenFieldElement.name = 'progress_token';
    formElement.appendChild(tokenFieldElement);
    const previewTokenFieldElement = document.createElement('input');
    previewTokenFieldElement.type = 'hidden';
    previewTokenFieldElement.name = 'preview_token';
    formElement.appendChild(previewTokenFieldElement);

    const primaryRowElement = createElement('div', 'SiteBrushCopySitePrimaryRow');
    const sourceUrlElement = createElement('input', 'SiteBrushCopySiteInput');
    sourceUrlElement.name = 'source_url';
    sourceUrlElement.type = 'text';
    sourceUrlElement.inputMode = 'url';
    sourceUrlElement.required = true;
    sourceUrlElement.placeholder = textFromConfig(configuration, 'sourceURLPlaceholder', 'https://example.com/');
    sourceUrlElement.value = String(configuration && configuration.sourceURL ? configuration.sourceURL : '');
    const submitButtonElement = createElement('button', 'SiteBrushCopySiteButton');
    submitButtonElement.type = 'submit';
    const submitIconElement = document.createElement('img');
    submitIconElement.src = configuredAssetURL(configuration, '/p/static/copy.png');
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
    const sourceLanguageElement = buildLanguageSelect(configuration);
    languageFieldElement.appendChild(sourceLanguageElement);
    secondaryGridElement.appendChild(sourceIPFieldElement);
    secondaryGridElement.appendChild(languageFieldElement);

    const wholeSiteLabelElement = createElement('label', 'SiteBrushCopySiteCheckbox');
    const wholeSiteElement = document.createElement('input');
    wholeSiteElement.type = 'checkbox';
    wholeSiteElement.name = 'copy_whole_site';
    wholeSiteElement.value = '1';
    wholeSiteElement.checked = Boolean(configuration && configuration.copyWholeSite);
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

    let progressWebSocket = null;
    let previewPayload = null;
    let downloadFinishedWithErrors = false;
    let failedResourceURLs = new Set();
    let failedResourceReasons = new Map();
    let importedRedirectPath = '';
    let downloadProgressModel = null;
    let retryWasAttempted = false;
    let partialImportCanRetry = false;
    let requestIsRunning = false;
    let progressWebSocketClosedIntentionally = false;
    let progressWebSocketFailureHandled = false;
    let progressReadyFallbackTimer = 0;
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
      if (requestIsRunning && activeGrabToken !== '') {
        const cancelRequestBody = new URLSearchParams();
        cancelRequestBody.set('progress_token', activeGrabToken);
        fetch(configuredEndpoint(configuration, cancelQuery), { method: 'POST', body: cancelRequestBody, keepalive: true }).catch(function ignoreCloseCancelError() {});
      }
      closeProgressWebSocket();
      stopRetryCountdown();
      overlayElement.remove();
    }

    function closeProgressWebSocket() {
      if (progressReadyFallbackTimer) {
        window.clearTimeout(progressReadyFallbackTimer);
        progressReadyFallbackTimer = 0;
      }
      if (!progressWebSocket) {
        return;
      }
      progressWebSocketClosedIntentionally = true;
      progressWebSocket.close();
      progressWebSocket = null;
    }

    function finishPartialImport() {
      closeProgressWebSocket();
      stopRetryCountdown();
      window.location.href = importedRedirectPath || targetPath || '/';
    }

    function requestActiveDownloadCancel() {
      if (activeGrabToken === '' || downloadCancelRequested) {
        return;
      }
      downloadCancelRequested = true;
      cancelButtonElement.disabled = true;
      setCopySiteStatus(statusElement, textFromConfig(configuration, 'partialImportClose', 'Finishing import...'), 'warning');
      const cancelRequestBody = new URLSearchParams();
      cancelRequestBody.append('progress_token', activeGrabToken);
      fetch(configuredEndpoint(configuration, cancelQuery), { method: 'POST', body: cancelRequestBody, headers: { Accept: 'application/json' } })
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
      setCopySiteStatus(statusElement, retryStatusText(progressPayload, secondsLeft), 'warning');
      if (secondsLeft <= 0) {
        return;
      }
      retryCountdownTimer = window.setInterval(function renderRetryCountdown() {
        secondsLeft -= 1;
        setCopySiteStatus(statusElement, retryStatusText(progressPayload, Math.max(secondsLeft, 0)), 'warning');
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
      setCopySiteStatus(statusElement, textFromConfig(configuration, retryWasAttempted ? 'partialImportClose' : 'partialImportRetry', 'Imported with errors.'), 'error');
      setProgress(progressBarElement, 100);
      closeProgressWebSocket();
      renderFailedResources();
      return true;
    }

    function connectProgressWebSocket(progressToken, readyCallback, forDownload) {
      let readyCallbackWasCalled = false;
      function startRequestOnce() {
        if (readyCallbackWasCalled) {
          return;
        }
        readyCallbackWasCalled = true;
        if (progressReadyFallbackTimer) {
          window.clearTimeout(progressReadyFallbackTimer);
          progressReadyFallbackTimer = 0;
        }
        readyCallback();
      }
      progressWebSocketClosedIntentionally = false;
      progressWebSocketFailureHandled = false;
      progressReadyFallbackTimer = window.setTimeout(startRequestOnce, 1000);
      if (typeof WebSocket !== 'function') {
        startRequestOnce();
        return;
      }
      try {
        progressWebSocket = new WebSocket(configuredWebSocketEndpoint(configuration, webSocketQuery, { token: progressToken }));
      } catch (connectionError) {
        startRequestOnce();
        return;
      }
      progressWebSocket.onmessage = function onProgressMessage(messageEvent) {
        let progressPayload = null;
        try {
          progressPayload = JSON.parse(messageEvent.data);
        } catch (parseError) {
          setCopySiteStatus(statusElement, textFromConfig(configuration, 'invalidStatusError', 'Invalid status.'), 'error');
          closeProgressWebSocket();
          return;
        }
        if (progressPayload.stage === 'ready') {
          startRequestOnce();
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
          setCopySiteStatus(statusElement, retryStatusText(progressPayload, 0), 'warning');
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
        setCopySiteStatus(statusElement, textFromConfig(configuration, 'progressDownloadedPrefix', 'Downloaded') + ' ' + downloadedTotal + ' ' + textFromConfig(configuration, 'fromWord', 'of') + ' ' + foundTotal + ' (' + completedPercent + '%). ' + textFromConfig(configuration, 'leftWord', 'Left') + ' ' + remainingTotal + ' (' + remainingPercent + '%).', '');
        urlElement.textContent = progressPayload.current_url || '';
        if (progressPayload.stage === 'done') {
          stopRetryCountdown();
          setCopySiteStatus(statusElement, forDownload ? textFromConfig(configuration, 'doneOpenPage', 'Done. Opening page...') : textFromConfig(configuration, 'previewResourcesText', 'Checking resources...'), forDownload ? 'success' : '');
          setProgress(progressBarElement, 100);
          closeProgressWebSocket();
        }
        if (progressPayload.stage === 'partial') {
          stopRetryCountdown();
          downloadFinishedWithErrors = true;
          setCopySiteStatus(statusElement, progressPayload.message || textFromConfig(configuration, retryWasAttempted ? 'partialImportClose' : 'partialImportRetry', 'Imported with errors.'), 'error');
          setProgress(progressBarElement, 100);
          renderFailedResources();
          closeProgressWebSocket();
        }
        if (progressPayload.stage === 'error' && !progressPayload.current_url) {
          setCopySiteStatus(statusElement, textFromConfig(configuration, 'downloadFailedRetry', 'Download failed.'), 'error');
          closeProgressWebSocket();
        }
      };
      function handleProgressConnectionFailure() {
        if (progressWebSocketClosedIntentionally || progressWebSocketFailureHandled) {
          return;
        }
        progressWebSocketFailureHandled = true;
        if (!readyCallbackWasCalled) {
          closeProgressWebSocket();
          setCopySiteStatus(statusElement, textFromConfig(configuration, 'loadingStarted', 'Loading started...'), '');
          startRequestOnce();
          return;
        }
        if (requestIsRunning) {
          setCopySiteStatus(statusElement, textFromConfig(configuration, 'connectionLostStatus', 'Connection lost. Waiting for the operation to finish...'), 'warning');
        }
      }
      progressWebSocket.onerror = handleProgressConnectionFailure;
      progressWebSocket.onclose = handleProgressConnectionFailure;
    }

    function showPreview(previewResponsePayload) {
      previewPayload = previewResponsePayload;
      resourcesElement.replaceChildren();
      const resourceCount = Number(previewPayload.resource_count) || 0;
      const pageCount = wholeSiteElement.checked ? Math.max(1, Number(previewPayload.page_count) || 1) : 0;
      const targetCount = pageCount + resourceCount;
      const singlePageRequired = Boolean(previewPayload.single_page_required);
      setCopySiteStatus(statusElement, singlePageRequired
        ? (previewPayload.message || textFromConfig(configuration, 'singlePageRequired', 'The website is larger than the free test-drive limit. Stop scanning and copy only its first page.'))
        : textFromConfig(configuration, 'confirmDownloadTextPrefix', 'Ready to download') + ' ' + targetCount + '.', singlePageRequired ? 'warning' : 'success');
      urlElement.textContent = previewPayload.source_url || sourceUrlElement.value;
      setFinishImportButtonMode(false);
      partialImportCanRetry = false;
      setContinueButtonPrimaryAction(true);
      continueButtonElement.textContent = singlePageRequired
        ? textFromConfig(configuration, 'copyFirstPage', 'Copy first page')
        : textFromConfig(configuration, 'continueButton', 'Continue');
      continueButtonElement.classList.remove('SiteBrushCopySiteHidden');
      progressElement.classList.add('SiteBrushCopySiteHidden');
      if (resourceCount > 0) {
        resourcesElement.classList.remove('SiteBrushCopySiteHidden');
        for (const previewResource of previewPayload.resources || []) {
          appendPreviewResource(resourcesElement, previewResource, configuration, function onResourceSelectionChange() {
            if (!previewPayload.single_page_required) {
              renderQuotaSummary(quotaElement, resourcesElement, previewPayload, configuration, continueButtonElement);
            }
          });
        }
      } else {
        resourcesElement.classList.add('SiteBrushCopySiteHidden');
      }
      if (singlePageRequired) {
        quotaElement.classList.add('SiteBrushCopySiteHidden');
        continueButtonElement.disabled = false;
      } else {
        renderQuotaSummary(quotaElement, resourcesElement, previewPayload, configuration, continueButtonElement);
      }
    }

    function invalidatePreview() {
      if (!previewPayload) {
        return;
      }
      previewPayload = null;
      previewTokenFieldElement.value = '';
      resourcesElement.replaceChildren();
      resourcesElement.classList.add('SiteBrushCopySiteHidden');
      quotaElement.classList.add('SiteBrushCopySiteHidden');
      continueButtonElement.classList.add('SiteBrushCopySiteHidden');
      setCopySiteStatus(statusElement, '', '');
      urlElement.textContent = '';
    }

    function requestPreview() {
      if (!formElement.reportValidity()) {
        return;
      }
      const progressToken = randomProgressToken();
      tokenFieldElement.value = progressToken;
      previewTokenFieldElement.value = '';
      activeGrabToken = progressToken;
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
      setCopySiteStatus(statusElement, textFromConfig(configuration, 'previewResourcesText', 'Checking resources...'), '');
      urlElement.textContent = sourceUrlElement.value;
      submitButtonElement.disabled = true;
      connectProgressWebSocket(progressToken, function submitPreviewRequest() {
        requestIsRunning = true;
        fetch(configuredEndpoint(configuration, previewQuery), { method: 'POST', body: formRequestBody(formElement), headers: { Accept: 'application/json' } })
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
            activeGrabToken = '';
            submitButtonElement.disabled = false;
            closeProgressWebSocket();
            previewTokenFieldElement.value = progressToken;
            showPreview(previewResponsePayload);
          })
          .catch(function handlePreviewError(previewError) {
            requestIsRunning = false;
            activeGrabToken = '';
            submitButtonElement.disabled = false;
            closeProgressWebSocket();
            setCopySiteStatus(statusElement, previewError.message || textFromConfig(configuration, 'previewErrorDefault', 'Preview failed.'), 'error');
          });
      }, false);
    }

    function startDownload() {
      if (!previewPayload) {
        requestPreview();
        return;
      }
      if (previewPayload.single_page_required) {
        wholeSiteElement.checked = false;
        invalidatePreview();
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
        activeDownloadEndpoint = retryQuery;
        syncFailedResources(formElement, failedResourceURLs);
        downloadProgressModel = buildRetryProgressModel(failedResourceURLs);
      } else {
        retryWasAttempted = false;
        activeDownloadEndpoint = downloadQuery;
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
      setCopySiteStatus(statusElement, textFromConfig(configuration, 'loadingStarted', 'Loading started...'), '');
      urlElement.textContent = '';
      connectProgressWebSocket(progressToken, function submitDownloadRequest() {
        requestIsRunning = true;
        const downloadBody = formRequestBody(formElement);
        if (publicTrialMode) {
          downloadBody.set('create_progress_token', progressToken);
        }
        fetch(configuredEndpoint(configuration, activeDownloadEndpoint), { method: 'POST', body: downloadBody, headers: { Accept: 'application/json' } })
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
            if (!publicTrialMode && applyDownloadFailureState(downloadPayload)) {
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
            setCopySiteStatus(statusElement, textFromConfig(configuration, 'doneOpenPage', 'Done. Opening page...'), 'success');
            closeProgressWebSocket();
            window.location.href = redirectPath;
          })
          .catch(function handleDownloadError(downloadError) {
            requestIsRunning = false;
            activeGrabToken = '';
            submitButtonElement.disabled = false;
            cancelButtonElement.disabled = false;
            setFinishImportButtonMode(false);
            closeProgressWebSocket();
            setCopySiteStatus(statusElement, downloadError.message || textFromConfig(configuration, 'loadPageFailed', 'Load failed.'), 'error');
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
    sourceUrlElement.addEventListener('input', invalidatePreview);
    sourceIPElement.addEventListener('input', invalidatePreview);
    sourceLanguageElement.addEventListener('change', invalidatePreview);
    wholeSiteElement.addEventListener('change', invalidatePreview);
    formElement.addEventListener('submit', function onCopyFormSubmit(submitEvent) {
      submitEvent.preventDefault();
      clearSelectionFields(formElement);
      requestPreview();
    });
    sourceUrlElement.focus();
    if (sourceUrlElement.value !== '') {
      sourceUrlElement.select();
    }
  }

  function publicTrialText(configuration, textName, fallbackText) {
    return textFromConfig(configuration, textName, fallbackText);
  }

  function publicTrialFetchURL(endpointPath, queryName) {
    const requestURL = new URL(endpointPath || '/', window.location.href);
    requestURL.search = '';
    requestURL.searchParams.set(queryName, '');
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
        const sourceURL = String(new FormData(formElement).get('source_url') || '');
        const unifiedConfiguration = Object.assign({}, publicTrialConfiguration, {
          publicTrial: true,
          copyWholeSite: false,
          sourceURL: sourceURL,
          path: '/',
          previewQuery: 'trial_site_preview',
          downloadQuery: 'trial_site_create',
          retryQuery: 'trial_site_create',
          cancelQuery: 'trial_site_preview_cancel',
          webSocketQuery: 'trial_site_ws'
        });
        openCopySiteModal(unifiedConfiguration);
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
