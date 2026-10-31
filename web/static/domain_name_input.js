(function initializeDomainNameInput() {
  'use strict';

  const cyrillicLetters = Object.freeze({
    а: 'a', б: 'b', в: 'v', г: 'g', д: 'd', е: 'e', ё: 'yo', ж: 'zh', з: 'z', и: 'i', й: 'y', к: 'k', л: 'l', м: 'm', н: 'n', о: 'o', п: 'p', р: 'r', с: 's', т: 't', у: 'u', ф: 'f', х: 'h', ц: 'ts', ч: 'ch', ш: 'sh', щ: 'shch', ъ: '', ы: 'y', ь: '', э: 'e', ю: 'yu', я: 'ya',
    є: 'ye', і: 'i', ї: 'yi', ґ: 'g', ў: 'u', ә: 'a', ғ: 'g', қ: 'q', ң: 'n', ө: 'o', ұ: 'u', ү: 'u', һ: 'h', ҷ: 'j'
  });

  function normalize(name) {
    let normalizedName = '';
    for (const letter of String(name || '').toLowerCase().normalize('NFKC')) {
      if (/\s/u.test(letter)) continue;
      normalizedName += Object.prototype.hasOwnProperty.call(cyrillicLetters, letter) ? cyrillicLetters[letter] : letter;
    }
    return normalizedName;
  }

  function valid(name) {
    return /^[a-z0-9](?:[a-z0-9-]{0,22}[a-z0-9])?$/.test(name);
  }

  function address(name, parentDomain) {
    return name && parentDomain ? name + '.' + parentDomain : '';
  }

  window.SiteBrushDomainNameInput = Object.freeze({normalize: normalize, valid: valid, address: address});
})();
