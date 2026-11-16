package web

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
)

func (c *client) pdf(path string) (int, []byte, http.Header) {
	c.t.Helper()
	req, _ := http.NewRequest("GET", c.base+path, nil)
	code, body, h := c.do(req)
	return code, []byte(body), h
}

func TestReportPDFs(t *testing.T) {
	c := newClient(t)
	c.importSmall() // 2025 with one voucher and the accounts 1930, 3999 and 6570

	for _, tc := range []struct {
		path string
		want []string
	}{
		{"/c/1/income.pdf?year=1", []string{"Resultatr", "3999"}},
		{"/c/1/balance.pdf?year=1", []string{"Balansr", "1930"}},
		{"/c/1/trial.pdf?year=1", []string{"Saldobalans", "1930"}},
		{"/c/1/ledger.pdf?year=1", []string{"Huvudbok", "1930", "3999"}},
		{"/c/1/ledger.pdf?year=1&from=1900&to=1999", []string{"Huvudbok", "1930"}},
	} {
		code, body, h := c.pdf(tc.path)
		if code != http.StatusOK {
			t.Fatalf("%s: status %d: %s", tc.path, code, body)
		}
		if h.Get("Content-Type") != "application/pdf" {
			t.Errorf("%s: content type %q", tc.path, h.Get("Content-Type"))
		}
		if cd := h.Get("Content-Disposition"); !strings.Contains(cd, "attachment") || !strings.HasSuffix(cd, `.pdf"`) {
			t.Errorf("%s: content disposition %q", tc.path, cd)
		}
		if !bytes.HasPrefix(body, []byte("%PDF-1.4")) || !bytes.HasSuffix(body, []byte("%%EOF\n")) {
			t.Errorf("%s: not a complete PDF", tc.path)
		}
		if len(body) < 1000 {
			t.Errorf("%s: suspiciously small PDF (%d bytes)", tc.path, len(body))
		}
		for _, want := range tc.want {
			if !bytes.Contains(body, []byte(want)) {
				t.Errorf("%s: PDF does not contain %q", tc.path, want)
			}
		}
	}
	if code, body, _ := c.pdf("/c/1/ledger.pdf?year=1&from=1900&to=1999"); code == http.StatusOK && bytes.Contains(body, []byte("3999")) {
		t.Error("filtered ledger PDF contains account 3999")
	}
}

func TestReportPagesLinkToPDF(t *testing.T) {
	c := newClient(t)
	c.importSmall()
	for _, path := range []string{"/c/1/income", "/c/1/balance", "/c/1/trial", "/c/1/ledger"} {
		code, body := c.get(path)
		if code != http.StatusOK {
			t.Fatalf("%s: %d", path, code)
		}
		if !strings.Contains(body, "Ladda ner PDF") {
			t.Errorf("%s has no PDF link", path)
		}
	}
	_, body := c.get("/c/1/ledger?from=1900&to=1999")
	if !strings.Contains(body, "ledger.pdf?year=1&amp;from=1900&amp;to=1999") {
		t.Errorf("ledger PDF link does not keep the filter:\n%s", body)
	}
}

func TestReportPDFUnknownCompany(t *testing.T) {
	c := newClient(t)
	if code, _, _ := c.pdf("/c/999/trial.pdf"); code != http.StatusNotFound {
		t.Errorf("unknown company: status %d", code)
	}
}
