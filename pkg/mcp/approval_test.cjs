const assert = require('node:assert/strict');
const { chromium } = require('../../scripts/editor/node_modules/playwright');

async function checkApprovalRedirect() {
  const approvalURL = process.env.SITEBRUSH_MCP_APPROVAL_URL;
  const callbackURL = new URL(process.env.SITEBRUSH_MCP_CALLBACK_URL);
  const cookieText = process.env.SITEBRUSH_MCP_COOKIE;
  assert.ok(approvalURL && cookieText, 'Run through TestMCPApprovalBrowserRedirect');
  const launchOptions = { headless: true };
  if (process.env.SITEBRUSH_EDITOR_CHROMIUM) {
    launchOptions.executablePath = process.env.SITEBRUSH_EDITOR_CHROMIUM;
  }
  const browser = await chromium.launch(launchOptions);
  try {
    const browserContext = await browser.newContext();
    const separator = cookieText.indexOf('=');
    await browserContext.addCookies([{ name: cookieText.slice(0, separator), value: cookieText.slice(separator + 1), url: approvalURL }]);
    const page = await browserContext.newPage();
    page.setDefaultTimeout(15000);
    await page.goto(approvalURL);
    await Promise.all([
      page.waitForURL(address => address.origin === callbackURL.origin && address.pathname === callbackURL.pathname),
      page.locator('button[name="decision"][value="allow"]').click()
    ]);
    const resultURL = new URL(page.url());
    assert.ok(resultURL.searchParams.get('code'), 'Missing one-time authorization response');
    assert.equal(resultURL.searchParams.get('state'), 'browser-state');
    assert.equal(await page.textContent('body'), 'OAuth complete');
    console.log('SiteBrush owner approval reached the registered external callback');
  } finally {
    await browser.close();
  }
}

checkApprovalRedirect().catch(error => {
  console.error(error.message);
  process.exitCode = 1;
});
