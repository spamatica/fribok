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

	if code, body := c.get("/c/1/vouchers/new"); code != 200 || !strings.Contains(body, `name="acct"`) || !strings.Contains(body, `<option value="6570">`) {
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
