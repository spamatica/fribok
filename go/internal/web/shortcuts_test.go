package web

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/dop251/goja"
)

func TestNavShortcutKeyParsing(t *testing.T) {
	src, err := staticFS.ReadFile("static/nav-shortcuts.js")
	if err != nil {
		t.Fatal(err)
	}
	vm := goja.New()
	if _, err := vm.RunString(string(src)); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ expr, want string }{
		// Plain single letters, with or without Shift.
		{`NavShortcuts.keyFor({code:"KeyK", key:"k"})`, "k"},
		{`NavShortcuts.keyFor({code:"KeyV", key:"v"})`, "v"},
		{`NavShortcuts.keyFor({shiftKey:true, code:"KeyV", key:"V"})`, "v"},
		{`NavShortcuts.keyFor({code:"KeyN", key:"n"})`, "n"},
		// Alt+Shift+letter remains a fallback.
		{`NavShortcuts.keyFor({altKey:true, shiftKey:true, code:"KeyK"})`, "k"},
		{`NavShortcuts.keyFor({altKey:true, shiftKey:true, key:"S"})`, "s"},
		// Alt alone is reserved by the browser menu; other modifiers are ignored.
		{`NavShortcuts.keyFor({altKey:true, code:"KeyK"})`, ""},
		{`NavShortcuts.keyFor({ctrlKey:true, code:"KeyK"})`, ""},
		{`NavShortcuts.keyFor({ctrlKey:true, shiftKey:true, code:"KeyK"})`, ""},
		{`NavShortcuts.keyFor({altKey:true, shiftKey:true, ctrlKey:true, code:"KeyK"})`, ""},
		{`NavShortcuts.keyFor({altKey:true, shiftKey:true, metaKey:true, code:"KeyK"})`, ""},
		{`NavShortcuts.keyFor({isComposing:true, code:"KeyK"})`, ""},
		{`NavShortcuts.keyFor({code:"Digit1", key:"1"})`, ""},
		{`NavShortcuts.keyFor({code:"Enter", key:"Enter"})`, ""},
		// Fields, editors and anything inside a form block shortcuts.
		{`NavShortcuts.isEditable({tagName:"INPUT"})`, "true"},
		{`NavShortcuts.isEditable({tagName:"TEXTAREA"})`, "true"},
		{`NavShortcuts.isEditable({tagName:"SELECT"})`, "true"},
		{`NavShortcuts.isEditable({tagName:"DIV", isContentEditable:true})`, "true"},
		{`NavShortcuts.isEditable({tagName:"BUTTON", parentNode:{tagName:"FORM"}})`, "true"},
		{`NavShortcuts.isEditable({tagName:"BODY"})`, "false"},
		{`NavShortcuts.inForm({tagName:"DIV", parentNode:{tagName:"FORM"}})`, "true"},
		{`NavShortcuts.inForm({tagName:"BODY"})`, "false"},
	}
	for _, c := range cases {
		v, err := vm.RunString(c.expr)
		if err != nil {
			t.Fatalf("%s: %v", c.expr, err)
		}
		if v.String() != c.want {
			t.Errorf("%s = %q, want %q", c.expr, v.String(), c.want)
		}
	}
}

