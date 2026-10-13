/* One UI participant owns these drafts. Async upload completions carry the
   recipient of their dialog, so a closed session cannot edit a later one. */
const sitebrushEditorModel = {
  document: null,
  previewNodes: [],
  previewAttributes: [],
  mode: 'visual',
  sourceHTML: '',
  sourceSnapshot: '',
  renderedBodyHTML: '',
  visualButtons: [],
  imageDraft: null,
  lastImageTap: null,
  savingPage: false,
  saveError: ''
};

function visualEditorLanguageCode(languageCode) {
  if (languageCode === 'zh') {
    return 'zh_cn';
  }
  if (languageCode === 'pt') {
    return 'pt_br';
  }
  return languageCode;
}

function renderVisualEditorSaveStatus(editorInstance) {
  const saveStatusElement = document.getElementById('editorSaveStatus');
  saveStatusElement.hidden = !sitebrushEditorModel.savingPage && !sitebrushEditorModel.saveError;
  saveStatusElement.textContent = sitebrushEditorModel.savingPage ? editUIText.saving : sitebrushEditorModel.saveError;
  editorInstance.setReadOnly(sitebrushEditorModel.savingPage);
  document.getElementById('sitebrushSourceHTML').readOnly = sitebrushEditorModel.savingPage;
}

function currentVisualEditorDocumentHTML(editorBodyHTML) {
  if (editorBodyHTML === sitebrushEditorModel.renderedBodyHTML) {
    return visualEditorDocumentModel.originalHTML;
  }
  return visualEditorDocumentModel.envelope.prefix + restoreVisualEditorPreviewBody(editorBodyHTML) + visualEditorDocumentModel.envelope.suffix;
}

/* Template contents stay inert while parsing. Only the visual projection loses
   executable content; source mode and save restore the document-owned originals. */
function createVisualEditorPreviewBody(bodyHTML) {
  sitebrushEditorModel.previewNodes = [];
  sitebrushEditorModel.previewAttributes = [];
  const bodyTemplate = document.createElement('template');
  bodyTemplate.innerHTML = bodyHTML;
  for (const scriptElement of bodyTemplate.content.querySelectorAll('script')) {
    const scriptIndex = sitebrushEditorModel.previewNodes.length;
    sitebrushEditorModel.previewNodes.push(scriptElement.outerHTML);
    const scriptPlaceholder = document.createElement('div');
    scriptPlaceholder.setAttribute('data-sitebrush-preview-node', String(scriptIndex));
    scriptPlaceholder.setAttribute('contenteditable', 'false');
    scriptPlaceholder.style.setProperty('display', 'none', 'important');
    scriptPlaceholder.textContent = 'SiteBrush script placeholder';
    scriptElement.replaceWith(scriptPlaceholder);
  }
  for (const previewElement of bodyTemplate.content.querySelectorAll('*')) {
    const executableAttributes = [];
    for (const previewAttribute of Array.from(previewElement.attributes)) {
      const attributeName = previewAttribute.name.toLowerCase();
      const embeddedDocumentSource = ['iframe', 'object', 'embed'].includes(previewElement.localName) && ['src', 'srcdoc', 'data'].includes(attributeName);
      if (attributeName.startsWith('on') || embeddedDocumentSource || /^\s*javascript:/i.test(previewAttribute.value)) {
        executableAttributes.push({ name: previewAttribute.name, text: previewAttribute.value });
        previewElement.removeAttribute(previewAttribute.name);
      }
    }
    if (executableAttributes.length > 0) {
      const attributeIndex = sitebrushEditorModel.previewAttributes.length;
      sitebrushEditorModel.previewAttributes.push(executableAttributes);
      previewElement.setAttribute('data-sitebrush-preview-attributes', String(attributeIndex));
    }
  }
  return bodyTemplate.innerHTML;
}

