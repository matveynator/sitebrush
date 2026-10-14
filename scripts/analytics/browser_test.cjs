const assert = require('node:assert/strict');
const os = require('node:os');
const path = require('node:path');
const {chromium,webkit} = require('../editor/node_modules/playwright');
const browserEngine = process.env.SITEBRUSH_BROWSER_ENGINE || 'chromium';
assert.ok(['chromium','webkit'].includes(browserEngine),'Unsupported browser engine');
(async function analyticsBrowserChecks() {
 const launchOptions = {headless:true};
 if(browserEngine==='chromium'&&process.env.SITEBRUSH_EDITOR_CHROMIUM)launchOptions.executablePath=process.env.SITEBRUSH_EDITOR_CHROMIUM;
 const browser=await(browserEngine==='webkit'?webkit:chromium).launch(launchOptions);
 try {
  for(const viewport of [{width:1280,height:800},{width:320,height:568}]) {
   const context=await browser.newContext({viewport,locale:'en-US'});
   const cookieText=process.env.SITEBRUSH_ANALYTICS_COOKIE;const separator=cookieText.indexOf('=');
   await context.addCookies([{name:cookieText.slice(0,separator),value:cookieText.slice(separator+1),url:process.env.SITEBRUSH_ANALYTICS_URL}]);
   await context.route('https://tile.openstreetmap.org/**',async tileRequest=>{
    assert.equal(tileRequest.request().headers().referer,new URL(process.env.SITEBRUSH_ANALYTICS_URL).origin+'/','OSM must receive only the site origin');
    await tileRequest.fulfill({contentType:'image/png',body:Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/l9sAAAAASUVORK5CYII=','base64')});
   });
   const page=await context.newPage();const errors=[];page.on('pageerror',error=>errors.push(error.message));
   await page.goto(process.env.SITEBRUSH_ANALYTICS_URL+'/?analytics&days=7');
   await page.locator('#conversion-funnels').getByText('Download · 100.0% · 1 / 1').waitFor();
   assert.equal(await page.locator('#technical-analytics').count(),0);
   assert.deepEqual(await page.locator('#conversion-dashboard > section').evaluateAll(sections=>sections.slice(0,2).map(section=>section.querySelector('h2').textContent.trim())),['Visits map','Conversion']);
   assert.equal(await page.locator('#conversion-dashboard > section:first-of-type select[name=days]').count(),1);
   assert.equal(await page.locator('#conversion-calendar-years button').count()>0,true);
   assert.equal(await page.locator('#analytics-server-hours,#analytics-map-points').count(),0);
   assert.equal(await page.locator('#conversion-dashboard').evaluate(element=>element.scrollWidth<=element.clientWidth),true,'dashboard overflows mobile');
   assert.equal(await page.locator('#conversion-paths > details').getAttribute('open') !== null,true);
   assert.equal(await page.locator('.conversion-tree').count()>0,true);
   await page.locator('#conversion-site-tree summary').filter({hasText:'Home · /'}).waitFor();
   assert.ok((await page.locator('#conversion-site-tree').textContent()).includes('Entrances: 1'));
   await page.locator('#conversion-site-tree summary').filter({hasText:/^\/docs$/}).click();
   await page.locator('#conversion-site-tree summary').filter({hasText:'Getting started · /docs/guide'}).waitFor();
   assert.equal(await page.locator('#conversion-visitor-map .leaflet-interactive').count(),1);
   assert.equal(await page.locator('#conversion-visitor-map .leaflet-control-attribution a[href="https://www.openstreetmap.org/copyright"]').isVisible(),true);
   assert.equal(await page.locator('#conversion-visitor-map img.leaflet-tile').first().getAttribute('referrerpolicy'),'origin');
   await page.locator('#conversion-visitor-map .leaflet-interactive').click();
   await page.locator('.leaflet-popup').getByText('192.0.2.9',{exact:true}).waitFor();
   assert.ok(await page.locator('#conversion-visit-calendar .activity-calendar-day').count()>=365);
   assert.deepEqual(await page.locator('#conversion-visit-calendar .activity-calendar-legend .activity-calendar-day').evaluateAll(swatches=>swatches.map(swatch=>getComputedStyle(swatch).backgroundColor)),['rgb(235, 237, 240)','rgb(33, 110, 57)']);
   await page.locator('#conversion-visit-calendar .activity-calendar-grid .activity-calendar-day[data-level=\"4\"]').click();
   assert.ok((await page.locator('#conversion-visit-calendar').textContent()).includes('1 Visitors'));
   assert.equal(await page.locator('#conversion-dashboard').evaluate(element=>element.scrollWidth<=element.clientWidth),true,'map/calendar overflow mobile');
   await page.locator('#conversion-behaviour > details > summary').click();
   await page.locator('#conversion-behaviour summary').filter({hasText:/^Clicks$/}).click();
   await page.getByRole('button',{name:'Open heatmap',exact:true}).click();
   const previewFrame=page.frameLocator('.conversion-heatmap-frame');
   await previewFrame.locator('[data-preview-key="key:download"]').waitFor();
   assert.equal(await previewFrame.locator('script').count(),0);
   assert.equal(await previewFrame.locator('input').evaluateAll(inputs=>inputs.every(input=>input.value==='')),true);
   assert.equal(await previewFrame.locator('textarea').inputValue(),'');
   assert.equal(await previewFrame.locator('[data-preview-key="key:download"]').evaluate(element=>element.style.backgroundColor.includes('255')),true);
   await page.locator('summary').filter({hasText:/^Sessions$/}).click();
   await page.locator('#conversion-sessions').getByRole('button',{name:'Play',exact:true}).click();
   await page.locator('#conversion-replay-player iframe').waitFor();
   const replayFrame=page.frameLocator('#conversion-replay-player iframe');
   await replayFrame.locator('[data-preview-key="key:download"]').waitFor();
   assert.equal(await replayFrame.locator('script').count(),0);
   assert.equal(await replayFrame.locator('input').evaluateAll(inputs=>inputs.every(input=>input.value==='')),true);
   assert.equal(await replayFrame.locator('textarea').inputValue(),'');
   await page.locator('#conversion-replay-player').getByRole('button',{name:'Play',exact:true}).click();
   await page.locator('#conversion-replay-player p').filter({hasText:'2 / 2'}).waitFor();
   await page.locator('summary').filter({hasText:/^Page change impact$/}).click();
   assert.ok((await page.locator('#conversion-page-impact').textContent()).includes('Download for Linux'));
   await page.locator('#conversion-sources > details > summary').click();
   assert.equal(await page.locator('#conversion-sources a').getAttribute('href'),'https://github.com/sitebrush/releases?tag=v1');
   await page.locator('summary').filter({hasText:/^Goals and funnels$/}).click();
   assert.ok((await page.locator('#conversion-dashboard').textContent()).includes('Conversion is the share of visitors'));
   const initialGoalCount = await page.locator('.conversion-editor').count();
   await page.getByRole('button',{name:'Add goal',exact:true}).click();
   assert.equal(await page.locator('.conversion-editor').count(),initialGoalCount+1);
   const suggestedGoalsButton = page.getByRole('button',{name:'Add goals from site structure',exact:true});
   if (await suggestedGoalsButton.isEnabled()) {
    await suggestedGoalsButton.click();
    assert.equal(await page.locator('.conversion-editor').count(),initialGoalCount+2);
   }
   assert.equal(await suggestedGoalsButton.isEnabled(),false,'existing suggested goal must not be duplicated');
   await page.locator('#conversion-save').click();
   try {
    await page.locator('#conversion-save-status').filter({hasText:'Saved. Reports refresh within one minute.'}).waitFor();
   } catch (saveError) {
    throw new Error(saveError.message+'\nConfiguration status: '+await page.locator('#conversion-save-status').textContent());
   }
   await page.screenshot({path:path.join(os.tmpdir(),'sitebrush-analytics-'+viewport.width+'.png'),fullPage:true});
   assert.deepEqual(errors,[]);
   console.log('Analytics browser '+viewport.width+': funnels, tree, sandbox heatmap, privacy, constructor and WebSocket save passed');
   await context.close();
  }
  const visitorContext = await browser.newContext({userAgent:'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36'});
  await visitorContext.addInitScript(session=>localStorage.setItem('sitebrush.analytics.session',JSON.stringify({identity:session,last:Date.now()})),process.env.SITEBRUSH_ANALYTICS_SAMPLE_SESSION);
  const visitorPage = await visitorContext.newPage();
  const sentFrames = []; let replayConfigured, batchAcknowledged; const configured=new Promise(resolve=>{replayConfigured=resolve;}); const acknowledged=new Promise(resolve=>{batchAcknowledged=resolve;});
  visitorPage.on('websocket',socket=>{socket.on('framesent',frame=>{sentFrames.push(JSON.parse(frame.payload));});socket.on('framereceived',frame=>{const response=JSON.parse(frame.payload);if(response.operation==='rules'&&response.replay.enabled)replayConfigured();if(response.operation==='ack'&&response.accepted)batchAcknowledged();});});
  await visitorPage.goto(process.env.SITEBRUSH_ANALYTICS_URL+'/?utm_source=github&token=url-secret');
  await visitorPage.waitForFunction(()=>!!window.SiteBrushAnalytics);
  await configured; await acknowledged;
  await visitorPage.locator('#name').fill('visitor-secret');
  await visitorPage.locator('#plan').selectOption('pro');
  await visitorPage.locator('#password').fill('password-secret');
  await visitorPage.evaluate(()=>{window.SiteBrushAnalytics.event('download');window.SiteBrushAnalytics.formResult('contact',true);});
  const beaconRequest = visitorPage.waitForRequest(request=>request.url().includes('/_sitebrush/analytics')&&request.method()==='POST');
  await visitorPage.evaluate(()=>window.dispatchEvent(new Event('pagehide')));
  const beacon = await beaconRequest;
  const beaconResponse = await beacon.response();
  assert.equal(beaconResponse.status(),204,'production endpoint rejected unload batch');
  assert.ok(sentFrames.length<=1,'collector sends per-action requests');
  console.log('Production collector: initial WebSocket batch, form/custom events, unload beacon and secret exclusion passed');
  await visitorContext.close();
 }finally{await browser.close();}
})().catch(error=>{console.error(error);process.exitCode=1;});
