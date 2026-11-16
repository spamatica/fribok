package web

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

func inputValue(t *testing.T, body, name string) string {
	t.Helper()
	re := regexp.MustCompile(`name="` + regexp.QuoteMeta(name) + `" value="([^"]*)"`)
	m := re.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no input %q in:\n%s", name, body)
	}
	return m[1]
}

func TestCreateCompanyRejectsDuplicateName(t *testing.T) {
	c := newClient(t)
	c.importSmall() // "Förening"

	code, body, _ := c.post("/companies", url.Values{
		"name": {" förening "}, "start": {"2026-01-01"}, "end": {"2026-12-31"}, "template": {"1"},
	}, nil)
	if code != http.StatusUnprocessableEntity || !strings.Contains(body, "redan ett företag med namnet") {
		t.Fatalf("duplicate create: %d\n%s", code, body)
	}
	if !strings.Contains(body, `<details class="add-company" open>`) {
		t.Error("the create form did not reopen")
	}
}

func TestImportDuplicateNameOffersRename(t *testing.T) {
	c := newClient(t)
	c.importSmall() // company 1, "Förening"

	code, body, _ := c.postSIE("/import", smallSIE)
	if code != http.StatusOK {
		t.Fatalf("duplicate import: %d %s", code, body)
	}
	for _, want := range []string{
		"Det finns redan ett företag med namnet",
		`name="token"`,
		`value="Förening (2)"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("rename page lacks %q:\n%s", want, body)
		}
	}
	token := inputValue(t, body, "token")
	code, body, h := c.post("/import/confirm", url.Values{"token": {token}, "name": {"Förening 2"}}, nil)
	if code != http.StatusSeeOther || h.Get("Location") != "/c/2/" {
		t.Fatalf("rename import: %d %q %s", code, h.Get("Location"), body)
	}
	_, page := c.get("/c/2/")
	if !strings.Contains(page, "Förening 2") || !strings.Contains(page, "Räkenskapsår") {
		t.Errorf("new company page:\n%s", page)
	}
}

func TestImportRenameRetriesDuplicateName(t *testing.T) {
	c := newClient(t)
	c.importSmall()

	_, body, _ := c.postSIE("/import", smallSIE)
	token := inputValue(t, body, "token")

	code, body, _ := c.post("/import/confirm", url.Values{"token": {token}, "name": {"Förening"}}, nil)
	if code != http.StatusUnprocessableEntity || !strings.Contains(body, "redan ett företag med namnet") {
		t.Fatalf("duplicate rename: %d\n%s", code, body)
	}
	if !strings.Contains(body, token) {
		t.Error("the token was lost after a duplicate name")
	}
	code, _, h := c.post("/import/confirm", url.Values{"token": {token}, "name": {"Förening 2"}}, nil)
	if code != http.StatusSeeOther || h.Get("Location") != "/c/2/" {
		t.Fatalf("second rename: %d %q", code, h.Get("Location"))
	}
}

func TestImportConfirmRejectsUnknownToken(t *testing.T) {
	c := newClient(t)
	code, _, h := c.post("/import/confirm", url.Values{"token": {"nope"}, "name": {"X"}}, nil)
	if code != http.StatusSeeOther || !strings.HasPrefix(h.Get("Location"), "/?error=") {
		t.Errorf("unknown token: %d %q", code, h.Get("Location"))
	}
}

func TestDeleteCompanyFlow(t *testing.T) {
	c := newClient(t)
	c.importSmall()

	code, body := c.get("/c/1/")
	if code != http.StatusOK || !strings.Contains(body, "Ta bort företaget") ||
		!strings.Contains(body, `href="/c/1/delete"`) {
		t.Fatalf("company page has no delete link: %d", code)
	}
	code, body = c.get("/c/1/delete")
	if code != http.StatusOK || !strings.Contains(body, "Är du säker") ||
		!strings.Contains(body, "Ja, ta bort företaget") {
		t.Fatalf("delete confirmation page: %d\n%s", code, body)
	}
	if code, body, _ := c.post("/c/1/delete", url.Values{}, nil); code != http.StatusUnprocessableEntity ||
		!strings.Contains(body, "Bekräfta") {
		t.Errorf("unconfirmed delete: %d %s", code, body)
	}
	code, _, h := c.post("/c/1/delete", url.Values{"confirm": {"yes"}}, nil)
	if code != http.StatusSeeOther || !strings.HasPrefix(h.Get("Location"), "/?notice=") {
		t.Fatalf("delete company: %d %q", code, h.Get("Location"))
	}
	code, page := c.get(h.Get("Location"))
	if code != http.StatusOK || !strings.Contains(page, "Företaget togs bort") ||
		!strings.Contains(page, "Inget importerat") || strings.Contains(page, `class="company-card"`) {
		t.Errorf("start page after delete:\n%s", page)
	}
	if code, _ := c.get("/c/1/"); code != http.StatusNotFound {
		t.Errorf("deleted company page still exists: %d", code)
	}
}
