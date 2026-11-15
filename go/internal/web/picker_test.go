package web

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/dop251/goja"
)

// The ranking runs the real static/account-picker.js in a JS engine.
func rankFunc(t *testing.T) func(q string, accounts [][2]string) []string {
	t.Helper()
	src, err := staticFS.ReadFile("static/account-picker.js")
	if err != nil {
		t.Fatal(err)
	}
	vm := goja.New()
	if _, err := vm.RunString(string(src)); err != nil {
		t.Fatal(err)
	}
	return func(q string, accounts [][2]string) []string {
		var items []string
		for _, a := range accounts {
			items = append(items, fmt.Sprintf("{n:%q,m:%q}", a[0], a[1]))
		}
		v, err := vm.RunString(fmt.Sprintf(`AccountPicker.rank(%q, [%s]).map(function (a) { return a.n; }).join(",")`,
			q, strings.Join(items, ",")))
		if err != nil {
			t.Fatal(err)
		}
		if v.String() == "" {
			return nil
		}
		return strings.Split(v.String(), ",")
	}
}

var pickerAccounts = [][2]string{
	{"1930", "Företagskonto"}, {"1940", "Övriga bankkonton"}, {"2010", "Eget kapital, delägare 1"},
	{"3921", "Provisionsintäkter"}, {"3980", "Erhållna offentliga bidrag"}, {"3999", "Medlemsavgift"},
	{"4010", "Inköp varor"}, {"5000", "Lokalkostnader (gruppkonto)"}, {"5910", "Marknadsföring"},
	{"6070", "Representation"}, {"6570", "Bankkostnader"},
}

func TestAccountPickerRanking(t *testing.T) {
	rank := rankFunc(t)
	for _, c := range []struct {
		q     string
		first string
		order string // the complete expected order, if it matters
	}{
		{"m", "3999", "3999,5910"},           // names starting with m, by number: not "Medlemsavgift" after other substring hits
		{"M", "3999", "3999,5910"},           // case does not matter
		{"med", "3999", "3999"},              //
		{"bank", "6570", "6570,1940"},        // name starts with it, then a later word starts with it
		{"kost", "6570", "6570,5000"},        // contained in the name, earlier position first (4 before 5)
		{"avg", "3999", "3999"},              // contained
		{"kapital", "2010", "2010"},          // second word
		{"1930", "1930", ""},                 // exact number
		{"19", "1930", "1930,1940"},          // number prefix, by number
		{"193", "1930", "1930"},              //
		{"930", "1930", "1930"},              // number contains it
		{"ö", "1940", "1940,1930,4010,5910"}, // Swedish letters
		{" bank ", "6570", "6570,1940"},      // surrounding spaces are ignored
		{"gruppkonto", "5000", "5000"},       // word inside parentheses
	} {
		got := rank(c.q, pickerAccounts)
		if len(got) == 0 || got[0] != c.first {
			t.Errorf("rank(%q) = %v, want %s first", c.q, got, c.first)
			continue
		}
		if c.order != "" && strings.Join(got, ",") != c.order {
			t.Errorf("rank(%q) = %v, want %s", c.q, got, c.order)
		}
	}
	if got := rank("", pickerAccounts); got != nil {
		t.Errorf("empty query gave %v", got)
	}
	if got := rank("zzz", pickerAccounts); got != nil {
		t.Errorf("no match gave %v", got)
	}
	// An exact number beats a name that happens to start with the same digits.
	if got := rank("1010", [][2]string{{"1010", "Utvecklingsutgifter"}, {"4000", "1010 Särskilt"}}); got[0] != "1010" {
		t.Errorf("exact number not first: %v", got)
	}
	// At most ten suggestions.
	var many [][2]string
	for i := 0; i < 40; i++ {
		many = append(many, [2]string{fmt.Sprintf("%d", 3000+i), "Konto"})
	}
	if got := rank("konto", many); len(got) != 10 {
		t.Errorf("%d suggestions, want 10", len(got))
	}
}