function restoreVisualEditorPreviewBody(previewBodyHTML) {
  const bodyTemplate = document.createElement('template');
  bodyTemplate.innerHTML = previewBodyHTML;
  for (const previewElement of bodyTemplate.content.querySelectorAll('[data-sitebrush-preview-attributes]')) {
    const attributeIndex = Number(previewElement.getAttribute('data-sitebrush-preview-attributes'));
    const originalAttributes = sitebrushEditorModel.previewAttributes[attributeIndex];
    if (originalAttributes) {
      for (const originalAttribute of originalAttributes) {
        previewElement.setAttribute(originalAttribute.name, originalAttribute.text);
      }
      previewElement.removeAttribute('data-sitebrush-preview-attributes');
    }
  }
  for (const scriptPlaceholder of bodyTemplate.content.querySelectorAll('[data-sitebrush-preview-node]')) {
    const scriptIndex = Number(scriptPlaceholder.getAttribute('data-sitebrush-preview-node'));
    const originalScriptHTML = sitebrushEditorModel.previewNodes[scriptIndex];
    if (originalScriptHTML !== undefined) {
      const scriptTemplate = document.createElement('template');
      scriptTemplate.innerHTML = originalScriptHTML;
      scriptPlaceholder.replaceWith(scriptTemplate.content);
    }
  }
  return bodyTemplate.innerHTML;
}

function initializeSitebrushEditorExtensions(editorInstance) {
  protectVisualEditorFrameScripts(editorInstance);
  sitebrushEditorModel.renderedBodyHTML = editorInstance.value;
  sitebrushEditorModel.visualButtons = editorInstance.o.buttons;
  const sourceContainer = document.createElement('div');
  sourceContainer.className = 'jodit-source';
  const sourceTextarea = document.createElement('textarea');
  sourceTextarea.className = 'jodit-source__mirror';
  sourceTextarea.id = 'sitebrushSourceHTML';
  sourceTextarea.spellcheck = false;
  sourceTextarea.setAttribute('aria-label', editUIText.htmlCode);
  sourceContainer.appendChild(sourceTextarea);
  editorInstance.workplace.appendChild(sourceContainer);
  sourceTextarea.addEventListener('input', function updateSourceDraft() {
    sitebrushEditorModel.sourceHTML = sourceTextarea.value;
  });
  sourceTextarea.addEventListener('keydown', function saveSourceWithHotkey(keyboardEvent) {
    if ((keyboardEvent.ctrlKey || keyboardEvent.metaKey) && keyboardEvent.key.toLowerCase() === 's') {
      keyboardEvent.preventDefault();
      submitVisualEditorForm(editorInstance);
    }
  });
  editorInstance.events.on('afterSetMode', function renderChangedEditorMode() {
    renderSitebrushEditorToolbar(editorInstance);
  });
  connectImagePropertiesEvents(editorInstance);
  connectImagePropertiesDialog(editorInstance);
  const previewResizeObserver = new ResizeObserver(function resizeImagePreview() {
    renderImagePropertiesPreview();
  });
  previewResizeObserver.observe(document.getElementById('imagePropertiesPreviewStage'));
  window.addEventListener('resize', syncImageDialogViewport);
  if (window.visualViewport) {
    window.visualViewport.addEventListener('resize', syncImageDialogViewport);
    window.visualViewport.addEventListener('scroll', syncImageDialogViewport);
  }
  refreshVisualEditorDocumentStyles(editorInstance);
  document.addEventListener('keydown', function dismissEditorDialog(keyboardEvent) {
    if (keyboardEvent.key === 'Escape') {
      if (document.getElementById('uploadDialogOverlay').classList.contains('is-open')) {
        closeUploadDialog();
      } else {
        closeImagePropertiesDialog();
      }
    }
  });
}

/* Jodit's hover/selection callbacks need scripting enabled in their document.
   CSP blocks scripts supplied by page HTML while allowing parent-owned editor
   functions; the visual projection also excludes page handlers and scripts. */
