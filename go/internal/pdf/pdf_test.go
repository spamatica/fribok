package pdf

import (
	"bytes"
	"strings"
	"testing"
)

func TestPDFStructureAndPages(t *testing.T) {
	d := NewA4(false)
	d.SetFooter(func(page, total int) string {
		return "Sida " + string(rune('0'+page)) + " av " + string(rune('0'+total))
	})
	d.Text(d.Margin(), 50, 12, true, "Årsredovisning (test)")
	d.Line(d.Margin(), 60, d.Width()-d.Margin(), 0.5)
	d.FillRect(d.Margin(), 70, 100, 12, 0.9)
	d.AddPage()
	d.Text(d.Margin(), 50, 10, false, "Andra sidan")

	data := d.Bytes()
	if !bytes.HasPrefix(data, []byte("%PDF-1.4")) {
		t.Fatalf("not a PDF: %q", data[:10])
	}
	if !bytes.HasSuffix(data, []byte("%%EOF\n")) {
		t.Fatalf("missing EOF marker")
	}
	for _, want := range [][]byte{
		[]byte("/Type /Catalog"),
		[]byte("/Type /Pages"),
		[]byte("/Type /Page "),
		[]byte("/BaseFont /Helvetica"),
		[]byte("/BaseFont /Helvetica-Bold"),
		[]byte("xref"),
		[]byte("trailer"),
		[]byte("Andra sidan"),
	} {
		if !bytes.Contains(data, want) {
			t.Errorf("PDF does not contain %q", want)
		}
	}
	if n := bytes.Count(data, []byte("/Type /Page ")); n != 2 {
		t.Errorf("page objects = %d, want 2", n)
	}
}

func TestFit(t *testing.T) {
	d := NewA4(false)
	if got := Fit(d, 9, false, 50, "kort"); got != "kort" {
		t.Errorf("Fit short = %q", got)
	}
	got := Fit(d, 9, false, 50, "en mycket lång text som inte får plats")
	if !strings.HasSuffix(got, "…") || d.StringWidth(9, false, got) > 50 {
		t.Errorf("Fit long = %q (width %.1f)", got, d.StringWidth(9, false, got))
	}
}
