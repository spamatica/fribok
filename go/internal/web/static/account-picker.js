// Account picker for the voucher form: a suggestion list under every account
// field. The best match is preselected, so Tab or Enter accepts it.
//
// Ranking, best first:
//   0. the account number equals what was typed
//   1. the number starts with it
//   2. the name starts with it
//   3. a word of the name starts with it (earlier words first)
//   4. the name contains it (earlier position first)
//   5. the number contains it
// Within the same rank and position, lower account numbers come first.
var AccountPicker = (function () {
  'use strict';

  function isWordChar(c) {
    return (c >= '0' && c <= '9') || c.toLowerCase() !== c.toUpperCase();
  }

  // Index of the first word in s that starts with q, or -1.
  function wordStart(s, q) {
    for (var i = 1; i < s.length; i++) {
      if (!isWordChar(s.charAt(i - 1)) && s.indexOf(q, i) === i) return i;
    }
    return -1;
  }

  // accounts: [{n: "1930", m: "Företagskonto"}, ...]. Returns at most limit of them.
  function rank(query, accounts, limit) {
    var q = String(query).trim().toLowerCase();
    if (q === '') return [];
    var found = [];
    for (var k = 0; k < accounts.length; k++) {
      var a = accounts[k];
      var name = a.m.toLowerCase();
      var tier, pos = 0, i;
      if (a.n === q) {
        tier = 0;
      } else if (a.n.indexOf(q) === 0) {
        tier = 1;
      } else if (name.indexOf(q) === 0) {
        tier = 2;
      } else if ((i = wordStart(name, q)) >= 0) {
        tier = 3; pos = i;
      } else if ((i = name.indexOf(q)) >= 0) {
        tier = 4; pos = i;
      } else if ((i = a.n.indexOf(q)) >= 0) {
        tier = 5; pos = i;
      } else {
        continue;
      }
      found.push({a: a, tier: tier, pos: pos});
    }
    found.sort(function (x, y) {
      if (x.tier !== y.tier) return x.tier - y.tier;
      if (x.pos !== y.pos) return x.pos - y.pos;
      return x.a.n < y.a.n ? -1 : (x.a.n > y.a.n ? 1 : 0);
    });
    return found.slice(0, limit || 10).map(function (x) { return x.a; });
  }

  // The accounts offered, read once from #acct-data.
  var known = null;
  function accounts() {
    if (known) return known;
    known = {list: [], byNumber: {}};
    var lis = document.querySelectorAll('#acct-data li');
    for (var i = 0; i < lis.length; i++) {
      var a = {n: lis[i].getAttribute('data-n'), m: lis[i].getAttribute('data-m')};
      known.list.push(a);
      known.byNumber[a.n] = a;
    }
    return known;
  }

  // Adds the suggestion list to one account input (also used for rows added later).
  function attachInput(input) {
    attach(input, accounts().list, accounts().byNumber);
  }

  function attach(input, accounts, byNumber) {
    var wrap = input.parentNode;
    wrap.className += ' ac';
    var list = document.createElement('ul');
    list.className = 'ac-list';
    list.hidden = true;
    wrap.appendChild(list);
    var label = document.createElement('span');
    label.className = 'acctname';
    wrap.appendChild(label);
    var items = [], sel = 0;

    function showName() {
      var a = byNumber[input.value.trim()];
      label.textContent = a ? a.m : '';
    }
    function close() { list.hidden = true; items = []; }
    function render() {
      list.innerHTML = '';
      items.forEach(function (a, i) {
        var li = document.createElement('li');
        li.textContent = a.n + ' ' + a.m;
        if (i === sel) li.className = 'sel';
        li.addEventListener('mousedown', function (e) { e.preventDefault(); pick(a); });
        list.appendChild(li);
      });
      list.hidden = items.length === 0;
    }
    function pick(a) {
      input.value = a.n;
      close();
      showName();
    }

    input.addEventListener('input', function () {
      label.textContent = '';
      items = rank(input.value, accounts, 10);
      sel = 0;
      render();
    });
    input.addEventListener('keydown', function (e) {
      if (list.hidden || items.length === 0) return;
      if (e.key === 'ArrowDown') {
        sel = (sel + 1) % items.length; render(); e.preventDefault();
      } else if (e.key === 'ArrowUp') {
        sel = (sel + items.length - 1) % items.length; render(); e.preventDefault();
      } else if (e.key === 'Enter') {
        // Only fill in the account. The event goes on to the form's own Enter
        // handling (see voucher-form.js), which decides where to go next.
        pick(items[sel]);
      } else if (e.key === 'Tab') {
        pick(items[sel]); // focus moves on by itself
      } else if (e.key === 'Escape') {
        close(); e.preventDefault();
      }
    });
    input.addEventListener('blur', function () { close(); showName(); });
    showName();
  }

  function init() {
    var inputs = document.querySelectorAll('input[name=acct]');
    for (var j = 0; j < inputs.length; j++) attachInput(inputs[j]);
  }

  if (typeof document !== 'undefined') {
    if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', init);
    else init();
  }
  return {rank: rank, attachInput: attachInput};
})();
