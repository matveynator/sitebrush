/* Preserve the source envelope; browser serialization is only used for the
   editable body, never for untouched document metadata. */
function visualEditorTagEnd(htmlText, tagStart) {
  let quoteCharacter = '';
  for (let characterIndex = tagStart + 1; characterIndex < htmlText.length; characterIndex++) {
    const currentCharacter = htmlText[characterIndex];
    if (quoteCharacter) {
      if (currentCharacter === quoteCharacter) {
        quoteCharacter = '';
      }
    } else if (currentCharacter === '"' || currentCharacter === "'") {
      quoteCharacter = currentCharacter;
    } else if (currentCharacter === '>') {
      return characterIndex + 1;
    }
  }
  return htmlText.length;
}

function visualEditorDocumentTokens(htmlText) {
  const documentTokens = [];
  const lowerHTML = htmlText.toLowerCase();
  let characterIndex = 0;
  let rawTextTag = '';
  while (characterIndex < htmlText.length) {
    const tagStart = htmlText.indexOf('<', characterIndex);
    if (tagStart < 0) {
      break;
    }
    if (htmlText.startsWith('<!--', tagStart)) {
      const commentEnd = htmlText.indexOf('-->', tagStart + 4);
      characterIndex = commentEnd < 0 ? htmlText.length : commentEnd + 3;
      continue;
    }
    const tagMatch = /^<\s*(\/?)\s*([a-z][a-z0-9:-]*|!doctype)(?=\s|\/?>)/i.exec(htmlText.slice(tagStart));
    if (!tagMatch) {
      characterIndex = tagStart + 1;
      continue;
    }
    const tagName = tagMatch[2].toLowerCase();
    const closingTag = tagMatch[1] === '/';
    if (rawTextTag && (!closingTag || tagName !== rawTextTag)) {
      characterIndex = tagStart + 1;
      continue;
    }
    const tagEnd = visualEditorTagEnd(htmlText, tagStart);
    documentTokens.push({ name: tagName, closing: closingTag, start: tagStart, end: tagEnd });
    characterIndex = tagEnd;
    if (closingTag) {
      rawTextTag = '';
    } else if (['script', 'style', 'title', 'textarea', 'xmp', 'iframe', 'noembed', 'noframes'].includes(tagName)) {
      rawTextTag = tagName;
      const rawClosingStart = lowerHTML.indexOf('</' + rawTextTag, characterIndex);
      characterIndex = rawClosingStart < 0 ? htmlText.length : rawClosingStart;
    }
  }
  return documentTokens;
}

function visualEditorDocumentEnvelope(htmlText) {
  const documentTokens = visualEditorDocumentTokens(htmlText);
  const headElementNames = new Set(['base', 'basefont', 'bgsound', 'link', 'meta', 'noframes', 'script', 'style', 'template', 'title']);
  let bodyStart = -1;
  let bodyEnd = htmlText.length;
  let shellStart = -1;
  let implicitBodyStart = -1;
  let implicitHeadEnd = -1;
  let hasDocumentShell = false;
  let templateDepth = 0;
  for (const documentToken of documentTokens) {
    if (documentToken.name === 'template') {
      templateDepth = Math.max(0, templateDepth + (documentToken.closing ? -1 : 1));
      continue;
    }
    if (templateDepth > 0) {
      continue;
    }
    if (['html', 'head', 'body', '!doctype'].includes(documentToken.name)) {
      hasDocumentShell = true;
    }
    if (!documentToken.closing && documentToken.name === 'html') {
      shellStart = documentToken.end;
    }
    if (documentToken.closing && documentToken.name === 'head' && bodyStart < 0) {
      shellStart = documentToken.end;
      implicitHeadEnd = documentToken.end;
    }
    if (!documentToken.closing && documentToken.name === 'body' && bodyStart < 0) {
      bodyStart = documentToken.end;
    }
    if (bodyStart < 0 && implicitBodyStart < 0) {
      if (headElementNames.has(documentToken.name) || documentToken.name === 'head') {
        implicitHeadEnd = Math.max(implicitHeadEnd, documentToken.end);
      } else if (!documentToken.closing && !['html', '!doctype', 'body'].includes(documentToken.name)) {
        implicitBodyStart = documentToken.start;
      }
    }
    if (documentToken.closing && (documentToken.name === 'body' || documentToken.name === 'html')) {
      bodyEnd = Math.min(bodyEnd, documentToken.start);
    }
  }
  if (!hasDocumentShell) {
    return { hasDocumentShell: false, prefix: '', suffix: '', bodyHTML: htmlText };
  }
  const contentStart = bodyStart >= 0 ? bodyStart
    : implicitBodyStart >= 0 ? implicitBodyStart
    : implicitHeadEnd >= 0 ? implicitHeadEnd
    : shellStart >= 0 ? shellStart
    : htmlText.length;
  const contentEnd = Math.max(contentStart, bodyEnd);
  return {
    hasDocumentShell: true,
    prefix: htmlText.slice(0, contentStart),
    suffix: htmlText.slice(contentEnd),
    bodyHTML: htmlText.slice(contentStart, contentEnd)
  };
}