func TestVoucherFormUsesThePicker(t *testing.T) {
	c := newClient(t)
	c.importSmall()
	_, page := c.get("/c/1/vouchers/new")
	if strings.Contains(page, "<datalist") || strings.Contains(page, `list="accounts"`) {
		t.Error("the form still uses a datalist")
	}
	for _, want := range []string{`<script src="/static/account-picker.js">`, `id="acct-data"`,
		`data-n="1930"`, `data-m="Medlemsavgift"`, `data-n="6570"`} {
		if !strings.Contains(page, want) {
			t.Errorf("form lacks %s", want)
		}
	}
	req, _ := http.NewRequest("GET", c.base+"/static/account-picker.js", nil)
	resp, err := c.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Type"), "javascript") || !strings.Contains(string(body), "AccountPicker") {
		t.Errorf("static script: %d %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
}

func formVM(t *testing.T) *goja.Runtime {
	t.Helper()
	src, err := staticFS.ReadFile("static/voucher-form.js")
	if err != nil {
		t.Fatal(err)
	}
	vm := goja.New()
	if _, err := vm.RunString(string(src)); err != nil {
		t.Fatal(err)
	}
	return vm
}

func TestVoucherFormAmounts(t *testing.T) {
	vm := formVM(t)
	eval := func(expr string) string {
		t.Helper()
		v, err := vm.RunString(expr)
		if err != nil {
			t.Fatalf("%s: %v", expr, err)
		}
		return v.String()
	}
	for in, want := range map[string]string{
		`"200"`: "20000", `"200.5"`: "20050", `"1 234,50"`: "123450", `"1 234,5"`: "123450",
		`"0.05"`: "5", `".5"`: "null", `"-5"`: "null", `"abc"`: "null", `"1.234"`: "null", `""`: "null", `"1,2,3"`: "null",
	} {
		if got := eval(`String(VoucherForm.parseOre(` + in + `))`); got != want {
			t.Errorf("parseOre(%s) = %s, want %s", in, got, want)
		}
	}
	for in, want := range map[string]string{"20000": "200.00", "5": "0.05", "123450": "1234.50", "-250": "-2.50", "0": "0.00"} {
		if got := eval(`VoucherForm.fmtOre(` + in + `)`); got != want {
			t.Errorf("fmtOre(%s) = %s, want %s", in, got, want)
		}
	}
	// What to suggest for the account just entered on a row.
	for _, c := range []struct{ name, others, current, want string }{
		{"debit above gives credit", `[{debit:"200",credit:""}]`, `{debit:"",credit:""}`, "credit 200.00"},
		{"credit above gives debit", `[{debit:"",credit:"200,50"}]`, `{debit:"",credit:""}`, "debit 200.50"},
		{"first row has nothing to balance", `[]`, `{debit:"",credit:""}`, "debit "},
		{"blank rows are ignored", `[{debit:"",credit:""},{debit:"75",credit:""}]`, `{debit:"",credit:""}`, "credit 75.00"},
		{"three rows", `[{debit:"300",credit:""},{debit:"",credit:"100"}]`, `{debit:"",credit:""}`, "credit 200.00"},
		{"two debits", `[{debit:"100",credit:""},{debit:"100",credit:""}]`, `{debit:"",credit:""}`, "credit 200.00"},
		{"already balanced", `[{debit:"100",credit:""},{debit:"",credit:"100"}]`, `{debit:"",credit:""}`, "debit "},
		{"never overwrites an amount", `[{debit:"200",credit:""}]`, `{debit:"5",credit:""}`, "debit "},
		{"unreadable amount elsewhere", `[{debit:"2x0",credit:""}]`, `{debit:"",credit:""}`, "debit "},
		{"over-balanced gives debit", `[{debit:"",credit:"50"},{debit:"10",credit:""}]`, `{debit:"",credit:""}`, "debit 40.00"},
	} {
		got := eval(`(function () { var s = VoucherForm.balancing(` + c.others + `, ` + c.current + `); return s.field + " " + s.value; })()`)
		if got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func TestVoucherFormPage(t *testing.T) {
	c := newClient(t)
	c.importSmall()
	_, page := c.get("/c/1/vouchers/new")
	if n := strings.Count(page, `name="acct"`); n != 5 { // four rows plus the template row
		t.Errorf("%d account inputs, want 4 rows and 1 in the row template", n)
	}
	for _, want := range []string{
		`<tbody id="rows">`, `<template id="row-template">`, `id="voucher-form"`,
		`name="date" value=`, ` required autofocus>`, `Ctrl+Enter`,
		`<script src="/static/voucher-form.js">`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("form lacks %s", want)
		}
	}
	// The date is a plain text field in yyyy-mm-dd form, not a date picker.
	if strings.Contains(page, `type="date"`) {
		t.Error("the form still has a date picker")
	}
	for _, want := range []string{`placeholder="yyyy-mm-dd"`, `pattern="[0-9]{4}-[0-9]{2}-[0-9]{2}"`, `maxlength="10"`} {
		if !strings.Contains(page, want) {
			t.Errorf("date field lacks %s", want)
		}
	}
	// A voucher with more lines than the default still gets a spare empty row.
	if got := len(padRows(make([]formRow, 5))); got != 6 {
		t.Errorf("padRows(5) = %d, want 6", got)
	}
	if got := len(padRows(make([]formRow, 3))); got != 4 {
		t.Errorf("padRows(3) = %d, want 4", got)
	}
	if got := len(padRows(nil)); got != 4 {
		t.Errorf("padRows(nil) = %d, want 4", got)
	}
}

// Dates are typed as yyyy-mm-dd everywhere; there is no browser date picker.
func TestNoDatePickers(t *testing.T) {
	entries, err := templateFS.ReadDir("templates")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		b, err := templateFS.ReadFile("templates/" + e.Name())
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), `type="date"`) {
			t.Errorf("%s still has a date picker", e.Name())
		}
	}
	c := newClient(t)
	c.importSmall()
	for _, page := range []string{"/", "/c/1/", "/c/1/vouchers"} {
		_, b := c.get(page)
		if strings.Contains(b, `type="date"`) {
			t.Errorf("%s renders a date picker", page)
		}
	}
	_, b := c.get("/")
	if !strings.Contains(b, `name="start" value="`) || strings.Count(b, `placeholder="yyyy-mm-dd"`) != 2 {
		t.Error("new-company dates are not plain yyyy-mm-dd fields")
	}
	_, b = c.get("/c/1/")
	if !strings.Contains(b, `name="until" size="10"`) {
		t.Error("lock date is not a plain yyyy-mm-dd field")
	}
}

func TestRattaPanelLabelAndScript(t *testing.T) {
	c := newClient(t)
	c.importSmall()
	_, page := c.get("/c/1/vouchers")
	if !strings.Contains(page, `<span class="lbl-close">Stäng</span>`) || strings.Contains(page, "Avbryt") {
		t.Error("the open Rätta button should read Stäng")
	}
	if !strings.Contains(page, `<script src="/static/voucher-list.js">`) {
		t.Error("the Verifikationer page lacks the script that keeps one Rätta panel open")
	}
}