function protectVisualEditorFrameScripts(editorInstance) {
  const frameDocument = editorInstance.iframe.contentDocument;
  const scriptPolicy = frameDocument.createElement('meta');
  scriptPolicy.httpEquiv = 'Content-Security-Policy';
  scriptPolicy.content = "script-src 'none'; frame-src 'none'; object-src 'none'";
  frameDocument.head.prepend(scriptPolicy);
}

function enterSitebrushCodeMode(editorInstance) {
  if (sitebrushEditorModel.mode === 'code' || sitebrushEditorModel.savingPage) {
    return;
  }
  sitebrushEditorModel.sourceHTML = currentVisualEditorDocumentHTML(editorInstance.value);
  sitebrushEditorModel.sourceSnapshot = sitebrushEditorModel.sourceHTML;
  sitebrushEditorModel.mode = 'code';
  const sourceTextarea = document.getElementById('sitebrushSourceHTML');
  sourceTextarea.value = sitebrushEditorModel.sourceHTML;
  editorInstance.setMode(Jodit.MODE_SOURCE);
  sourceTextarea.focus({ preventScroll: true });
  sourceTextarea.setSelectionRange(0, 0);
  sourceTextarea.scrollTop = 0;
  sourceTextarea.scrollLeft = 0;
}

function returnToSitebrushVisualMode(editorInstance, discardSourceChanges) {
  if (sitebrushEditorModel.savingPage) {
    return;
  }
  const nextDocumentHTML = discardSourceChanges ? sitebrushEditorModel.sourceSnapshot : sitebrushEditorModel.sourceHTML;
  const documentChanged = nextDocumentHTML !== sitebrushEditorModel.sourceSnapshot;
  sitebrushEditorModel.mode = 'visual';
  editorInstance.setMode(Jodit.MODE_WYSIWYG);
  if (documentChanged) {
    const previousAttributeLists = [visualEditorDocumentModel.htmlAttributeList, visualEditorDocumentModel.bodyAttributeList];
    Object.assign(visualEditorDocumentModel, parseVisualEditorDocument(nextDocumentHTML));
    const nextAttributeLists = [visualEditorDocumentModel.htmlAttributeList, visualEditorDocumentModel.bodyAttributeList];
    const frameElements = [editorInstance.iframe.contentDocument.documentElement, editorInstance.iframe.contentDocument.body];
    for (let elementIndex = 0; elementIndex < frameElements.length; elementIndex++) {
      for (const previousAttribute of previousAttributeLists[elementIndex]) {
        if (!nextAttributeLists[elementIndex].some(function attributeStillPresent(nextAttribute) { return nextAttribute.name === previousAttribute.name; })) {
          frameElements[elementIndex].removeAttribute(previousAttribute.name);
        }
      }
    }
    editorInstance.value = createVisualEditorPreviewBody(visualEditorDocumentModel.bodyHTML);
    sitebrushEditorModel.renderedBodyHTML = editorInstance.value;
    refreshVisualEditorDocumentStyles(editorInstance);
    syncVisualEditorFrameTheme(editorInstance, currentVisualEditorColorScheme());
  }
  scheduleVisualEditorLayout(editorInstance);
}

function renderSitebrushEditorToolbar(editorInstance) {
  const codeButtons = [
    { name: 'cancelSource', mode: Jodit.MODE_SPLIT, tooltip: editUIText.cancel, text: editUIText.cancel,
      exec: function cancelSourceDraft() { returnToSitebrushVisualMode(editorInstance, true); } },
    { name: 'saveSource', mode: Jodit.MODE_SPLIT, tooltip: editUIText.save, text: editUIText.save,
      exec: function saveSourceDraft() { submitVisualEditorForm(editorInstance); } },
    { name: 'backToVisual', mode: Jodit.MODE_SPLIT, tooltip: editUIText.backToVisual, text: editUIText.backToVisual,
      exec: function applySourceDraft() { returnToSitebrushVisualMode(editorInstance, false); } }
  ];
  editorInstance.container.classList.toggle('SiteBrushCodeMode', sitebrushEditorModel.mode === 'code');
  editorInstance.toolbar.build(sitebrushEditorModel.mode === 'code' ? codeButtons : sitebrushEditorModel.visualButtons);
  scheduleVisualEditorLayout(editorInstance);
}

