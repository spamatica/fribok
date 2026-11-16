// Keyboard shortcuts for the top navigation and the "Ny verifikation" button.
//
// A bare letter opens the menu item marked with that letter in data-shortcut;
// Alt+Shift+letter is a fallback where a page wants a modified shortcut. Plain
// Alt+letter is not used because browsers reserve it for their menus. Shortcuts
// are ignored while typing in a field/editor or while focus is inside a form,
// so a half-entered voucher is not lost.
var NavShortcuts = (function () {
  'use strict';

  function inForm(el) {
    for (; el; el = el.parentNode) {
      if (String(el.tagName || '').toLowerCase() === 'form') return true;
    }
    return false;
  }

  function isEditable(el) {
    if (!el) return false;
    var tag = String(el.tagName || '').toLowerCase();
    return tag === 'input' || tag === 'textarea' || tag === 'select' ||
      !!el.isContentEditable || inForm(el);
  }

  // Return the shortcut letter or "" when the event is not a shortcut. Using
  // code makes the letter independent of keyboard layout; key is a fallback.
  function keyFor(e) {
    if (!e || e.metaKey || e.isComposing) return '';
    if (e.altKey && !e.shiftKey) return ''; // reserved by the browser menu
    if (e.altKey && !e.ctrlKey && e.shiftKey) {
      var m = /^Key([A-Z])$/.exec(String(e.code || ''));
      if (m) return m[1].toLowerCase();
      var key = String(e.key || '');
      return /^[a-zA-Z]$/.test(key) ? key.toLowerCase() : '';
    }
    if (e.ctrlKey || e.altKey) return '';
    var code = /^Key([A-Z])$/.exec(String(e.code || ''));
    if (code) return code[1].toLowerCase();
    var key = String(e.key || '');
    return /^[a-zA-Z]$/.test(key) ? key.toLowerCase() : '';
  }

  function targetFor(key) {
    if (!key || typeof document === 'undefined') return null;
    return document.querySelector('[data-shortcut="' + key + '"]');
  }

  function init() {
    document.addEventListener('keydown', function (e) {
      if (e.defaultPrevented || e.repeat || isEditable(e.target)) return;
      var key = keyFor(e);
      var target = targetFor(key);
      if (!target) return;
      e.preventDefault();
      target.click();
    });
  }

  if (typeof document !== 'undefined') {
    if (document.readyState === 'loading') {
      document.addEventListener('DOMContentLoaded', init);
    } else {
      init();
    }
  }

  return {keyFor: keyFor, isEditable: isEditable, inForm: inForm, targetFor: targetFor};
})();
