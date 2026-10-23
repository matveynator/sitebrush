const assert = require('node:assert/strict');
const { chromium } = require('../../scripts/editor/node_modules/playwright');

async function checkConnectionAndEditor() {
  const fixtureURL = process.env.SITEBRUSH_MCP_FIXTURE_URL;
  const cookieText = process.env.SITEBRUSH_MCP_COOKIE;
  assert.ok(fixtureURL && cookieText, 'Run through TestMCPConnectionAndAIEditorBrowser');
  const browser = await chromium.launch({ headless: true, executablePath: process.env.SITEBRUSH_EDITOR_CHROMIUM || undefined });
  try {
    for (const viewport of [{ width: 1280, height: 800 }, { width: 320, height: 568 }]) {
      const browserContext = await browser.newContext({ viewport });
      const page = await browserContext.newPage();
      page.setDefaultTimeout(15000);
      await page.goto(fixtureURL + '/?mcp');
      assert.equal(await page.locator('#mcp-url').inputValue(), fixtureURL + '/?mcp');
      assert.match(await page.textContent('body'), /claude mcp add --transport http/);
      assert.ok(await page.locator('a[href*="login"]').count(), 'Instructions work before sign-in');
      assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1), 'Connection page fits mobile');
      const cookieSeparator = cookieText.indexOf('=');
      await browserContext.addCookies([{ name: cookieText.slice(0, cookieSeparator), value: cookieText.slice(cookieSeparator + 1), url: fixtureURL }]);
      let availableVersion = '938';
      let updateStatusRequests = 0;
      await page.routeWebSocket(/server_update_ws/, function routeUpdateStatus(socket) {
        socket.onMessage(function receiveUpdateStatus(encodedRequest) {
          assert.equal(JSON.parse(encodedRequest).action, 'status');
          updateStatusRequests += 1;
          socket.send(JSON.stringify({ State: { available: { version: availableVersion } } }));
        });
      });
      for (const central of [false, true]) {
        for (const releaseVersion of ['938', '939']) {
          availableVersion = releaseVersion;
          const previousRequests = updateStatusRequests;
          await page.goto(fixtureURL + '/menu-fixture' + (central ? '?central=1' : ''));
          assert.equal(updateStatusRequests, previousRequests, 'Opening a page does not request update status');
          await page.locator('h1').click({ button: 'right' });
          await page.waitForFunction(() => {
            const menuHost = document.getElementById('SiteBrushMenuBox');
            return menuHost && menuHost.shadowRoot;
          });
          const menuLinks = await page.locator('#SiteBrushMenuBox .SiteBrushMenuList a').allTextContents();
          assert.ok(menuLinks.indexOf('Аналитика') < menuLinks.indexOf('Настройки'));
          assert.ok(menuLinks.includes('Настройки сервера'));
          const accountIndex = menuLinks.findIndex(label => /[Уу]ч[её]тная запись/.test(label));
          assert.equal(accountIndex + 1, menuLinks.findIndex(label => /Выход|Выйти/.test(label)), 'Account stays immediately above sign out');
          assert.ok(!menuLinks.some(label => label.includes('AI · SiteBrush')));
          const updateLink = page.locator('#SiteBrushMenuBox .SiteBrushContextMenuUpdate');
          if (releaseVersion === '939') {
            await updateLink.waitFor({ state: 'visible' });
          } else {
            await page.waitForTimeout(50);
            assert.equal(await updateLink.isVisible(), false, 'Same version hides update link');
          }
          const menuLayout = await page.locator('#SiteBrushMenuBox .SiteBrushMenuBox').evaluate(menu => {
            const rectangle = menu.getBoundingClientRect();
            return { fits: rectangle.left >= 0 && rectangle.top >= 0 && rectangle.right <= window.innerWidth && rectangle.bottom <= window.innerHeight + 1, scrolls: menu.scrollHeight > menu.clientHeight + 1 };
          });
          assert.ok(menuLayout.fits && !menuLayout.scrolls, 'Menu fits the viewport without scrolling');
        }
      }
      let publicMenuPaused = false;
      await page.routeWebSocket(/sitebrush_menu_ws/, function routePublicMenu(socket) {
        if (publicMenuPaused) {
          socket.onMessage(function showUpdatingMenu() { socket.send(JSON.stringify({updating: true, message: 'Идёт обновление сайта, подождите.'})); });
        } else {
          socket.connectToServer();
        }
      });
      await page.goto(fixtureURL + '/public-fixture');
      await page.locator('h1').click({button: 'right'});
      await page.locator('#SiteBrushMenuBox a[href*="profile"]').waitFor({state: 'visible'});
      const lazyMenuStyle = await page.locator('#SiteBrushMenuBox .SiteBrushMenuBox').evaluate(menu => getComputedStyle(menu).position);
      assert.equal(lazyMenuStyle, 'fixed', 'Lazy administrator menu retains its shadow-root styles');
      publicMenuPaused = true;
      await page.mouse.click(viewport.width - 4, viewport.height - 4);
      await page.locator('h1').click({button: 'right'});
      await page.locator('#SiteBrushMenuBox').getByText('Идёт обновление сайта, подождите.').waitFor({state: 'visible'});
      assert.equal(await page.locator('#SiteBrushMenuBox a, #SiteBrushMenuBox button').count(), 0, 'Maintenance menu exposes no edit or sign-in actions');
      publicMenuPaused = false;
      await page.mouse.click(viewport.width - 4, viewport.height - 4);
      await page.locator('h1').click({button: 'right'});
      await page.locator('#SiteBrushMenuBox a[href*="profile"]').waitFor({state: 'visible'});
      await page.route('**/*', async function routeProviderFixture(route) {
        const address = new URL(route.request().url());
        if (address.searchParams.has('ai_provider_status')) {
          await route.fulfill({ json: { providers: [{ provider: 'ollama', saved: true, unlocked: true }, { provider: 'anthropic', saved: true, unlocked: true }] } });
        } else if (address.searchParams.has('ai_provider_models')) {
          await route.fulfill({ json: { models: ['fixture-model'], model: 'fixture-model' } });
        } else if (address.origin !== fixtureURL) {
          await route.abort();
        } else {
          await route.continue();
        }
      });
      await page.addInitScript(() => {
        window.SpeechRecognition = class FixtureSpeechRecognition {
          start() {
            this.stopped = false;
            queueMicrotask(() => {
              this.onstart();
              const transcript = [{ transcript: 'Change the headline' }];
              transcript.isFinal = true;
              this.onresult({ results: [transcript] });
            });
          }
          stop() { if (!this.stopped) { this.stopped = true; queueMicrotask(() => this.onend()); } }
          abort() { this.stop(); }
        };
      });
      const requests = [];
      await page.routeWebSocket(/ai_execute_ws/, function routeEditorStream(socket) {
        socket.onMessage(function receiveEditorTask(encodedRequest) {
          requests.push(JSON.parse(encodedRequest));
          socket.send(JSON.stringify({ type: 'status', text: 'Editing fixture' }));
          socket.send(JSON.stringify({ type: 'delta', text: '<h1>Updated by AI</h1>' }));
          socket.send(JSON.stringify({ type: 'draft', path: '/', title: 'AI fixture', html: '<!doctype html><html><head><title>AI fixture</title></head><body><h1>Updated by AI</h1></body></html>', draft: true }));
        });
      });
      await page.goto(fixtureURL + '/?ai');
      await page.locator('#externalTabButton').click();
      assert.ok(await page.locator('#externalPanel a[href="/?mcp"]').isVisible());
      assert.ok(await page.locator('#externalPanel a[href="/?profile#ai-access"]').isVisible());
      await page.locator('#pageTabButton').click();
      await page.locator('#recordButton').click();
      await page.waitForFunction(() => document.getElementById('aiCommand').value === 'Change the headline');
      await page.locator('#executeButton').click();
      await page.locator('#previewPanel').waitFor({ state: 'visible' });
      assert.equal(requests[0].provider, 'ollama');
      await page.locator('#discardDraftButton').click();
      await page.locator('#previewPanel').waitFor({ state: 'hidden' });
      await page.locator('#providerSettings').evaluate(settings => { settings.open = true; });
      await page.selectOption('#provider', 'anthropic');
      await page.locator('#aiCommand').fill('Update the headline using the cloud provider');
      await page.locator('#executeButton').click();
      await page.locator('#previewPanel').waitFor({ state: 'visible' });
      assert.equal(requests[1].provider, 'anthropic');
      await Promise.all([
        page.waitForResponse(response => new URL(response.url()).searchParams.has('ai_apply') && response.status() === 200),
        page.locator('#saveDraftButton').click()
      ]);
      await page.waitForFunction(() => document.getElementById('status').textContent.startsWith('Сохранено.'));
      await browserContext.close();
    }
    console.log('MCP instructions, mobile layout, voice input, local/cloud editor selection, streaming draft, cancellation and real save passed');
  } finally { await browser.close(); }
}
checkConnectionAndEditor().catch(error => { console.error(error); process.exitCode = 1; });
