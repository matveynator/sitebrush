const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const controls = new Map();
function control(identifier) {
  if (!controls.has(identifier)) controls.set(identifier, {disabled: false, textContent: '', attributes: {}, setAttribute(name, text) { this.attributes[name] = text; }});
  return controls.get(identifier);
}
const requests = [];
const environment = {
  URL, FormData, Blob, AbortController,
  currentPagePath: '/editor',
  visualEditorFrameBaseURL: () => 'https://site.example/editor',
  editUIText: {invalidImagePosition: 'Use bounded percentages or position keywords', imagePreviewFailed: 'Preview failed', imageResizeFailed: 'Preparation failed'},
  document: {
    getElementById: control,
    querySelector: () => ({value: 'csrf-token'}),
    querySelectorAll: () => [control('imagePropertiesPosition')]
  },
  fetch: async (requestURL, options) => {
    const address = String(requestURL);
    requests.push({address, options});
    if (address.startsWith('https://images.example')) throw new TypeError('Cross-origin image host does not allow CORS');
    if (address === 'https://site.example/local.png') return {ok: true, blob: async () => new Blob(['pixels'], {type: 'image/png'})};
    assert.equal(address, '/editor?files&prepare_image');
    return {ok: true, json: async () => ({preview: {url: '/p/prepared.png'}})};
  }
};
vm.createContext(environment);
vm.runInContext(fs.readFileSync(path.join(__dirname, '../../web/static/visual_editor_extensions.js'), 'utf8'), environment);

async function runImageReplacementRegressions() {
  for (const [position, coordinates] of [
    ['25% 75%', [.25, .75]], ['.5% 100%', [.005, 1]], ['left top', [0, 0]],
    ['top left', [0, 0]], ['bottom right', [1, 1]], ['center left', [0, .5]],
    ['top center', [.5, 0]], ['center', [.5, .5]], ['bottom', [.5, 1]],
    ['0px 0', [0, 0]], ['RIGHT BOTTOM', [1, 1]]
  ]) assert.deepEqual(Array.from(environment.imageCropPosition(position)), coordinates, position);

  const rejectedPositions = ['25px 75%', 'right 10px bottom 20px', 'calc(50% - 10px) 50%', '-10% 50%', '150% 50%', 'left right', 'top bottom', '', '50% 50% 50%'];
  for (const position of rejectedPositions) assert.throws(() => environment.imageCropPosition(position), /bounded percentages/);

  const draft = {source: 'https://images.example/photo.png', position: '25% 75%', width: 50, height: 50, fit: 'cover', fullSizeReferences: [{}], preparationController: new AbortController()};
  const prepared = await environment.prepareImageDraftReplacement(draft);
  assert.equal(prepared.preview.url, '/p/prepared.png');
  assert.equal(requests.length, 1, 'an external source never needs a browser CORS download');
  const externalRequest = requests[0].options.body;
  assert.equal(externalRequest.get('image_source'), draft.source);
  assert.equal(externalRequest.get('upload_files'), null);
  assert.equal(externalRequest.get('image_position_x'), '0.25');
  assert.equal(externalRequest.get('image_position_y'), '0.75');
  assert.equal(externalRequest.get('image_full'), '1');
  assert.equal(externalRequest.get('account_csrf'), 'csrf-token');

  requests.length = 0;
  await environment.prepareImageDraftReplacement({...draft, source: '/local.png'});
  assert.equal(requests.length, 2);
  assert.equal(requests[1].options.body.get('image_source'), null);
  assert.equal(requests[1].options.body.get('upload_files').type, 'image/png');

  requests.length = 0;
  await environment.prepareImageDraftReplacement({...draft, replacementFile: new Blob(['pixels'], {type: 'image/png'})});
  assert.equal(requests.length, 1, 'uploaded replacements use the selected file directly');

  environment.draft = {...draft, error: '', loadingPreview: false, preparing: false};
  vm.runInContext('sitebrushEditorModel.imageDraft = draft', environment);
  for (const position of rejectedPositions) {
    environment.draft.position = position;
    environment.renderImagePropertiesStatus();
    assert.equal(control('imagePropertiesSave').disabled, true, position);
    assert.equal(control('imagePropertiesPosition').attributes['aria-invalid'], 'true');
    assert.match(control('imagePropertiesError').textContent, /bounded percentages/);
  }
  environment.draft.position = '25% 75%';
  environment.renderImagePropertiesStatus();
  assert.equal(control('imagePropertiesSave').disabled, false);
  assert.equal(control('imagePropertiesPosition').attributes['aria-invalid'], 'false');
  environment.draft.error = 'Image preview failed';
  environment.renderImagePropertiesStatus();
  assert.equal(control('imagePropertiesSave').disabled, true, 'position validation does not erase image errors');
  console.log('Image source and crop-position regressions passed');
}
runImageReplacementRegressions().catch(error => { console.error(error); process.exitCode = 1; });
