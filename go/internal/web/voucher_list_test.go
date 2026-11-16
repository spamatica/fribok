package web

import (
	"testing"

	"github.com/dop251/goja"
)

func TestVoucherListPanelClosing(t *testing.T) {
	src, err := staticFS.ReadFile("static/voucher-list.js")
	if err != nil {
		t.Fatal(err)
	}
	vm := goja.New()
	prelude := `
var openList = [];
var handler = null;
var document = {
  addEventListener: function (type, fn) { if (type === "click") handler = fn; },
  querySelectorAll: function (sel) { return sel === 'details.correct[open]' ? openList : []; }
};
function panel() {
  return {
    removed: 0,
    removeAttribute: function (name) { if (name === "open") this.removed++; },
    closest: function () { return null; }
  };
}
`
	if _, err := vm.RunString(prelude + "\n" + string(src)); err != nil {
		t.Fatal(err)
	}
	run := func(script string) string {
		t.Helper()
		v, err := vm.RunString(script)
		if err != nil {
			t.Fatal(err)
		}
		return v.String()
	}

	// Clicking outside every panel closes all open ones.
	if got := run(`
		var p1 = panel(), p2 = panel();
		openList = [p1, p2];
		VoucherList.handleClick({target: {closest: function () { return null; }}, defaultPrevented: false});
		p1.removed + "/" + p2.removed`); got != "1/1" {
		t.Errorf("outside click = %q, want 1/1", got)
	}

	// A button inside the panel closes that panel.
	if got := run(`
		var p = panel();
		var target = {closest: function (sel) {
			if (sel === 'details.correct') return p;
			if (sel === 'button, a') return {};
			return null;
		}};
		VoucherList.handleClick({target: target, defaultPrevented: false});
		String(p.removed)`); got != "1" {
		t.Errorf("button click = %q, want 1", got)
	}

	// A cancelled confirm() (defaultPrevented) leaves it open.
	if got := run(`
		var p = panel();
		var target = {closest: function (sel) {
			if (sel === 'details.correct') return p;
			if (sel === 'button, a') return {};
			return null;
		}};
		VoucherList.handleClick({target: target, defaultPrevented: true});
		String(p.removed)`); got != "0" {
		t.Errorf("cancelled button click = %q, want 0", got)
	}

	// Editing in a field keeps the panel open.
	if got := run(`
		var p = panel();
		var target = {closest: function (sel) {
			if (sel === 'details.correct') return p;
			return null;
		}};
		VoucherList.handleClick({target: target, defaultPrevented: false});
		String(p.removed)`); got != "0" {
		t.Errorf("field click = %q, want 0", got)
	}

	// Clicking a summary closes the other panels but not the clicked one.
	if got := run(`
		var p = panel(), other = panel();
		var summary = {
			parentNode: p,
			closest: function (sel) { return sel === 'details.correct > summary' ? summary : null; }
		};
		openList = [p, other];
		VoucherList.handleClick({target: summary, defaultPrevented: false});
		p.removed + "/" + other.removed`); got != "0/1" {
		t.Errorf("summary click = %q, want 0/1", got)
	}
}
