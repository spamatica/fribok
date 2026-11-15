package web

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fribok/internal/ledger"
)

func TestImportAndBrowse(t *testing.T) {
	files, _ := filepath.Glob("../../../test_files/*.se")
	if len(files) == 0 {
		t.Skip("no SIE file in test_files")
	}
	store, err := ledger.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ts := httptest.NewServer(New(store).Handler())
	defer ts.Close()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	data, _ := os.ReadFile(files[0])
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "x.se")
	fw.Write(data)
	mw.Close()
	resp, err := client.Post(ts.URL+"/import", mw.FormDataContentType(), &body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("import: status %d", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if !strings.HasPrefix(loc, "/c/") {
		t.Fatalf("import redirected to %q", loc)
	}

	for _, path := range []string{"/", loc, loc + "accounts", loc + "vouchers", loc + "trial"} {
		r, err := client.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(r.Body)
		r.Body.Close()
		if r.StatusCode != 200 {
			t.Errorf("%s: status %d: %s", path, r.StatusCode, b)
		}
		if !strings.Contains(string(b), "</html>") {
			t.Errorf("%s: page not rendered completely", path)
		}
	}
	if r, _ := client.Get(ts.URL + "/c/999/"); r.StatusCode != 404 {
		t.Errorf("unknown company: status %d", r.StatusCode)
	}
}