function refreshVisualEditorDocumentStyles(editorInstance) {
  const frameDocument = editorInstance.iframe.contentDocument;
  for (const stylesheetElement of frameDocument.head.querySelectorAll('link[rel="stylesheet"], style[data-sitebrush-document-style]')) {
    stylesheetElement.remove();
  }
  for (const stylesheetURL of visualEditorStylesheetLinks(visualEditorDocumentModel.originalHTML)) {
    const stylesheetElement = frameDocument.createElement('link');
    stylesheetElement.rel = 'stylesheet';
    stylesheetElement.href = stylesheetURL;
    frameDocument.head.appendChild(stylesheetElement);
  }
  const inlineStyleElement = frameDocument.createElement('style');
  inlineStyleElement.setAttribute('data-sitebrush-document-style', '');
  inlineStyleElement.textContent = visualEditorInlineStyleText(visualEditorDocumentModel.originalHTML);
  frameDocument.head.appendChild(inlineStyleElement);
}

function connectImagePropertiesEvents(editorInstance) {
  editorInstance.events.on('openImageProperties', function openSelectedImageProperties(selectedImage) {
    if (sitebrushEditorModel.mode === 'visual' && selectedImage.tagName === 'IMG') {
      openImagePropertiesDialog(editorInstance, selectedImage);
    }
  });
  editorInstance.events.on('dblclick', function openImageAfterDoubleClick(pointerEvent) {
    if (sitebrushEditorModel.mode === 'visual' && pointerEvent.target.tagName === 'IMG') {
      pointerEvent.preventDefault();
      openImagePropertiesDialog(editorInstance, pointerEvent.target);
      return false;
    }
  });
  editorInstance.events.on('touchend', function openImageAfterDoubleTap(touchEvent) {
    const touchedImage = touchEvent.target;
    if (sitebrushEditorModel.mode !== 'visual' || touchedImage.tagName !== 'IMG' || touchEvent.changedTouches.length !== 1) {
      sitebrushEditorModel.lastImageTap = null;
      return;
    }
    const imageTouch = touchEvent.changedTouches[0];
    const previousTap = sitebrushEditorModel.lastImageTap;
    const tapTime = performance.now();
    if (previousTap && previousTap.image === touchedImage && tapTime - previousTap.time < 350 &&
        Math.hypot(imageTouch.clientX - previousTap.x, imageTouch.clientY - previousTap.y) < 24) {
      sitebrushEditorModel.lastImageTap = null;
      touchEvent.preventDefault();
      openImagePropertiesDialog(editorInstance, touchedImage);
      return false;
    }
    sitebrushEditorModel.lastImageTap = { image: touchedImage, time: tapTime, x: imageTouch.clientX, y: imageTouch.clientY };
  });
}

