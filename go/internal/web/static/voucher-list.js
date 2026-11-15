// Verifikationer page: only one Rätta panel can be open at a time.
// Opening one closes the others. The handler runs on the click on the
// summary, before the browser toggles it, so it also works with the keyboard
// (Enter and Space on a summary produce a click).
(function () {
  'use strict';
  document.addEventListener('click', function (e) {
    var summary = e.target.closest ? e.target.closest('details.correct > summary') : null;
    if (!summary) return;
    var mine = summary.parentNode;
    var open = document.querySelectorAll('details.correct[open]');
    for (var i = 0; i < open.length; i++) {
      if (open[i] !== mine) open[i].removeAttribute('open');
    }
  });
})();
