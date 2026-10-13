const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const documentEnvironment = {};
vm.createContext(documentEnvironment);
vm.runInContext(fs.readFileSync(path.join(__dirname, '../../web/static/visual_editor_document.js'), 'utf8'), documentEnvironment);

for (const [htmlText, expectedBody, expectedShell] of [
  ['<p>fragment</p>', '<p>fragment</p>', false],
  ['<!DOCTYPE html><HTML><HEAD><META content="a > b"><TITLE>Title</TITLE></HEAD><BODY class="original">body</BODY></HTML>', 'body', true],
  ['<!-- <body>comment</body> --><html><head><script>const sample="<body>wrong</body>";</script><style>.example:before{content:"<body>"}</style></head><body>right</body></html>', 'right', true],
  ['<html><head><title>Example</title></head>implicit body</html>', 'implicit body', true],
  ['<html><head><title>Empty</title></head></html>', '', true],
  ['<html><head><script>const sample="</script-not-a-tag><body>wrong</body>";</script></head><body>right</body></html>', 'right', true],
  ['<html><head><template><body>template content</body></template></head><body>real body</body></html>', 'real body', true]
]) {
  const documentEnvelope = documentEnvironment.visualEditorDocumentEnvelope(htmlText);
  assert.equal(documentEnvelope.bodyHTML, expectedBody);
  assert.equal(documentEnvelope.hasDocumentShell, expectedShell);
  assert.equal(documentEnvelope.prefix + documentEnvelope.bodyHTML + documentEnvelope.suffix, htmlText);
  assert.equal(documentEnvelope.prefix + 'changed' + documentEnvelope.suffix, htmlText.replace(expectedBody || '</head>', expectedBody ? 'changed' : '</head>changed'));
}
console.log('Document envelope regressions passed');