function openImagePropertiesDialog(editorInstance, imageElement) {
  if (sitebrushEditorModel.imageDraft || sitebrushEditorModel.savingPage) {
    return;
  }
  const imageRectangle = imageElement.getBoundingClientRect();
  const imageStyle = editorInstance.iframe.contentWindow.getComputedStyle(imageElement);
  const fitMode = ['cover', 'contain'].includes(imageStyle.objectFit) || imageElement.style.objectFit === 'fill' ? imageStyle.objectFit : 'cover';
  sitebrushEditorModel.imageDraft = {
    imageElement: imageElement,
    source: imageElement.getAttribute('src') || '',
    originalSource: imageElement.getAttribute('src') || '',
    width: Math.max(1, Math.round(imageRectangle.width)),
    height: Math.max(1, Math.round(imageRectangle.height)),
    areaRatio: imageRectangle.width / imageRectangle.height || 1,
    lockedRatio: true,
    fit: fitMode,
    position: imageStyle.objectPosition || '50% 50%',
    dimensionsChanged: false,
    fitChanged: fitMode !== imageStyle.objectFit,
    positionChanged: false,
    error: '',
    loadingPreview: false,
    fileByteCount: null,
    fileSizeState: 'loading',
    previousFocus: document.activeElement
  };
  renderImagePropertiesDialog();
  syncImageDialogViewport();
  loadImagePropertiesFileSize(sitebrushEditorModel.imageDraft);
  document.getElementById('imagePropertiesClose').focus();
}

function renderImagePropertiesDialog() {
  const imageDraft = sitebrushEditorModel.imageDraft;
  const dialogElement = document.getElementById('imagePropertiesDialog');
  dialogElement.hidden = !imageDraft;
  if (!imageDraft) {
    return;
  }
  document.getElementById('imagePropertiesSource').value = imageDraft.source;
  document.getElementById('imagePropertiesWidth').value = imageDraft.width;
  document.getElementById('imagePropertiesHeight').value = imageDraft.height;
  document.getElementById('imagePropertiesLock').checked = imageDraft.lockedRatio;
  document.getElementById('imagePropertiesFit').value = imageDraft.fit;
  document.getElementById('imagePropertiesPosition').value = imageDraft.position;
  renderImagePropertiesPreview();
  renderImagePropertiesFileSize();
  renderImagePropertiesStatus();
  document.getElementById('imagePropertiesDimensions').textContent = imageDraft.width + ' × ' + imageDraft.height;
}

function syncImageDialogViewport() {
  if (!sitebrushEditorModel.imageDraft) {
    return;
  }
  const viewportHeight = window.visualViewport ? window.visualViewport.height : window.innerHeight;
  const viewportTop = window.visualViewport ? window.visualViewport.offsetTop : 0;
  const dialogElement = document.getElementById('imagePropertiesDialog');
  dialogElement.style.setProperty('--image-viewport-height', viewportHeight + 'px');
  dialogElement.style.setProperty('--image-viewport-top', viewportTop + 'px');
  renderImagePropertiesPreview();
}

function renderImagePropertiesPreview() {
  const imageDraft = sitebrushEditorModel.imageDraft;
  if (!imageDraft) {
    return;
  }
  const previewImage = document.getElementById('imagePropertiesPreview');
  const previewStage = document.getElementById('imagePropertiesPreviewStage');
  let resolvedSource = '';
  try {
    resolvedSource = new URL(imageDraft.source, visualEditorFrameBaseURL()).href;
  } catch (sourceURLError) {
    imageDraft.error = editUIText.imagePreviewFailed;
  }
  if (resolvedSource && previewImage.src !== resolvedSource) {
    imageDraft.loadingPreview = true;
    previewImage.src = resolvedSource;
  }
  const previewScale = Math.min(previewStage.clientWidth / imageDraft.width, previewStage.clientHeight / imageDraft.height);
  if (previewScale > 0) {
    previewImage.style.width = imageDraft.width * previewScale + 'px';
    previewImage.style.height = imageDraft.height * previewScale + 'px';
  }
  previewImage.style.objectFit = imageDraft.fit;
  previewImage.style.objectPosition = imageDraft.position;
  renderImagePropertiesStatus();
}

function imageFileSizeText(fileByteCount) {
  const isMegabytes = fileByteCount >= 1024 * 1024;
  const scaledSize = fileByteCount / (isMegabytes ? 1024 * 1024 : 1024);
  const numberFormat = new Intl.NumberFormat(document.documentElement.lang, { minimumFractionDigits: 1, maximumFractionDigits: 2 });
  return numberFormat.format(scaledSize) + ' ' + (isMegabytes ? editUIText.megabyteUnit : editUIText.kilobyteUnit);
}

