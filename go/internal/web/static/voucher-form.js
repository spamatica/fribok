// Keyboard handling for the voucher form.
//
//   Enter       moves to the next field instead of submitting.
//   Ctrl+Enter  books the voucher.
//   Enter in an account field fills in the amount that balances the voucher
//               and goes to the right column: after a debit of 200 the next
//               row gets a credit of 200 (and the other way round).
//   Enter in a debit field skips the credit column.
//   Enter or Tab in the last field of the last row adds a row.
var VoucherForm = (function () {
  'use strict';

  // "200", "200.5", "1 234,50" -> öre; null if it is not an amount.
  function parseOre(s) {
    s = String(s).replace(/[\s ]/g, '').replace(',', '.');
    var m = /^(\d+)(?:\.(\d{1,2}))?$/.exec(s);
    if (!m) return null;
    var frac = m[2] ? (m[2] + '0').slice(0, 2) : '00';
    return parseInt(m[1], 10) * 100 + parseInt(frac, 10);
  }

  function fmtOre(n) {
    var sign = n < 0 ? '-' : '';
    n = Math.abs(n);
    var k = n % 100;
    return sign + Math.floor(n / 100) + '.' + (k < 10 ? '0' : '') + k;
  }

  function blank(s) { return String(s).trim() === ''; }

  // Which column and amount to suggest for a row whose account was just
  // entered. others: the other rows as [{debit, credit}] (strings); current:
  // the row itself. The suggestion is the amount that balances the voucher,
  // in the opposite column. Returns {field: 'debit'|'credit', value: string};
  // value is '' when there is nothing to suggest (balanced, or the row
  // already has an amount, or some other row has an unreadable amount).
  function balancing(others, current) {
    var none = {field: 'debit', value: ''};
    if (!blank(current.debit) || !blank(current.credit)) return none;
    var sum = 0;
    for (var i = 0; i < others.length; i++) {
      var d = blank(others[i].debit) ? 0 : parseOre(others[i].debit);
      var c = blank(others[i].credit) ? 0 : parseOre(others[i].credit);
      if (d === null || c === null) return none;
      sum += d - c;
    }
    if (sum > 0) return {field: 'credit', value: fmtOre(sum)};
    if (sum < 0) return {field: 'debit', value: fmtOre(-sum)};
    return none;
  }

  function init() {
    var form = document.getElementById('voucher-form');
    var tbody = document.getElementById('rows');
    var tpl = document.getElementById('row-template');
    if (!form || !tbody || !tpl) return;
    var button = form.querySelector('button');

    function field(tr, name) { return tr.querySelector('input[name=' + name + ']'); }
    function rowOf(el) {
      for (var n = el; n; n = n.parentNode) if (n.parentNode === tbody) return n;
      return null;
    }
    function rowEmpty(tr) {
      var names = ['acct', 'debit', 'credit', 'ltext'];
      for (var i = 0; i < names.length; i++) if (!blank(field(tr, names[i]).value)) return false;
      return true;
    }
    function focus(el) {
      el.focus();
      try { el.select(); } catch (e) { /* e.g. date inputs */ }
    }
    function addRow() {
      tbody.appendChild(tpl.content.cloneNode(true));
      var tr = tbody.lastElementChild;
      AccountPicker.attachInput(field(tr, 'acct'));
      return tr;
    }
    function isLastField(el) {
      var tr = rowOf(el);
      return tr && el.name === 'ltext' && tr === tbody.lastElementChild;
    }

    // Enter in the account field of a row.
    function afterAccount(tr) {
      var acct = field(tr, 'acct');
      if (blank(acct.value)) { focus(field(tr, 'debit')); return; }
      var others = [];
      for (var r = tbody.firstElementChild; r; r = r.nextElementSibling) {
        if (r !== tr) others.push({debit: field(r, 'debit').value, credit: field(r, 'credit').value});
      }
      var s = balancing(others, {debit: field(tr, 'debit').value, credit: field(tr, 'credit').value});
      var target = field(tr, s.field);
      if (s.value !== '') target.value = s.value;
      focus(target);
    }

    function advance(el) {
      var tr = rowOf(el);
      if (tr) {
        if (el.name === 'acct') return afterAccount(tr);
        if (el.name === 'debit') return focus(field(tr, blank(el.value) ? 'credit' : 'ltext'));
        if (el.name === 'credit') return focus(field(tr, 'ltext'));
        // Radtext: on to the next row; after the last (non-empty) row, a new one.
        var next = tr.nextElementSibling;
        if (!next) {
          if (rowEmpty(tr)) return button.focus();
          next = addRow();
        }
        return focus(field(next, 'acct'));
      }
      var fields = form.querySelectorAll('input:not([type=hidden]), select');
      for (var i = 0; i < fields.length; i++) {
        if (fields[i] === el) return i + 1 < fields.length ? focus(fields[i + 1]) : button.focus();
      }
    }

    form.addEventListener('keydown', function (e) {
      if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) {
        e.preventDefault();
        if (form.requestSubmit) form.requestSubmit(); else form.submit();
        return;
      }
      var el = e.target;
      if (el.tagName !== 'INPUT' || e.defaultPrevented) return;
      if (e.key === 'Enter') {
        e.preventDefault();
        advance(el);
      } else if (e.key === 'Tab' && !e.shiftKey && !e.ctrlKey && !e.altKey && !e.metaKey) {
        if (isLastField(el) && !rowEmpty(rowOf(el))) {
          e.preventDefault();
          focus(field(addRow(), 'acct'));
        }
      }
    });

    var date = form.querySelector('input[name=date]');
    if (date) { date.focus(); try { date.select(); } catch (e) { /* ignore */ } }
  }

  if (typeof document !== 'undefined') {
    if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', init);
    else init();
  }
  return {parseOre: parseOre, fmtOre: fmtOre, balancing: balancing};
})();
