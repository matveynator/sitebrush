const assert = require('node:assert/strict');
const { chromium } = require('playwright');

const fixtureURL = process.env.SITEBRUSH_EDITOR_URL;
const cookieText = process.env.SITEBRUSH_EDITOR_COOKIE;
assert.ok(fixtureURL && cookieText, 'Run through TestVisualEditorBrowserRegressions');
const cookieSeparator = cookieText.indexOf('=');

async function runEditorRegressions() {
  const launchOptions = { headless: true };
  if (process.env.SITEBRUSH_EDITOR_CHROMIUM) {
    launchOptions.executablePath = process.env.SITEBRUSH_EDITOR_CHROMIUM;
  }
  const browser = await chromium.launch(launchOptions);
  let fixtureIndex = 0;
  try {
    for (const viewport of [{ width: 1280, height: 800 }, { width: 320, height: 568 }]) {
      const context = await browser.newContext({ viewport, hasTouch: viewport.width === 320 });
      await context.addCookies([{ name: cookieText.slice(0, cookieSeparator), value: cookieText.slice(cookieSeparator + 1), url: fixtureURL }]);
      const page = await context.newPage();
      page.setDefaultTimeout(10000);
      const browserErrors = [];
      const securityPolicyErrors = [];
      let checkingSecurityPolicy = false;
      page.on('console', consoleMessage => {
        const policyViolation = /Blocked script execution|Refused to execute|violates.*Content Security Policy|violates.*directive/.test(consoleMessage.text());
        if (page.url().includes('?visual') && policyViolation) {
          (checkingSecurityPolicy ? securityPolicyErrors : browserErrors).push(consoleMessage.text());
        }
      });
      page.on('pageerror', browserError => {
        if (page.url().includes('?visual')) {
          browserErrors.push(browserError.message);
          console.error(browserError.stack);
        }
      });
      const control = controlName => page.locator('.jodit-toolbar__box [data-ref="' + controlName + '"] button').first();
      const frameImage = imageID => page.frameLocator('.jodit-wysiwyg_iframe').locator('#' + imageID);

      async function openFixture() {
        const pagePath = '/editor-' + fixtureIndex++;
        await page.goto(fixtureURL + pagePath + '?visual');
        if (!(await page.locator('.jodit-container').count())) {
          console.error('Missing editor at', page.url(), (await page.content()).slice(-4000));
        }
        await page.locator('.jodit-wysiwyg_iframe').waitFor();
        await frameImage('attribute-image').waitFor();
        await frameImage('attribute-image').evaluate(imageElement => imageElement.decode());
        return pagePath;
      }
      async function storedHTML(pagePath) {
        const storedResponse = await context.request.get(fixtureURL + '/_editor-fixture.html?path=' + pagePath);
        assert.equal(storedResponse.status(), 200);
        return storedResponse.text();
      }
      async function saveWithButton(saveButton) {
        await Promise.all([
          page.waitForURL(browserURL => !browserURL.searchParams.has('visual')),
          saveButton.click()
        ]);
      }
      async function savedImageAttributes(pagePath, imageID) {
        const savedHTML = await storedHTML(pagePath);
        return page.evaluate(({ savedHTML, imageID }) => {
          const savedDocument = new DOMParser().parseFromString(savedHTML, 'text/html');
          const savedImage = savedDocument.getElementById(imageID);
          return {
            src: savedImage.getAttribute('src'), width: savedImage.getAttribute('width'), height: savedImage.getAttribute('height'),
            inlineWidth: savedImage.style.width, inlineHeight: savedImage.style.height,
            fit: savedImage.style.objectFit, position: savedImage.style.objectPosition, className: savedImage.className
          };
        }, { savedHTML, imageID });
      }
      async function previewScreenshotPixels() {
        const screenshotPNG = await page.locator('#imagePropertiesPreview').screenshot();
        return page.evaluate(async encodedScreenshot => {
          const screenshotImage = new Image();
          screenshotImage.src = 'data:image/png;base64,' + encodedScreenshot;
          await screenshotImage.decode();
          const screenshotCanvas = document.createElement('canvas');
          screenshotCanvas.width = screenshotImage.naturalWidth;
          screenshotCanvas.height = screenshotImage.naturalHeight;
          const screenshotContext = screenshotCanvas.getContext('2d');
          screenshotContext.drawImage(screenshotImage, 0, 0);
          return [[0.5, 0.05], [0.1, 0.6]].map(([horizontalFraction, verticalFraction]) => Array.from(screenshotContext.getImageData(
            Math.floor(screenshotCanvas.width * horizontalFraction), Math.floor(screenshotCanvas.height * verticalFraction), 1, 1
          ).data).slice(0, 3));
        }, screenshotPNG.toString('base64'));
      }

      // The old toolbar has enough controls to exceed this mobile viewport.
      const sourcePagePath = await openFixture();
      const originalDocument = await storedHTML(sourcePagePath);
      const visualControlCount = await page.locator('.jodit-toolbar__box button').count();
      assert.ok(visualControlCount > 15);
      await frameImage('attribute-image').hover();
      await page.waitForTimeout(400);
      assert.deepEqual(browserErrors, [], 'hover must not emit sandbox errors');
      assert.equal(await page.frameLocator('.jodit-wysiwyg_iframe').locator('body').evaluate(bodyElement => bodyElement.ownerDocument.defaultView.fixtureScriptMustNotRun), undefined);
      assert.equal(await page.frameLocator('.jodit-wysiwyg_iframe').locator('body').evaluate(bodyElement => bodyElement.ownerDocument.defaultView.fixtureHandlerMustNotRun), undefined);
      assert.equal(await page.frameLocator('.jodit-wysiwyg_iframe').locator('body').evaluate(bodyElement => bodyElement.ownerDocument.defaultView.fixtureBodyScriptMustNotRun), undefined);
      await control('source').click();
      const sourceEditor = page.locator('.jodit-source .CodeMirror');
      async function sourceHTML() {
        return sourceEditor.evaluate(editorElement => editorElement.CodeMirror.getValue());
      }
      async function replaceSourceHTML(documentHTML) {
        await sourceEditor.evaluate((editorElement, nextHTML) => {
          const codeMirrorEditor = editorElement.CodeMirror;
          codeMirrorEditor.replaceRange(nextHTML, { line: 0, ch: 0 },
            { line: codeMirrorEditor.lastLine(), ch: codeMirrorEditor.getLine(codeMirrorEditor.lastLine()).length });
        }, documentHTML);
      }
      async function sourceCursorIndex() {
        return sourceEditor.evaluate(editorElement => editorElement.CodeMirror.indexFromPos(editorElement.CodeMirror.getCursor()));
      }
      const loadedAssetURLs = await page.locator('script[src*="visual_editor_"]').evaluateAll(scriptElements => scriptElements.map(scriptElement => scriptElement.src));
      assert.equal(loadedAssetURLs.length, 2);
      const editorAssetVersion = new URL(loadedAssetURLs[0]).searchParams.get('v');
      assert.match(editorAssetVersion, /^[a-f0-9]{64}$/);
      assert.equal(new URL(loadedAssetURLs[1]).searchParams.get('v'), editorAssetVersion);
      for (const loadedAssetURL of loadedAssetURLs) {
        const assetResponse = await context.request.get(loadedAssetURL);
        assert.ok(assetResponse.headers()['cache-control'].includes('no-store'));
      }
      assert.equal(await sourceHTML(), originalDocument);
      assert.deepEqual(await sourceEditor.evaluate(editorElement => [editorElement.CodeMirror.getScrollInfo().top, editorElement.CodeMirror.indexFromPos(editorElement.CodeMirror.getCursor())]), [0, 0]);
      assert.equal(await page.locator('.jodit-toolbar__box button').count(), 3);
      for (const visualControlName of ['bold', 'italic', 'source', 'insertServerFile']) {
        assert.equal(await control(visualControlName).count(), 0, visualControlName + ' must be removed');
      }
      for (const codeControlName of ['cancelSource', 'saveSource', 'backToVisual']) {
        const codeButton = control(codeControlName);
        assert.ok(await codeButton.isVisible());
        const buttonRectangle = await codeButton.boundingBox();
        assert.ok(buttonRectangle.x >= 0 && buttonRectangle.x + buttonRectangle.width <= viewport.width + 1, codeControlName + ' within viewport');
        assert.ok(buttonRectangle.height >= 40, await codeButton.evaluate(buttonElement => buttonElement.outerHTML));
      }
      assert.equal(await page.locator('.jodit-toolbar__box').evaluate(toolbarElement => toolbarElement.scrollWidth <= toolbarElement.clientWidth), true);
      assert.equal(await page.locator('.jodit-toolbar__box .jodit-toolbar-button__separator').count(), 0);
      if (process.env.SITEBRUSH_EDITOR_ARTIFACT_DIR) {
        await page.screenshot({ path: process.env.SITEBRUSH_EDITOR_ARTIFACT_DIR + '/code-' + viewport.width + '.png' });
      }

      assert.ok(await sourceEditor.locator('.cm-tag').count(), 'HTML tags are highlighted');
      assert.ok(await sourceEditor.locator('.cm-attribute').count(), 'HTML attributes are highlighted');
      assert.ok(await sourceEditor.locator('.cm-keyword').count(), 'inline JavaScript is highlighted');
      const embeddedModes = await sourceEditor.evaluate(editorElement => {
        const codeMirrorEditor = editorElement.CodeMirror;
        const cssPosition = codeMirrorEditor.posFromIndex(codeMirrorEditor.getValue().indexOf('width: 180px') + 3);
        const scriptPosition = codeMirrorEditor.posFromIndex(codeMirrorEditor.getValue().indexOf('const boundaryExample') + 3);
        return [codeMirrorEditor.getModeAt(cssPosition).name, codeMirrorEditor.getModeAt(scriptPosition).name];
      });
      assert.deepEqual(embeddedModes, ['css', 'javascript']);
      const lightTagColor = await sourceEditor.locator('.cm-tag').first().evaluate(tagElement => getComputedStyle(tagElement).color);
      await page.emulateMedia({ colorScheme: 'dark' });
      assert.notEqual(await sourceEditor.locator('.cm-tag').first().evaluate(tagElement => getComputedStyle(tagElement).color), lightTagColor);
      await page.emulateMedia({ colorScheme: 'light' });
      const sourceRectangle = await sourceEditor.boundingBox();
      assert.ok(sourceRectangle.x >= 0 && sourceRectangle.x + sourceRectangle.width <= viewport.width + 1);
      assert.ok(sourceRectangle.y + sourceRectangle.height <= viewport.height + 1);

      await replaceSourceHTML('<div>');
      await sourceEditor.evaluate(editorElement => {
        editorElement.CodeMirror.setCursor(0, 5);
        editorElement.CodeMirror.focus();
      });
      await page.keyboard.press('Enter');
      assert.equal(await sourceHTML(), '<div>\n  ');
      await page.keyboard.press('Tab');
      assert.equal(await sourceHTML(), '<div>\n    ');
      await page.keyboard.press('Shift+Tab');
      assert.equal(await sourceHTML(), '<div>\n  ');
      await page.keyboard.insertText('text');
      await sourceEditor.evaluate(editorElement => editorElement.CodeMirror.setSelection({ line: 0, ch: 0 }, { line: 1, ch: 6 }));
      await page.keyboard.press('Tab');
      assert.equal(await sourceHTML(), '  <div>\n    text');
      await page.keyboard.press('Shift+Tab');
      assert.equal(await sourceHTML(), '<div>\n  text');
      await sourceEditor.evaluate(editorElement => {
        const codeMirrorEditor = editorElement.CodeMirror;
        codeMirrorEditor.clearHistory();
        codeMirrorEditor.setCursor(1, 6);
      });
      await page.keyboard.insertText(' undo');
      assert.equal(await sourceHTML(), '<div>\n  text undo');
      await sourceEditor.evaluate(editorElement => editorElement.CodeMirror.undo());
      assert.equal(await sourceHTML(), '<div>\n  text');
      await sourceEditor.evaluate(editorElement => editorElement.CodeMirror.redo());
      assert.equal(await sourceHTML(), '<div>\n  text undo');
      assert.equal(await page.evaluate(() => sitebrushEditorModel.sourceHTML), '<div>\n  text undo');
      await control('cancelSource').click();
      await control('source').click();
      assert.equal(await sourceHTML(), originalDocument);

      // Typing midway through a long document must retain its caret and scroll.
      const caretStart = originalDocument.indexOf('Long document for caret') + 3000;
      await sourceEditor.evaluate((editorElement, cursorIndex) => {
        const codeMirrorEditor = editorElement.CodeMirror;
        codeMirrorEditor.setCursor(codeMirrorEditor.posFromIndex(cursorIndex));
        codeMirrorEditor.focus();
      }, caretStart);
      // Let the browser reveal the caret before measuring; native typing may
      // legitimately scroll an offscreen caret into view on a narrow viewport.
      await page.keyboard.press('ArrowRight');
      await page.keyboard.press('ArrowLeft');
      const scrollBeforeTyping = await sourceEditor.evaluate(editorElement => editorElement.CodeMirror.getScrollInfo().top);
      assert.ok(scrollBeforeTyping > 0, 'long document is scrolled before typing');
      await page.keyboard.insertText('caret-regression');
      await page.waitForTimeout(300);
      assert.equal(await sourceCursorIndex(), caretStart + 16);
      const scrollAfterTyping = await sourceEditor.evaluate(editorElement => editorElement.CodeMirror.getScrollInfo().top);
      const sourceLineHeight = await sourceEditor.evaluate(editorElement => editorElement.CodeMirror.defaultTextHeight());
      assert.ok(Math.abs(scrollAfterTyping - scrollBeforeTyping) <= sourceLineHeight + 1,
        'source scroll moved unexpectedly: before=' + scrollBeforeTyping + ' after=' + scrollAfterTyping);
      await page.setViewportSize({ width: viewport.width, height: viewport.height - 20 });
      assert.equal(await sourceCursorIndex(), caretStart + 16);
      await page.setViewportSize(viewport);
      await page.emulateMedia({ colorScheme: 'dark' });
      assert.equal(await sourceCursorIndex(), caretStart + 16);
      assert.equal(await page.locator('.jodit-toolbar__box button').count(), 3);
      await page.emulateMedia({ colorScheme: 'light' });
      await control('cancelSource').click();
      assert.ok(await control('bold').isVisible());
      assert.equal(await page.locator('.jodit-toolbar__box button').count(), visualControlCount);
      await control('source').click();
      assert.equal(await sourceHTML(), originalDocument);
      const modifiedDocument = originalDocument.replace('Editable content', 'Changed from source')
        .replace('preserve > metadata', 'source metadata preserved')
        .replace('.class-frame { width: 180px; height: 120px; }', '.class-frame { width: 160px; height: 110px; }')
        .replace(' data-example="body-preserved"', '');
      await replaceSourceHTML(modifiedDocument);
      await control('backToVisual').click();
      assert.ok(await control('bold').isVisible());
      assert.ok(await page.frameLocator('.jodit-wysiwyg_iframe').getByText('Changed from source').isVisible());
      const updatedClassArea = await frameImage('class-image').boundingBox();
      assert.equal(updatedClassArea.width, 160);
      assert.equal(updatedClassArea.height, 110);
      assert.equal(await page.frameLocator('.jodit-wysiwyg_iframe').locator('body').getAttribute('data-example'), null);
      await control('source').click();
      assert.equal(await sourceHTML(), modifiedDocument);
      await saveWithButton(control('saveSource'));
      assert.equal(await storedHTML(sourcePagePath), modifiedDocument);

      const unchangedPagePath = await openFixture();
      const unchangedHTML = await storedHTML(unchangedPagePath);
      await control('source').click();
      await control('backToVisual').click();
      await saveWithButton(control('savePageHtml'));
      assert.equal(await storedHTML(unchangedPagePath), unchangedHTML, 'no-op save preserves exact document');

      const hotkeyPagePath = await openFixture();
      await control('source').click();
      const hotkeyDocument = (await sourceHTML()).replace('Editable content', 'Saved with keyboard');
      await replaceSourceHTML(hotkeyDocument);
      await sourceEditor.evaluate(editorElement => editorElement.CodeMirror.focus());
      await Promise.all([
        page.waitForURL(browserURL => !browserURL.searchParams.has('visual')),
        page.keyboard.press(viewport.width === 320 ? 'Meta+s' : 'Control+s')
      ]);
      assert.equal(await storedHTML(hotkeyPagePath), hotkeyDocument);

      // Repair historical copies through source mode, then repeat visual saves.
      // The document envelope must never be appended to the editable body.
      const repeatedPagePath = '/editor-repeat-' + viewport.width;
      const corruptedPagePath = '/editor-corrupted-' + viewport.width;
      const canonicalDocument = await storedHTML(repeatedPagePath);
      const canonicalHead = canonicalDocument.slice(0, canonicalDocument.indexOf('<body'));
      await page.goto(fixtureURL + corruptedPagePath + '?visual');
      await control('source').click();
      assert.equal((await sourceHTML()).match(/<meta\b/g).length, 3);
      await replaceSourceHTML(canonicalDocument);
      await control('backToVisual').click();
      await saveWithButton(control('savePageHtml'));
      for (let saveCycle = 0; saveCycle < 3; saveCycle++) {
        const cycleDocument = await storedHTML(corruptedPagePath);
        assert.equal(cycleDocument.slice(0, cycleDocument.indexOf('<body')), canonicalHead);
        assert.equal(cycleDocument.match(/<meta\b/g).length, 1);
        assert.equal(cycleDocument.match(/<head\b/g).length, 1);
        await page.goto(fixtureURL + corruptedPagePath + '?visual');
        await control('source').click();
        assert.equal((await sourceHTML()).match(/<meta\b/g).length, 1);
        await control('backToVisual').click();
        const cycleParagraph = page.frameLocator('.jodit-wysiwyg_iframe').getByText('Editable content', { exact: false });
        await cycleParagraph.click();
        await page.keyboard.press('End');
        await page.keyboard.insertText(' cycle-' + saveCycle);
        await saveWithButton(control('savePageHtml'));
      }
      assert.equal((await storedHTML(corruptedPagePath)).match(/<meta\b/g).length, 1);

      const visualEditPath = await openFixture();
      const visualOriginal = await storedHTML(visualEditPath);
      const editableParagraph = page.frameLocator('.jodit-wysiwyg_iframe').getByText('Editable content', { exact: true });
      await editableParagraph.click();
      await page.keyboard.press('End');
      await page.keyboard.insertText(' updated');
      await saveWithButton(control('savePageHtml'));
      const visuallySaved = await storedHTML(visualEditPath);
      assert.equal(visuallySaved.slice(0, visuallySaved.indexOf('<body')), visualOriginal.slice(0, visualOriginal.indexOf('<body')));
      assert.ok(visuallySaved.includes('Editable content updated'));
      assert.ok(visuallySaved.includes('onload="window.fixtureHandlerMustNotRun = true"'));
      assert.ok(visuallySaved.includes('<script id="body-script">window.fixtureBodyScriptMustNotRun = true;</script>'));
      assert.ok(visuallySaved.includes('srcdoc="&lt;script&gt;window.fixtureNestedScriptMustNotRun = true;&lt;/script&gt;"'));

      const cancelPagePath = await openFixture();
      await frameImage('attribute-image').click();
      assert.equal(await page.locator('#imagePropertiesDialog').isVisible(), false);
      await page.locator('.jodit-popup [data-ref="pencil"] button').click();
      assert.ok(await page.locator('#imagePropertiesDialog').isVisible());
      await page.locator('#imagePropertiesClose').click();
      await frameImage('attribute-image').dblclick();
      assert.ok(await page.locator('#imagePropertiesDialog').isVisible());
      await page.locator('#imagePropertiesSource').fill('/_editor-tall.svg');
      await page.locator('#imagePropertiesSource').press('Tab');
      await page.locator('#imagePropertiesLock').uncheck();
      await page.locator('#imagePropertiesWidth').fill('200');
      await page.locator('#imagePropertiesWidth').press('Tab');
      await page.locator('#imagePropertiesFit').selectOption('contain');
      await page.locator('#imagePropertiesCancel').click();
      assert.equal(await frameImage('attribute-image').getAttribute('src'), '/_editor-wide.svg');
      assert.equal(await frameImage('attribute-image').getAttribute('width'), '180');
      assert.equal(await frameImage('attribute-image').getAttribute('style'), null);
      assert.ok((await storedHTML(cancelPagePath)).includes('src="/_editor-wide.svg"'));
      await frameImage('attribute-image').dblclick();
      await page.locator('#imagePropertiesClose').click();
      assert.equal(await page.locator('#imagePropertiesDialog').isVisible(), false);

      // Exercise the real upload dialog, original bytes and production save.
      for (const imageID of ['attribute-image', 'class-image', 'responsive-image', 'inline-image']) {
        const uploadPagePath = await openFixture();
        const originalArea = await frameImage(imageID).boundingBox();
        await frameImage(imageID).dblclick();
        const fileChooserPromise = page.waitForEvent('filechooser');
        await page.locator('#imagePropertiesReplace').click();
        const fileChooser = await fileChooserPromise;
        const imageBytes = await page.evaluate(() => {
          const imageCanvas = document.createElement('canvas');
          imageCanvas.width = 100;
          imageCanvas.height = 400;
          const drawingContext = imageCanvas.getContext('2d');
          drawingContext.fillStyle = '#ff8800';
          drawingContext.fillRect(0, 0, 100, 400);
          return imageCanvas.toDataURL('image/png').split(',')[1];
        });
        const originalBytes = Buffer.from(imageBytes, 'base64');
        await fileChooser.setFiles({ name: 'replacement.png', mimeType: 'image/png', buffer: originalBytes });
        await page.locator('#uploadDialogName').waitFor();
        await page.locator('#uploadDialogUpload').waitFor({ state: 'visible' });
        const uploadResponsePromise = page.waitForResponse(response => response.url().endsWith('?files') && response.request().method() === 'POST');
        await page.locator('#uploadDialogUpload').click();
        const uploadResponse = await uploadResponsePromise;
        assert.equal(uploadResponse.status(), 200);
        await page.locator('#uploadDialogOverlay').waitFor({ state: 'hidden' });
        const uploadedSource = await page.locator('#imagePropertiesSource').inputValue();
        assert.match(uploadedSource, /^\/p\/[a-f0-9]+\.png$/);
        const uploadedFileResponse = await context.request.get(fixtureURL + uploadedSource);
        assert.equal(uploadedFileResponse.status(), 200);
        assert.ok((await uploadedFileResponse.body()).equals(originalBytes), 'replacement preserves original bytes');
        const preview = page.locator('#imagePropertiesPreview');
        await preview.evaluate(previewElement => previewElement.decode());
        assert.equal(await preview.evaluate(previewElement => getComputedStyle(previewElement).objectFit), 'cover');
        assert.equal(await page.locator('#imagePropertiesWidth').inputValue(), String(Math.round(originalArea.width)));
        assert.equal(await page.locator('#imagePropertiesHeight').inputValue(), String(Math.round(originalArea.height)));
        await page.locator('#imagePropertiesPosition').fill('25% 75%');
        await page.locator('#imagePropertiesPosition').press('Tab');
        await saveWithButton(page.locator('#imagePropertiesSave'));
        const savedAttributes = await savedImageAttributes(uploadPagePath, imageID);
        assert.equal(savedAttributes.src, uploadedSource);
        assert.equal(savedAttributes.fit, 'cover');
        assert.equal(savedAttributes.position, '25% 75%');
        if (imageID === 'attribute-image') {
          assert.equal(savedAttributes.width, '180');
          assert.equal(savedAttributes.height, '120');
        }
        if (imageID === 'class-image' || imageID === 'responsive-image') {
          assert.equal(savedAttributes.inlineWidth, '');
          assert.equal(savedAttributes.inlineHeight, '');
          assert.ok(savedAttributes.className);
        }
        if (imageID !== 'responsive-image') {
          await page.goto(fixtureURL + uploadPagePath + '?visual');
          const replacedArea = await frameImage(imageID).boundingBox();
          assert.equal(replacedArea.width, originalArea.width);
          assert.equal(replacedArea.height, originalArea.height);
        }
      }

      for (const fitMode of ['cover', 'contain', 'fill']) {
        const fitPagePath = await openFixture();
        await frameImage('attribute-image').dblclick();
        await page.locator('#imagePropertiesSource').fill('/_editor-tall.svg');
        await page.locator('#imagePropertiesSource').press('Tab');
        await page.locator('#imagePropertiesFit').selectOption(fitMode);
        await page.locator('#imagePropertiesWidth').fill('240');
        await page.locator('#imagePropertiesWidth').press('Tab');
        assert.equal(await page.locator('#imagePropertiesHeight').inputValue(), '160');
        await page.locator('#imagePropertiesPreview').evaluate(previewElement => previewElement.decode());
        assert.equal(await page.locator('#imagePropertiesPreview').evaluate(previewElement => getComputedStyle(previewElement).objectFit), fitMode);
        const previewPixels = await previewScreenshotPixels();
        if (fitMode === 'cover') {
          assert.notDeepEqual(previewPixels[0], [255, 0, 0], 'cover crops the top red band');
        } else {
          assert.deepEqual(previewPixels[0], [255, 0, 0], fitMode + ' shows the top band');
        }
        if (fitMode === 'contain') {
          assert.notDeepEqual(previewPixels[1], [255, 165, 0], 'contain leaves side space');
        } else {
          assert.deepEqual(previewPixels[1], [255, 165, 0], fitMode + ' fills the frame');
        }
        await saveWithButton(page.locator('#imagePropertiesSave'));
        const savedFit = await savedImageAttributes(fitPagePath, 'attribute-image');
        assert.equal(savedFit.width, '240');
        assert.equal(savedFit.height, '160');
        assert.equal(savedFit.fit, fitMode);
      }

      // Failed saves retain the source draft and can be retried through the
      // same endpoint. Failed uploads must not mutate the selected image.
      const failedSavePath = await openFixture();
      await control('source').click();
      const errorDraft = (await sourceHTML()).replace('Editable content', 'Retry save draft');
      await replaceSourceHTML(errorDraft);
      await page.route('**/*?save', async interceptedRoute => {
        assert.equal(await sourceEditor.evaluate(editorElement => editorElement.CodeMirror.getOption('readOnly')), true);
        await interceptedRoute.fulfill({ status: 500, body: 'fixture save failure' });
      });
      await control('saveSource').click();
      await page.locator('#editorSaveStatus').getByText('fixture save failure', { exact: false }).waitFor();
      assert.equal(await sourceHTML(), errorDraft);
      assert.equal(await sourceEditor.evaluate(editorElement => editorElement.CodeMirror.getOption('readOnly')), false);
      await page.unroute('**/*?save');
      await saveWithButton(control('saveSource'));
      assert.equal(await storedHTML(failedSavePath), errorDraft);

      await openFixture();
      await frameImage('class-image').dblclick();
      await page.locator('#imagePropertiesSource').fill('http://[');
      await page.locator('#imagePropertiesSource').press('Tab');
      assert.equal(await page.locator('#imagePropertiesSave').isEnabled(), false);
      await page.locator('#imagePropertiesSource').fill('/_editor-wide.svg');
      await page.locator('#imagePropertiesSource').press('Tab');
      await page.locator('#imagePropertiesPosition').fill('invalid position');
      await page.locator('#imagePropertiesPosition').press('Tab');
      assert.equal(await page.locator('#imagePropertiesSave').isEnabled(), false);
      await page.locator('#imagePropertiesPosition').fill('left top');
      await page.locator('#imagePropertiesPosition').press('Tab');
      const chooserPromise = page.waitForEvent('filechooser');
      await page.locator('#imagePropertiesReplace').click();
      await (await chooserPromise).setFiles({ name: 'replacement.svg', mimeType: 'image/svg+xml', buffer: Buffer.from('<svg xmlns="http://www.w3.org/2000/svg" width="100" height="400"><rect width="100" height="400" fill="orange"/></svg>') });
      await page.route('**/*?files', interceptedRoute => interceptedRoute.fulfill({ status: 500, body: 'fixture upload failure' }));
      await page.locator('#uploadDialogUpload').click();
      await page.waitForFunction(() => !document.getElementById('uploadDialogUpload').disabled && document.getElementById('uploadDialogError').textContent.length > 0);
      assert.equal(await frameImage('class-image').getAttribute('src'), '/_editor-wide.svg');
      await page.unroute('**/*?files');
      let releaseDelayedUpload;
      const delayedUpload = new Promise(resolve => { releaseDelayedUpload = resolve; });
      let confirmUploadReceived;
      const uploadReceived = new Promise(resolve => { confirmUploadReceived = resolve; });
      await page.route('**/*?files', async interceptedRoute => {
        const completedUpload = await interceptedRoute.fetch();
        confirmUploadReceived();
        await delayedUpload;
        await interceptedRoute.fulfill({ response: completedUpload });
      });
      await page.locator('#uploadDialogUpload').click();
      await uploadReceived;
      await page.locator('#uploadDialogClose').click();
      await page.locator('#imagePropertiesCancel').click();
      await frameImage('attribute-image').dblclick();
      releaseDelayedUpload();
      await page.waitForTimeout(100);
      assert.equal(await page.locator('#imagePropertiesSource').inputValue(), '/_editor-wide.svg', 'late upload cannot change another dialog');
      await page.unroute('**/*?files');
      await page.locator('#imagePropertiesClose').click();

      if (viewport.width === 320) {
        await openFixture();
        await frameImage('attribute-image').tap();
        await frameImage('attribute-image').tap();
        assert.ok(await page.locator('#imagePropertiesDialog').isVisible(), 'double tap opens image properties');
      }
      assert.deepEqual(browserErrors, [], 'browser errors');
      checkingSecurityPolicy = true;
      await page.evaluate(() => {
        const frameElement = document.querySelector('.jodit-wysiwyg_iframe');
        const frameDocument = frameElement.contentDocument;
        const injectedScript = frameDocument.createElement('script');
        injectedScript.textContent = 'parent.fixtureScriptEscaped = true';
        frameDocument.head.appendChild(injectedScript);
        injectedScript.remove();
        const injectedImage = frameDocument.createElement('img');
        injectedImage.setAttribute('onerror', 'parent.fixtureHandlerEscaped = true');
        frameDocument.body.appendChild(injectedImage);
        injectedImage.dispatchEvent(new Event('error'));
        injectedImage.remove();
      });
      await page.waitForTimeout(100);
      assert.deepEqual(await page.evaluate(() => [window.fixtureScriptEscaped, window.fixtureHandlerEscaped]), [undefined, undefined], 'CSP must prevent page code execution');
      assert.ok(securityPolicyErrors.length >= 2, 'CSP rejects scripts and inline handlers');
      console.log('Editor regressions passed at ' + viewport.width + '×' + viewport.height);
      await context.close();
    }
  } finally {
    await browser.close();
  }
}

runEditorRegressions().catch(browserError => {
  console.error(browserError);
  process.exitCode = 1;
});