function renderImagePropertiesFileSize() {
  const imageDraft = sitebrushEditorModel.imageDraft;
  if (!imageDraft) {
    return;
  }
  const fileSizeElement = document.getElementById('imagePropertiesFileSize');
  if (imageDraft.fileSizeState === 'known') {
    fileSizeElement.textContent = imageFileSizeText(imageDraft.fileByteCount);
  } else {
    fileSizeElement.textContent = imageDraft.fileSizeState === 'loading' ? editUIText.fileSizeLoading : editUIText.fileSizeUnavailable;
  }
}

// HEAD reads existing resource metadata without downloading a second image.
// The dialog identity and source URL define whether its reply still matters.
function loadImagePropertiesFileSize(receivingDraft) {
  const sourceURL = receivingDraft.source;
  receivingDraft.fileSizeState = 'loading';
  renderImagePropertiesFileSize();
  imageResourceByteCount(sourceURL)
    .then(function receiveImageByteCount(fileByteCount) {
      if (sitebrushEditorModel.imageDraft !== receivingDraft || receivingDraft.source !== sourceURL) {
        return;
      }
      receivingDraft.fileByteCount = fileByteCount;
      receivingDraft.fileSizeState = 'known';
      renderImagePropertiesFileSize();
    })
    .catch(function showUnavailableImageSize() {
      if (sitebrushEditorModel.imageDraft !== receivingDraft || receivingDraft.source !== sourceURL) {
        return;
      }
      receivingDraft.fileByteCount = null;
      receivingDraft.fileSizeState = 'unavailable';
      renderImagePropertiesFileSize();
    });
}

async function imageResourceByteCount(sourceURL) {
  const absoluteURL = new URL(sourceURL, visualEditorFrameBaseURL());
  if (absoluteURL.protocol === 'data:') {
    const payloadStart = sourceURL.indexOf(',');
    const encodedPayload = sourceURL.slice(payloadStart + 1);
    if (sourceURL.slice(0, payloadStart).toLowerCase().endsWith(';base64')) {
      const base64Payload = decodeURIComponent(encodedPayload).replace(/\s/g, '');
      const paddingLength = base64Payload.endsWith('==') ? 2 : base64Payload.endsWith('=') ? 1 : 0;
      return Math.floor(base64Payload.length * 3 / 4) - paddingLength;
    }
    return new TextEncoder().encode(decodeURIComponent(encodedPayload)).length;
  }
  const resourceResponse = await fetch(absoluteURL.href, { method: absoluteURL.protocol === 'blob:' ? 'GET' : 'HEAD' });
  if (!resourceResponse.ok) {
    throw new Error(editUIText.fileSizeUnavailable);
  }
  if (absoluteURL.protocol === 'blob:') {
    return (await resourceResponse.blob()).size;
  }
  const contentLength = resourceResponse.headers.get('Content-Length');
  const fileByteCount = contentLength === null ? NaN : Number(contentLength);
  if (!Number.isFinite(fileByteCount) || fileByteCount < 0) {
    throw new Error(editUIText.fileSizeUnavailable);
  }
  return fileByteCount;
}

function renderImagePropertiesStatus() {
  const imageDraft = sitebrushEditorModel.imageDraft;
  if (!imageDraft) {
    return;
  }
  document.getElementById('imagePropertiesError').textContent = imageDraft.error;
  document.getElementById('imagePropertiesSave').disabled = !!imageDraft.error || imageDraft.loadingPreview;
}

function closeImagePropertiesDialog() {
  const closedDraft = sitebrushEditorModel.imageDraft;
  sitebrushEditorModel.imageDraft = null;
  renderImagePropertiesDialog();
  if (closedDraft && closedDraft.previousFocus) {
    closedDraft.previousFocus.focus({ preventScroll: true });
  }
}

