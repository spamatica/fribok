package web

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

const mergeNextSIE = `#FNAMN "Förening"
#RAR 0 20260101 20261231
#RAR -1 20250101 20251231
#KONTO 1930 "Bank"
#KONTO 2010 "Eget kapital"
#KONTO 3999 "Medlemsavgift"
#KONTO 6570 "Bankkostnader"
#KONTO 6571 "Extra kostnad"
#IB 0 1930 100.00
#IB 0 2010 -100.00
#IB -1 1930 100.00
#IB -1 2010 -100.00
#UB -1 1930 100.00
#UB -1 2010 -100.00
#RES -1 3999 0.00
#VER "V" "1" 20260102 "start"
{
#TRANS 1930 {} 50.00
#TRANS 6571 {} 50.00
#TRANS 3999 {} -100.00
}
`

func (c *client) postSIE(path, file string) (int, string, http.Header) {
	c.t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "x.se")
	fw.Write([]byte(file))
	mw.Close()
	req, _ := http.NewRequest("POST", c.base+path, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return c.do(req)
}

func TestMergeSIEIntoExistingCompany(t *testing.T) {
	c := newClient(t)
	c.importSmall()

	code, body := c.get("/c/1/")
	if code != 200 || !strings.Contains(body, `action="/c/1/import"`) {
		t.Fatalf("company page has no merge form: %d", code)
	}

	code, body, h := c.postSIE("/c/1/import", mergeNextSIE)
	if code != http.StatusSeeOther {
		t.Fatalf("merge: status %d: %s", code, body)
	}
	loc := h.Get("Location")
	if !strings.HasPrefix(loc, "/c/1/?notice=") {
		t.Fatalf("merge redirected to %q", loc)
	}

	code, body = c.get(loc)
	if code != 200 {
		t.Fatalf("company page: %d", code)
	}
	for _, want := range []string{
		"2025-01-01 – 2025-12-31",
		"2026-01-01 – 2026-12-31",
		"Tillagda räkenskapsår: 2026-01-01 – 2026-12-31",
		"Fanns redan: 2025-01-01 – 2025-12-31",
		"Importerade verifikationer: 1. Nya konton: 2.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page does not contain %q:\n%s", want, body)
		}
	}
}

func TestMergeSIEErrorIsShownOnCompanyPage(t *testing.T) {
	c := newClient(t)
	c.importSmall()

	bad := strings.Replace(mergeNextSIE, "#TRANS 3999 {} -100.00", "#TRANS 3999 {} -99.00", 1)
	code, body, h := c.postSIE("/c/1/import", bad)
	if code != http.StatusSeeOther {
		t.Fatalf("merge: status %d: %s", code, body)
	}
	loc := h.Get("Location")
	if !strings.HasPrefix(loc, "/c/1/?error=") {
		t.Fatalf("merge redirected to %q", loc)
	}
	code, body = c.get(loc)
	if code != 200 || !strings.Contains(body, "balanserar inte") {
		t.Fatalf("error page: %d %s", code, body)
	}
}

func TestMergeSIEWithNoFileShowsError(t *testing.T) {
	c := newClient(t)
	c.importSmall()

	code, body, _ := c.post("/c/1/import", url.Values{}, nil)
	if code != http.StatusSeeOther {
		t.Fatalf("merge without file: status %d: %s", code, body)
	}
}

func TestClosePreviewWarnsWhenNextYearExists(t *testing.T) {
	c := newClient(t)
	c.importSmall()
	for _, a := range [][3]string{
		{"2098", "Balanserat resultat", "S"},
		{"2099", "Årets resultat", "S"},
		{"8999", "Årets resultat", "K"},
	} {
		form := url.Values{"number": {a[0]}, "name": {a[1]}, "type": {a[2]}}
		if code, body, _ := c.post("/c/1/accounts", form, nil); code != http.StatusSeeOther {
			t.Fatalf("add account %s: %d %s", a[0], code, body)
		}
	}
	if code, body, _ := c.postSIE("/c/1/import", mergeNextSIE); code != http.StatusSeeOther {
		t.Fatalf("merge: %d %s", code, body)
	}

	code, page := c.get("/c/1/close?year=1")
	if code != 200 {
		t.Fatalf("preview: %d\n%s", code, page)
	}
	for _, want := range []string{
		"Avsluta året används",
		"Det går inte att ångra",
		"Nästa räkenskapsår finns redan",
		"ersätter dess ingående balanser",
		"Lås period",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("preview does not contain %q:\n%s", want, page)
		}
	}

	// The newest year has no next year, so there is nothing to overwrite.
	code, page = c.get("/c/1/close?year=2")
	if code != 200 {
		t.Fatalf("newest-year preview: %d\n%s", code, page)
	}
	if strings.Contains(page, "Nästa räkenskapsår finns redan") {
		t.Errorf("newest-year preview warns about a next year:\n%s", page)
	}
}
