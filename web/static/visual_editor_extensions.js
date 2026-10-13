/* One UI participant owns these drafts. Async upload completions carry the
   recipient of their dialog, so a closed session cannot edit a later one. */
const sitebrushEditorModel = {
  document: null,
  previewNodes: [],
  previewAttributes: [],
  previewToken: '',
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

function newVisualEditorPreviewToken() {
  const randomBytes = new Uint8Array(16);
  crypto.getRandomValues(randomBytes);
  return Array.from(randomBytes, byteValue => byteValue.toString(16).padStart(2, '0')).join('');
}

function visualEditorOwnedPreviewElements(root, markerAttribute) {
  const previewToken = sitebrushEditorModel.previewToken;
  return Array.from(root.querySelectorAll('[' + markerAttribute + '][data-sitebrush-preview-owner]')).filter(function ownedPreviewElement(previewElement) {
    return previewElement.getAttribute('data-sitebrush-preview-owner') === previewToken;
  });
}

/* Template contents stay inert while parsing. Only the visual projection loses
   executable content; source mode and save restore the document-owned originals. */
function createVisualEditorPreviewBody(bodyHTML) {
  sitebrushEditorModel.previewNodes = [];
  sitebrushEditorModel.previewAttributes = [];
  sitebrushEditorModel.previewToken = newVisualEditorPreviewToken();
  const bodyTemplate = document.createElement('template');
  bodyTemplate.innerHTML = bodyHTML;
  for (const scriptElement of bodyTemplate.content.querySelectorAll('script')) {
    const scriptIndex = sitebrushEditorModel.previewNodes.length;
    sitebrushEditorModel.previewNodes.push(scriptElement.outerHTML);
    const scriptPlaceholder = document.createElement('div');
    scriptPlaceholder.setAttribute('data-sitebrush-preview-owner', sitebrushEditorModel.previewToken);
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
      previewElement.setAttribute('data-sitebrush-preview-owner', sitebrushEditorModel.previewToken);
      previewElement.setAttribute('data-sitebrush-preview-attributes', String(attributeIndex));
    }
  }
  return bodyTemplate.innerHTML;
}

function restoreVisualEditorPreviewBody(previewBodyHTML) {
  const bodyTemplate = document.createElement('template');
  bodyTemplate.innerHTML = previewBodyHTML;
  for (const previewElement of visualEditorOwnedPreviewElements(bodyTemplate.content, 'data-sitebrush-preview-attributes')) {
    const attributeIndex = Number(previewElement.getAttribute('data-sitebrush-preview-attributes'));
    const originalAttributes = sitebrushEditorModel.previewAttributes[attributeIndex];
    if (originalAttributes) {
      for (const originalAttribute of originalAttributes) {
        previewElement.setAttribute(originalAttribute.name, originalAttribute.text);
      }
      previewElement.removeAttribute('data-sitebrush-preview-attributes');
      previewElement.removeAttribute('data-sitebrush-preview-owner');
    }
  }
  for (const scriptPlaceholder of visualEditorOwnedPreviewElements(bodyTemplate.content, 'data-sitebrush-preview-node')) {
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