function changeImageAreaSize(changedSide, requestedSize) {
  const imageDraft = sitebrushEditorModel.imageDraft;
  if (!imageDraft) {
    return;
  }
  const numericSize = Number(requestedSize);
  if (!Number.isFinite(numericSize) || numericSize <= 0 || numericSize > 12000) {
    imageDraft.error = editUIText.invalidImageSize;
    document.getElementById('imagePropertiesError').textContent = imageDraft.error;
    document.getElementById('imagePropertiesSave').disabled = true;
    return;
  }
  imageDraft.error = '';
  let nextWidth = changedSide === 'width' ? Math.round(numericSize) : imageDraft.width;
  let nextHeight = changedSide === 'height' ? Math.round(numericSize) : imageDraft.height;
  if (imageDraft.lockedRatio) {
    if (changedSide === 'width') {
      nextHeight = Math.max(1, Math.round(nextWidth / imageDraft.areaRatio));
    } else {
      nextWidth = Math.max(1, Math.round(nextHeight * imageDraft.areaRatio));
    }
  }
  if (nextWidth > 12000 || nextHeight > 12000) {
    imageDraft.error = editUIText.invalidImageSize;
    document.getElementById('imagePropertiesError').textContent = imageDraft.error;
    document.getElementById('imagePropertiesSave').disabled = true;
    return;
  }
  imageDraft.width = nextWidth;
  imageDraft.height = nextHeight;
  imageDraft.dimensionsChanged = true;
  imageDraft.fitChanged = true;
  renderImagePropertiesDialog();
}

function applyImagePropertiesDraft(editorInstance) {
  const imageDraft = sitebrushEditorModel.imageDraft;
  if (!imageDraft || imageDraft.error || !imageDraft.imageElement.isConnected) {
    return;
  }
  const selectedImage = imageDraft.imageElement;
  selectedImage.setAttribute('src', imageDraft.source);
  if (imageDraft.source !== imageDraft.originalSource) {
    selectedImage.removeAttribute('srcset');
    selectedImage.removeAttribute('sizes');
  }
  if (imageDraft.dimensionsChanged) {
    for (const dimensionName of ['width', 'height']) {
      if (selectedImage.hasAttribute(dimensionName) && !selectedImage.style[dimensionName]) {
        selectedImage.setAttribute(dimensionName, String(imageDraft[dimensionName]));
      } else {
        selectedImage.style[dimensionName] = imageDraft[dimensionName] + 'px';
      }
    }
  }
  if (imageDraft.fitChanged) {
    selectedImage.style.objectFit = imageDraft.fit;
  }
  if (imageDraft.positionChanged) {
    selectedImage.style.objectPosition = imageDraft.position;
  }
  editorInstance.synchronizeValues();
  closeImagePropertiesDialog();
  submitVisualEditorForm(editorInstance);
}