func TestKeyboardShortcutMarkupAndScript(t *testing.T) {
	c := newClient(t)
	c.importSmall()

	pages := map[string][]string{
		"/": {
			`data-shortcut="f" aria-keyshortcuts="F"`,
			`data-shortcut="k" aria-keyshortcuts="K"`,
			"Företag <kbd class=\"shortcut\"",
			"Kontoplansmallar <kbd class=\"shortcut\"",
		},
		"/c/1/": {
			`data-shortcut="k" aria-keyshortcuts="K"`,
			`data-shortcut="v" aria-keyshortcuts="V"`,
			`data-shortcut="h" aria-keyshortcuts="H"`,
			`data-shortcut="s" aria-keyshortcuts="S"`,
			`data-shortcut="r" aria-keyshortcuts="R"`,
			`data-shortcut="b" aria-keyshortcuts="B"`,
			`Kontoplan <kbd class="shortcut"`,
			`Balansräkning <kbd class="shortcut"`,
		},
		"/c/1/vouchers": {
			`data-shortcut="n" aria-keyshortcuts="N"`,
			`Ny verifikation <kbd class="shortcut"`,
		},
	}
	for path, wants := range pages {
		code, body := c.get(path)
		if code != http.StatusOK {
			t.Fatalf("%s: %d", path, code)
		}
		for _, want := range wants {
			if !strings.Contains(body, want) {
				t.Errorf("%s lacks %q", path, want)
			}
		}
		if !strings.Contains(body, `<script src="/static/nav-shortcuts.js"></script>`) {
			t.Errorf("%s lacks the shortcut script", path)
		}
	}

	// The script is served as JavaScript.
	req, _ := http.NewRequest("GET", c.base+"/static/nav-shortcuts.js", nil)
	resp, err := c.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(resp.Header.Get("Content-Type"), "javascript") ||
		!strings.Contains(string(body), "NavShortcuts") {
		t.Errorf("shortcut script: %d %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
}

func TestShortcutHintsAreInMarkup(t *testing.T) {
	c := newClient(t)
	c.importSmall()
	_, body := c.get("/c/1/")
	for _, tc := range []struct{ label, key string }{
		{"Kontoplan", "K"}, {"Verifikationer", "V"}, {"Huvudbok", "H"},
		{"Saldobalans", "S"}, {"Resultaträkning", "R"}, {"Balansräkning", "B"},
	} {
		want := fmt.Sprintf("%s <kbd class=\"shortcut\" aria-hidden=\"true\">%s</kbd>", tc.label, tc.key)
		if !strings.Contains(body, want) {
			t.Errorf("missing shortcut hint %q", want)
		}
	}
}

func TestNavShortcutDispatch(t *testing.T) {
	src, err := staticFS.ReadFile("static/nav-shortcuts.js")
	if err != nil {
		t.Fatal(err)
	}
	vm := goja.New()
	prelude := `
var clicked = "";
var prevented = false;
var handler = null;
var document = {
  readyState: "complete",
  addEventListener: function (type, fn) { if (type === "keydown") handler = fn; },
  querySelector: function (sel) {
    if (sel === '[data-shortcut="v"]') {
      return {click: function () { clicked = "v"; }};
    }
    return null;
  }
};
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
	// A plain letter on a normal page clicks the matching link.
	got := run(`clicked = ""; prevented = false;
		handler({code: "KeyV", key: "v", target: {tagName: "BODY"}, preventDefault: function () { prevented = true; }});
		clicked + "/" + prevented`)
	if got != "v/true" {
		t.Errorf("single-key dispatch = %q, want v/true", got)
	}
	// Alt+Shift+letter remains a fallback.
	got = run(`clicked = ""; prevented = false;
		handler({altKey: true, shiftKey: true, code: "KeyV", target: {tagName: "BODY"}, preventDefault: function () { prevented = true; }});
		clicked + "/" + prevented`)
	if got != "v/true" {
		t.Errorf("fallback dispatch = %q, want v/true", got)
	}
	// Typing in a field must not navigate.
	got = run(`clicked = ""; prevented = false;
		handler({code: "KeyV", key: "v", target: {tagName: "INPUT"}, preventDefault: function () { prevented = true; }});
		clicked + "/" + prevented`)
	if got != "/false" {
		t.Errorf("editable dispatch = %q, want /false", got)
	}
	// Focus on a button inside the voucher form must not navigate either.
	got = run(`clicked = ""; prevented = false;
		handler({code: "KeyV", key: "v", target: {tagName: "BUTTON", parentNode: {tagName: "FORM"}}, preventDefault: function () { prevented = true; }});
		clicked + "/" + prevented`)
	if got != "/false" {
		t.Errorf("form dispatch = %q, want /false", got)
	}
	// Plain Alt+letter is left to the browser menu.
	got = run(`clicked = ""; prevented = false;
		handler({altKey: true, code: "KeyV", target: {tagName: "BODY"}, preventDefault: function () { prevented = true; }});
		clicked + "/" + prevented`)
	if got != "/false" {
		t.Errorf("plain-alt dispatch = %q, want /false", got)
	}
	// A key without a target is left alone.
	got = run(`clicked = ""; prevented = false;
		handler({code: "KeyX", key: "x", target: {tagName: "BODY"}, preventDefault: function () { prevented = true; }});
		clicked + "/" + prevented`)
	if got != "/false" {
		t.Errorf("unknown dispatch = %q, want /false", got)
	}
}
