package web

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"fribok/internal/ledger"
)

const smallSIE = `#FNAMN "Förening"
#RAR 0 20250101 20251231
#KONTO 1930 "Bank"
#KONTO 3999 "Medlemsavgift"
#KONTO 6570 "Bankkostnader"
#VER "V" "1" 20250102 "start"
{
#TRANS 1930 {} 100.00
#TRANS 3999 {} -100.00
}
`

type client struct {
	t    *testing.T
	base string
	c    *http.Client
}

func newClient(t *testing.T) *client {
	t.Helper()
	store, err := ledger.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	ts := httptest.NewServer(New(store).Handler())
	t.Cleanup(ts.Close)
	return &client{t: t, base: ts.URL, c: &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (c *client) do(req *http.Request) (int, string, http.Header) {
	c.t.Helper()
	resp, err := c.c.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header
}

func (c *client) get(path string) (int, string) {
	req, _ := http.NewRequest("GET", c.base+path, nil)
	code, body, _ := c.do(req)
	return code, body
}

func (c *client) post(path string, form url.Values, hdr map[string]string) (int, string, http.Header) {
	req, _ := http.NewRequest("POST", c.base+path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	return c.do(req)
}

func (c *client) importSmall() {
	c.t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "x.se")
	fw.Write([]byte(smallSIE))
	mw.Close()
	req, _ := http.NewRequest("POST", c.base+"/import", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if code, b, h := c.do(req); code != http.StatusSeeOther || h.Get("Location") != "/c/1/" {
		c.t.Fatalf("import: %d %q %s", code, h.Get("Location"), b)
	}
	// The file only uses 1930 and 3999; enable 6570 from the template as well.
	if code, b, _ := c.post("/c/1/chart", url.Values{"shown": {"6570"}, "on": {"6570"}, "action": {"save"}}, nil); code != http.StatusSeeOther {
		c.t.Fatalf("enable 6570: %d %s", code, b)
	}
}

func postedForm(date, text string, rows ...[4]string) url.Values {
	f := url.Values{"year": {"1"}, "series": {"V"}, "date": {date}, "text": {text}}
	for _, r := range rows {
		f.Add("acct", r[0])
		f.Add("debit", r[1])
		f.Add("credit", r[2])
		f.Add("ltext", r[3])
	}
	return f
}

func TestVoucherEntryFlow(t *testing.T) {
	c := newClient(t)
	c.importSmall()

	if code, body := c.get("/c/1/vouchers/new"); code != 200 || !strings.Contains(body, `name="acct"`) || !strings.Contains(body, `data-n="6570"`) {
		t.Fatalf("form: %d", code)
	}

	// An unbalanced voucher is refused and the input is kept in the form.
	code, body, _ := c.post("/c/1/vouchers", postedForm("2025-03-01", "bankavgift",
		[4]string{"6570", "12,50", "", "avgift"}, [4]string{"1930", "", "12", ""}), nil)
	if code != 422 || !strings.Contains(body, "balanserar inte") || !strings.Contains(body, `value="12,50"`) || !strings.Contains(body, `value="bankavgift"`) {
		t.Fatalf("unbalanced: %d\n%s", code, body)
	}
	if code, body, _ := c.post("/c/1/vouchers", postedForm("2025-03-01", "x",
		[4]string{"6570", "abc", "", ""}, [4]string{"1930", "", "1", ""}), nil); code != 422 || !strings.Contains(body, "Ogiltigt belopp på rad 1") {
		t.Errorf("bad amount: %d", code)
	}
	if code, body, _ := c.post("/c/1/vouchers", postedForm("2025-03-01", "x",
		[4]string{"6570", "5", "5", ""}, [4]string{"1930", "", "0", ""}), nil); code != 422 || !strings.Contains(body, "både debet och kredit") {
		t.Errorf("both sides: %d", code)
	}

	// A good one: decimal comma, blank rows ignored.
	code, _, h := c.post("/c/1/vouchers", postedForm("2025-03-01", "bankavgift",
		[4]string{"6570", "12,50", "", ""}, [4]string{"1930", "", "12.50", ""}, [4]string{"", "", "", ""}), nil)
	if code != http.StatusSeeOther || !strings.HasPrefix(h.Get("Location"), "/c/1/vouchers?year=1#v") {
		t.Fatalf("create: %d %q", code, h.Get("Location"))
	}
	_, list := c.get("/c/1/vouchers")
	if !strings.Contains(list, "V2") || !strings.Contains(list, "bankavgift") || !strings.Contains(list, "12,50") {
		t.Errorf("voucher not listed:\n%s", list)
	}

	// Correct V2: reversal V3, then the page offers a prefilled form.
	code, _, h = c.post("/c/1/vouchers/2/correct", url.Values{"date": {"2025-03-02"}, "reason": {"fel belopp"}}, nil)
	if code != http.StatusSeeOther || !strings.HasPrefix(h.Get("Location"), "/c/1/vouchers/new?copy=2") {
		t.Fatalf("correct: %d %q", code, h.Get("Location"))
	}
	_, form := c.get(h.Get("Location"))
	if !strings.Contains(form, "Rättelseverifikation V3 skapad") || !strings.Contains(form, `value="12.50"`) || !strings.Contains(form, `value="bankavgift"`) {
		t.Errorf("prefilled form:\n%s", form)
	}
	_, list = c.get("/c/1/vouchers")
	if !strings.Contains(list, "rättar V2") || !strings.Contains(list, "(rättad)") {
		t.Errorf("correction not marked:\n%s", list)
	}
	if code, body, _ := c.post("/c/1/vouchers/2/correct", url.Values{"date": {"2025-03-02"}, "reason": {"igen"}}, nil); code != 422 || !strings.Contains(body, "redan rättad") {
		t.Errorf("second correction: %d %s", code, body)
	}

	// Lock, then posting inside the lock is refused.
	if code, _, _ := c.post("/c/1/lock", url.Values{"year": {"1"}, "until": {"2025-03-31"}}, nil); code != http.StatusSeeOther {
		t.Fatalf("lock: %d", code)
	}
	if _, body := c.get("/c/1/"); !strings.Contains(body, "låst t.o.m. 2025-03-31") {
		t.Errorf("lock not shown")
	}
	if code, body, _ := c.post("/c/1/vouchers", postedForm("2025-03-15", "sent",
		[4]string{"6570", "1", "", ""}, [4]string{"1930", "", "1", ""}), nil); code != 422 || !strings.Contains(body, "låst") {
		t.Errorf("post into locked period: %d", code)
	}
	if code, body, _ := c.post("/c/1/lock", url.Values{"year": {"1"}, "until": {"2025-01-31"}}, nil); code != 422 || !strings.Contains(body, "kan inte hävas") {
		t.Errorf("unlock: %d %s", code, body)
	}
}

func TestCrossSitePostsAreRefused(t *testing.T) {
	c := newClient(t)
	c.importSmall()
	f := postedForm("2025-03-01", "x", [4]string{"6570", "1", "", ""}, [4]string{"1930", "", "1", ""})
	for name, hdr := range map[string]map[string]string{
		"sec-fetch-site": {"Sec-Fetch-Site": "cross-site"},
		"origin":         {"Origin": "http://evil.example"},
	} {
		if code, _, _ := c.post("/c/1/vouchers", f, hdr); code != http.StatusForbidden {
			t.Errorf("%s: status %d, want 403", name, code)
		}
	}
	if code, _, _ := c.post("/c/1/vouchers", f, map[string]string{"Sec-Fetch-Site": "same-origin", "Origin": c.base}); code != http.StatusSeeOther {
		t.Errorf("same-origin post refused: %d", code)
	}
	_, list := c.get("/c/1/vouchers")
	if strings.Count(list, `class="voucher"`) != 2 {
		t.Errorf("expected exactly the imported voucher and one posted:\n%s", list)
	}
}

func TestCloseYearFlow(t *testing.T) {
	c := newClient(t)
	// Reuse the small books and add the accounts closing needs.
	src := strings.Replace(smallSIE, `#KONTO 6570`, "#KONTO 2098 \"Balanserat\"\n#KONTO 2099 \"Årets resultat\"\n#KONTO 8999 \"Årets resultat\"\n#KONTO 6570", 1)
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "x.se")
	fw.Write([]byte(src))
	mw.Close()
	req, _ := http.NewRequest("POST", c.base+"/import", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if code, b, _ := c.do(req); code != http.StatusSeeOther {
		t.Fatalf("import: %d %s", code, b)
	}

	if _, b := c.get("/c/1/"); !strings.Contains(b, "avsluta året") {
		t.Error("no close link on company page")
	}
	code, page := c.get("/c/1/close?year=1")
	if code != 200 || !strings.Contains(page, "bokförs som verifikation i serie BS") || !strings.Contains(page, "2026-01-01") || !strings.Contains(page, `name="confirm"`) {
		t.Fatalf("preview: %d\n%s", code, page)
	}
	if !strings.Contains(page, "Avsluta året används") || !strings.Contains(page, "Det går inte att ångra") {
		t.Errorf("preview does not explain what closing means:\n%s", page)
	}
	if _, b := c.get("/c/1/vouchers"); strings.Contains(b, "BS1") {
		t.Fatal("preview posted a voucher")
	}

	// Without the confirmation nothing happens.
	if code, b, _ := c.post("/c/1/close", url.Values{"year": {"1"}}, nil); code != 422 || !strings.Contains(b, "Bekräfta") {
		t.Errorf("unconfirmed: %d", code)
	}
	if _, b := c.get("/c/1/vouchers"); strings.Contains(b, "BS1") {
		t.Fatal("unconfirmed close posted a voucher")
	}
	// A bad account is reported on the preview page.
	if code, b := c.get("/c/1/close?year=1&result=1930&carry=2098"); code != 422 || !strings.Contains(b, "skuld- eller eget kapital") {
		t.Errorf("bad account: %d", code)
	}

	code, _, h := c.post("/c/1/close", url.Values{"year": {"1"}, "confirm": {"yes"}}, nil)
	if code != http.StatusSeeOther || h.Get("Location") != "/c/1/" {
		t.Fatalf("close: %d %q", code, h.Get("Location"))
	}
	_, home := c.get("/c/1/")
	if !strings.Contains(home, "2026-01-01 – 2026-12-31") || !strings.Contains(home, "låst t.o.m. 2025-12-31") {
		t.Errorf("home after close:\n%s", home)
	}
	if code, b := c.get("/c/1/close?year=1"); code != 422 || !strings.Contains(b, "redan avslutat") {
		t.Errorf("second close: %d", code)
	}
	// The new year's trial balance has the opening balance, and so does the balance sheet.
	if _, b := c.get("/c/1/balance"); !strings.Contains(b, "1930") || !strings.Contains(b, "100,00") {
		t.Errorf("balance sheet of the new year:\n%s", b)
	}
	_, vs := c.get("/c/1/vouchers?year=1")
	if !strings.Contains(vs, "BS1") {
		t.Errorf("closing voucher missing:\n%s", vs)
	}
}

func TestTemplatesChartAndCompanies(t *testing.T) {
	c := newClient(t)
	c.importSmall() // company 1; its chart became the template "Importerad kontoplan: Förening"

	// Company 1 has all three accounts of the template, two used by the file and 6570 enabled by the helper.
	_, b := c.get("/c/1/accounts")
	if !strings.Contains(b, "Importerad kontoplan: Förening") || !strings.Contains(b, "3 av 3 konton är aktiverade") {
		t.Fatalf("chart page of company 1:\n%s", b)
	}
	// 6570 is not used, so it can be disabled; the used ones cannot.
	if code, _, _ := c.post("/c/1/chart", url.Values{"shown": {"1930", "3999", "6570"}, "on": {"1930", "3999"}, "action": {"save"}}, nil); code != http.StatusSeeOther {
		t.Fatalf("disable unused: %d", code)
	}
	_, b = c.get("/c/1/accounts?enabled=1")
	if !strings.Contains(b, "2 av 3 konton är aktiverade") || strings.Contains(b, "Bankkostnader") || !strings.Contains(b, "Medlemsavgift") {
		t.Errorf("only-enabled filter:\n%s", b)
	}
	code, b, _ := c.post("/c/1/chart", url.Values{"shown": {"1930", "3999", "6570"}, "on": {"3999"}, "action": {"save"}}, nil)
	if code != 422 || !strings.Contains(b, "inaktiveras") || !strings.Contains(b, "1930") {
		t.Errorf("disable used: %d", code)
	}

	// The new company form offers the template.
	if code, b := c.get("/"); code != 200 || !strings.Contains(b, `action="/companies"`) || !strings.Contains(b, "Importerad kontoplan: Förening (3 konton)") {
		t.Fatalf("index: %d", code)
	}
	code, b, _ = c.post("/companies", url.Values{"name": {"Ny"}, "orgnr": {"1-2"}, "start": {"2026-12-31"}, "end": {"2026-01-01"}, "template": {"1"}}, nil)
	if code != 422 || !strings.Contains(b, "före dess start") || !strings.Contains(b, `value="Ny"`) {
		t.Errorf("bad company: %d", code)
	}
	code, _, h := c.post("/companies", url.Values{"name": {"Ny förening"}, "orgnr": {"802000-1"}, "start": {"2026-01-01"}, "end": {"2026-12-31"}, "template": {"1"}, "enable": {"none"}}, nil)
	if code != http.StatusSeeOther || h.Get("Location") != "/c/2/" {
		t.Fatalf("create company: %d %q", code, h.Get("Location"))
	}
	if _, b := c.get("/c/2/accounts"); !strings.Contains(b, "0 av 3 konton är aktiverade") {
		t.Errorf("new company chart:\n%s", b)
	}
	// Nothing is enabled, so booking fails until the accounts are enabled.
	f := postedForm("2026-03-01", "första", [4]string{"1930", "50", "", ""}, [4]string{"3999", "", "50", ""})
	f.Set("year", "2")
	if code, b, _ := c.post("/c/2/vouchers", f, nil); code != 422 || !strings.Contains(b, "finns inte i kontoplanen") {
		t.Errorf("booking on disabled accounts: %d", code)
	}
	// Search, then enable everything that is shown.
	if _, b := c.get("/c/2/accounts?q=19"); !strings.Contains(b, "1 konton visas") {
		t.Errorf("search:\n%s", b)
	}
	if code, _, _ := c.post("/c/2/chart", url.Values{"shown": {"1930", "3999"}, "action": {"enable_shown"}, "q": {""}}, nil); code != http.StatusSeeOther {
		t.Fatalf("enable shown: %d", code)
	}
	if _, b := c.get("/c/2/accounts"); !strings.Contains(b, "2 av 3 konton är aktiverade") {
		t.Errorf("after enabling:\n%s", b)
	}
	if code, b, h := c.post("/c/2/vouchers", f, nil); code != http.StatusSeeOther || !strings.HasPrefix(h.Get("Location"), "/c/2/vouchers?year=2") {
		t.Errorf("booking after enabling: %d %s", code, b)
	}
	// The chart of company 2 cannot touch company 1.
	if _, b := c.get("/c/1/accounts"); !strings.Contains(b, "2 av 3 konton är aktiverade") {
		t.Errorf("company 1 changed:\n%s", b)
	}

	// Custom accounts: not for numbers in the template, listed as "egen", not togglable.
	if code, b, _ := c.post("/c/2/accounts", url.Values{"number": {"6570"}, "name": {"x"}}, nil); code != 422 || !strings.Contains(b, "finns i kontoplansmallen") {
		t.Errorf("template number as custom: %d", code)
	}
	if code, _, _ := c.post("/c/2/accounts", url.Values{"number": {"7777"}, "name": {"Eget konto"}}, nil); code != http.StatusSeeOther {
		t.Fatalf("custom account: %d", code)
	}
	if _, b := c.get("/c/2/accounts?q=7777"); !strings.Contains(b, "Eget konto") || !strings.Contains(b, ">egen<") {
		t.Errorf("custom account listing:\n%s", b)
	}
	// Edit page: rename, and deactivate an unused template account.
	if code, b := c.get("/c/2/accounts/7777"); code != 200 || !strings.Contains(b, "Ta bort kontot") {
		t.Errorf("custom edit page: %d", code)
	}
	if code, _, _ := c.post("/c/2/accounts/7777", url.Values{"name": {"Eget konto 2"}, "type": {"K"}}, nil); code != http.StatusSeeOther {
		t.Errorf("rename: %d", code)
	}
	if code, _, _ := c.post("/c/2/accounts/7777/delete", url.Values{}, nil); code != http.StatusSeeOther {
		t.Errorf("delete custom: %d", code)
	}
	if code, _ := c.get("/c/2/accounts/7777"); code != 404 {
		t.Errorf("deleted account page: %d", code)
	}
	if code, _ := c.get("/c/2/accounts/6570"); code != 404 {
		t.Errorf("disabled account has an edit page: %d", code) // not enabled in company 2
	}
	if code, b := c.get("/c/1/accounts/1930"); code != 200 || strings.Contains(b, "Ta bort kontot") || strings.Contains(b, "Inaktivera kontot") {
		t.Errorf("used account edit page: %d", code)
	}

	// Template management.
	if code, b := c.get("/templates"); code != 200 || !strings.Contains(b, "Importerad kontoplan: Förening") || !strings.Contains(b, ">2<") {
		t.Errorf("templates page: %d\n%s", code, b)
	}
	upload := func(name, content string) (int, string) {
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		fw, _ := mw.CreateFormFile("file", "m.se")
		fw.Write([]byte(content))
		mw.WriteField("name", name)
		mw.Close()
		req, _ := http.NewRequest("POST", c.base+"/templates", &body)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		code, b, _ := c.do(req)
		return code, b
	}
	chart := "#FNAMN \"X\"\n#KPTYP BAS2025\n#KONTO 1930 \"Bank\"\n#KONTO 2099 \"Årets resultat\"\n"
	if code, b := upload("BAS 2025", chart); code != http.StatusSeeOther {
		t.Fatalf("import template: %d %s", code, b)
	}
	if code, b := upload("BAS 2025", chart); code != 422 || !strings.Contains(b, "redan en mall") {
		t.Errorf("duplicate template: %d", code)
	}
	if code, b := upload("Tom", "#FNAMN \"X\"\n"); code != 422 || !strings.Contains(b, "ingen kontoplan") {
		t.Errorf("empty template: %d", code)
	}
	if code, b := upload("Trasig", "not sie"); code != 422 || !strings.Contains(b, "kunde inte läsas") {
		t.Errorf("broken file: %d", code)
	}
	if code, b, _ := c.post("/templates/1/delete", url.Values{}, nil); code != 422 || !strings.Contains(b, "används av 2 företag") {
		t.Errorf("delete used template: %d %s", code, b)
	}
	if code, _, _ := c.post("/templates/2/delete", url.Values{}, nil); code != http.StatusSeeOther {
		t.Errorf("delete unused template: %d", code)
	}
}

func TestDeleteLastVoucherFlow(t *testing.T) {
	c := newClient(t)
	c.importSmall()
	good := func(text string) {
		t.Helper()
		if code, b, _ := c.post("/c/1/vouchers", postedForm("2025-03-01", text,
			[4]string{"6570", "10", "", ""}, [4]string{"1930", "", "10", ""}), nil); code != http.StatusSeeOther {
			t.Fatalf("post %s: %d %s", text, code, b)
		}
	}
	good("första") // V2
	good("andra")  // V3, posted last

	// The list is newest first.
	_, page := c.get("/c/1/vouchers")
	v3, v2 := strings.Index(page, "<strong>V3</strong>"), strings.Index(page, "<strong>V2</strong>")
	if v3 < 0 || v2 < 0 || v3 > v2 {
		t.Errorf("vouchers are not newest first (V3 at %d, V2 at %d)", v3, v2)
	}
	// Only the last voucher offers deletion.
	if n := strings.Count(page, `/delete"`); n != 1 || !strings.Contains(page, `/c/1/vouchers/3/delete"`) || !strings.Contains(page, "Radera verifikationen") {
		t.Errorf("delete forms on the page: %d\n", n)
	}
	// Deleting an earlier one is refused and changes nothing.
	if code, b, _ := c.post("/c/1/vouchers/2/delete", url.Values{}, nil); code != 422 || !strings.Contains(b, "Bara den senast bokförda") {
		t.Errorf("delete non-last: %d %s", code, b)
	}
	// Cross-site posts are refused like all others.
	if code, _, _ := c.post("/c/1/vouchers/3/delete", url.Values{}, map[string]string{"Origin": "http://evil.example"}); code != http.StatusForbidden {
		t.Errorf("cross-site delete: %d", code)
	}
	if _, p := c.get("/c/1/vouchers"); !strings.Contains(p, "andra") {
		t.Fatal("voucher vanished after refused deletes")
	}

	code, _, h := c.post("/c/1/vouchers/3/delete", url.Values{}, nil)
	if code != http.StatusSeeOther || !strings.HasPrefix(h.Get("Location"), "/c/1/vouchers?year=1&notice=") {
		t.Fatalf("delete: %d %q", code, h.Get("Location"))
	}
	_, page = c.get(h.Get("Location"))
	if !strings.Contains(page, "Verifikationen raderades") || strings.Contains(page, "andra") || !strings.Contains(page, `/c/1/vouchers/2/delete"`) {
		t.Errorf("page after delete:\n%s", page)
	}
	// The number is reused.
	good("tredje")
	if _, p := c.get("/c/1/vouchers"); !strings.Contains(p, "<strong>V3</strong> 2025-03-01 – tredje") {
		t.Errorf("number V3 not reused:\n%s", p)
	}

	// A locked period cannot be deleted from, and the button is gone.
	if code, _, _ := c.post("/c/1/lock", url.Values{"year": {"1"}, "until": {"2025-03-31"}}, nil); code != http.StatusSeeOther {
		t.Fatal("lock failed")
	}
	if _, p := c.get("/c/1/vouchers"); strings.Contains(p, `/delete"`) {
		t.Error("delete offered in a locked period")
	}
	if code, b, _ := c.post("/c/1/vouchers/3/delete", url.Values{}, nil); code != 422 || !strings.Contains(b, "låst") {
		t.Errorf("delete in locked period: %d %s", code, b)
	}
	// There is no deleted-voucher page any more.
	if code, _ := c.get("/c/1/deleted"); code != http.StatusNotFound {
		t.Errorf("deleted-voucher page still exists: %d", code)
	}
}

func TestVouchersPageHasNoDeletedLink(t *testing.T) {
	c := newClient(t)
	c.importSmall()
	code, body := c.get("/c/1/vouchers")
	if code != http.StatusOK {
		t.Fatalf("vouchers page: %d", code)
	}
	if strings.Contains(body, "Raderade verifikationer") || strings.Contains(body, `href="/c/1/deleted"`) {
		t.Errorf("vouchers page still links to the deleted-voucher log:\n%s", body)
	}
	if !strings.Contains(body, `class="btn primary"`) || !strings.Contains(body, `>Ny verifikation <kbd class="shortcut"`) {
		t.Errorf("Ny verifikation is not a primary button:\n%s", body)
	}
	if code, _ := c.get("/c/1/deleted"); code != http.StatusNotFound {
		t.Errorf("deleted-voucher page still exists: %d", code)
	}
}

func TestStartPageHighlightsCompanies(t *testing.T) {
	c := newClient(t)
	_, before := c.get("/")
	if !strings.Contains(before, "Inget importerat") || !strings.Contains(before, `action="/companies"`) {
		t.Fatalf("empty start page:\n%s", before)
	}
	if strings.Contains(before, `class="company-card"`) || strings.Contains(before, `class="add-company"`) {
		t.Error("empty start page shows the company picker")
	}

	c.importSmall()
	_, after := c.get("/")
	for _, want := range []string{
		`class="company-list"`,
		`class="company-card"`,
		">Förening<",
		"Öppna →",
		`<details class="add-company">`,
		`<summary class="btn">Lägg till företag</summary>`,
		`action="/companies"`,
	} {
		if !strings.Contains(after, want) {
			t.Errorf("start page lacks %q:\n%s", want, after)
		}
	}
	if strings.Index(after, "Dina företag") > strings.Index(after, `class="add-company"`) {
		t.Error("the add-company form is above the company list")
	}

	// A failed create opens the collapsed form again, with the typed values.
	code, body, _ := c.post("/companies", url.Values{
		"name": {"Ny"}, "start": {"2026-12-31"}, "end": {"2026-01-01"}, "template": {"1"},
	}, nil)
	if code != http.StatusUnprocessableEntity || !strings.Contains(body, `<details class="add-company" open>`) ||
		!strings.Contains(body, `value="Ny"`) {
		t.Errorf("failed create did not open the form: %d", code)
	}
}