function connectImagePropertiesDialog(editorInstance) {
  document.getElementById('imagePropertiesDialog').addEventListener('keydown', function containImageDialogFocus(keyboardEvent) {
    if (keyboardEvent.key !== 'Tab') {
      return;
    }
    const dialogControls = Array.from(document.getElementById('imagePropertiesDialog').querySelectorAll('button, input, select')).filter(function availableDialogControl(controlElement) {
      return !controlElement.disabled && controlElement.getClientRects().length > 0;
    });
    const firstControl = dialogControls[0];
    const lastControl = dialogControls[dialogControls.length - 1];
    if (keyboardEvent.shiftKey && document.activeElement === firstControl) {
      keyboardEvent.preventDefault();
      lastControl.focus();
    } else if (!keyboardEvent.shiftKey && document.activeElement === lastControl) {
      keyboardEvent.preventDefault();
      firstControl.focus();
    }
  });
  for (const closeButtonID of ['imagePropertiesClose', 'imagePropertiesCancel']) {
    document.getElementById(closeButtonID).addEventListener('click', closeImagePropertiesDialog);
  }
  document.getElementById('imagePropertiesSave').addEventListener('click', function saveImageProperties() {
    applyImagePropertiesDraft(editorInstance);
  });
  for (const dimensionName of ['width', 'height']) {
    document.getElementById(dimensionName === 'width' ? 'imagePropertiesWidth' : 'imagePropertiesHeight').addEventListener('change', function changeImageDimension(changeEvent) {
      changeImageAreaSize(dimensionName, changeEvent.target.value);
    });
  }
  document.getElementById('imagePropertiesLock').addEventListener('change', function changeAreaRatioLock(changeEvent) {
    const imageDraft = sitebrushEditorModel.imageDraft;
    imageDraft.lockedRatio = changeEvent.target.checked;
    imageDraft.areaRatio = imageDraft.width / imageDraft.height;
  });
  document.getElementById('imagePropertiesFit').addEventListener('change', function changeImageFit(changeEvent) {
    sitebrushEditorModel.imageDraft.fit = changeEvent.target.value;
    sitebrushEditorModel.imageDraft.fitChanged = true;
    renderImagePropertiesDialog();
  });
  document.getElementById('imagePropertiesPosition').addEventListener('change', function changeCropPosition(changeEvent) {
    const positionText = changeEvent.target.value.trim();
    const imageDraft = sitebrushEditorModel.imageDraft;
    if (!CSS.supports('object-position', positionText)) {
      imageDraft.error = editUIText.invalidImagePosition;
    } else {
      imageDraft.error = '';
      imageDraft.position = positionText;
      imageDraft.positionChanged = true;
    }
    renderImagePropertiesDialog();
  });
  document.getElementById('imagePropertiesSource').addEventListener('change', function changeImageSource(changeEvent) {
    replaceImageDraftSource(sitebrushEditorModel.imageDraft, changeEvent.target.value.trim());
  });
  document.getElementById('imagePropertiesReplace').addEventListener('click', function uploadImageReplacement() {
    const receivingDraft = sitebrushEditorModel.imageDraft;
    chooseAndUploadFiles(editorInstance, function receiveReplacementURL(storedFileURL, storedFileByteCount) {
      if (sitebrushEditorModel.imageDraft === receivingDraft) {
        replaceImageDraftSource(receivingDraft, storedFileURL, storedFileByteCount);
      }
    });
  });
  document.getElementById('imagePropertiesPreview').addEventListener('error', function showImagePreviewError() {
    if (sitebrushEditorModel.imageDraft) {
      sitebrushEditorModel.imageDraft.error = editUIText.imagePreviewFailed;
      sitebrushEditorModel.imageDraft.loadingPreview = false;
      renderImagePropertiesStatus();
    }
  });
  document.getElementById('imagePropertiesPreview').addEventListener('load', function completeImagePreview() {
    if (sitebrushEditorModel.imageDraft) {
      sitebrushEditorModel.imageDraft.loadingPreview = false;
      renderImagePropertiesStatus();
    }
  });
}

function replaceImageDraftSource(imageDraft, sourceURL, storedFileByteCount) {
  if (!imageDraft) {
    return;
  }
  if (!sourceURL) {
    imageDraft.error = editUIText.imagePreviewFailed;
  } else {
    imageDraft.error = '';
    imageDraft.source = sourceURL;
    imageDraft.fit = 'cover';
    imageDraft.position = '50% 50%';
    imageDraft.fitChanged = true;
    imageDraft.positionChanged = true;
  }
  renderImagePropertiesDialog();
  if (Number.isFinite(storedFileByteCount)) {
    imageDraft.fileByteCount = storedFileByteCount;
    imageDraft.fileSizeState = 'known';
    renderImagePropertiesFileSize();
  } else {
    loadImagePropertiesFileSize(imageDraft);
  }
}
