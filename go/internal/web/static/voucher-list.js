// Verifikationer page: the Rätta panel is a small dialog.
//   - opening one closes the others (the handler runs before the browser
//     toggles the summary, so keyboard activation works too),
//   - clicking a button in the panel closes it,
//   - clicking anywhere outside an open panel closes it.
var VoucherList = (function () {
  'use strict';

  function closeOthers(mine) {
    var open = document.querySelectorAll('details.correct[open]');
    for (var i = 0; i < open.length; i++) {
      if (open[i] !== mine) open[i].removeAttribute('open');
    }
  }

  function handleClick(e) {
    var target = e.target;
    if (!target || !target.closest) return;

    var summary = target.closest('details.correct > summary');
    if (summary) {
      closeOthers(summary.parentNode);
      return;
    }

    var panel = target.closest('details.correct');
    if (panel) {
      // Keep the panel open while editing, but close it when an action button
      // is clicked. A cancelled confirm() prevents the default, and the panel
      // stays open in that case.
      if (target.closest('button, a') && !e.defaultPrevented) {
        panel.removeAttribute('open');
      }
      return;
    }

    closeOthers(null);
  }

  document.addEventListener('click', handleClick);
  return {handleClick: handleClick};
})();
