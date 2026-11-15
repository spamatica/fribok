package ledger

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fribok/internal/sie"
)

func parse(t *testing.T, src string) *sie.Document {
	t.Helper()
	d, err := sie.Parse(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func chartMap(t *testing.T, s *Store, cid int64) map[string]ChartRow {
	t.Helper()
	rows, err := s.Chart(context.Background(), cid)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]ChartRow{}
	for _, r := range rows {
		m[r.Number] = r
	}
	return m
}

func TestImportEnablesOnlyUsedAccounts(t *testing.T) {
	ctx := context.Background()
	s, cid, _, _ := setup(t)
	// setup enables 6570 for the voucher tests; go back to the state right after the import.
	if err := s.SetEnabled(ctx, cid, []string{"6570"}, nil); err != nil {
		t.Fatal(err)
	}
	chart := chartMap(t, s, cid)
	if len(chart) != 3 {
		t.Fatalf("chart = %+v", chart)
	}
	for num, want := range map[string]bool{"1930": true, "3999": true, "6570": false} {
		r := chart[num]
		if !r.InTemplate || r.Enabled != want || r.InUse != want {
			t.Errorf("%s = %+v, want enabled=%v", num, r, want)
		}
	}
	if as, _ := s.Accounts(ctx, cid); len(as) != 2 {
		t.Errorf("%d enabled accounts, want the 2 used", len(as))
	}
	ts, _ := s.Templates(ctx)
	if len(ts) != 1 || ts[0].Accounts != 3 || ts[0].Companies != 1 || ts[0].Name != "Importerad kontoplan: Förening" {
		t.Errorf("templates = %+v", ts)
	}
}

func TestSecondImportReusesTemplateAndKeepsCustomAccounts(t *testing.T) {
	ctx := context.Background()
	s := mustOpen(t)
	first := `#FNAMN "A"
#KPTYP EUBAS97
#RAR 0 20250101 20251231
#KONTO 1930 "Bank"
#KONTO 3999 "Avgift"
#KONTO 6570 "Bankkostnader"
#IB 0 1930 10.00
#IB 0 3999 -10.00
`
	// The second file has the same chart type, but an account of its own
	// (7777) that is not in the template, and renames 1930.
	second := `#FNAMN "B"
#KPTYP EUBAS97
#RAR 0 20250101 20251231
#KONTO 1930 "Företagskonto"
#KONTO 3999 "Avgift"
#KONTO 6570 "Bankkostnader"
#KONTO 7777 "Eget konto"
#VER "V" "1" 20250102 "x"
{
#TRANS 7777 {} 5.00
#TRANS 1930 {} -5.00
}
`
	a, err := s.ImportSIE(ctx, parse(t, first))
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.ImportSIE(ctx, parse(t, second))
	if err != nil {
		t.Fatal(err)
	}
	ts, _ := s.Templates(ctx)
	if len(ts) != 1 || ts[0].Name != "EUBAS97" || ts[0].Companies != 2 || ts[0].Accounts != 3 {
		t.Fatalf("templates = %+v", ts)
	}
	ca, _ := s.Company(ctx, a)
	cb, _ := s.Company(ctx, b)
	if ca.TemplateID != cb.TemplateID || ca.TemplateName != "EUBAS97" {
		t.Errorf("companies use different templates: %+v %+v", ca, cb)
	}
	chart := chartMap(t, s, b)
	if r := chart["7777"]; r.InTemplate || !r.Enabled || r.Name != "Eget konto" {
		t.Errorf("custom account = %+v", r)
	}
	if r := chart["1930"]; r.Name != "Företagskonto" || !r.Enabled {
		t.Errorf("file's own name for 1930 not kept: %+v", r)
	}
	// Company A is unaffected by B's rename and has no 7777.
	if r := chartMap(t, s, a)["1930"]; r.Name != "Bank" {
		t.Errorf("A's 1930 = %+v", r)
	}
	if _, ok := chartMap(t, s, a)["7777"]; ok {
		t.Error("custom account leaked into the other company")
	}
}

func TestSetEnabled(t *testing.T) {
	ctx := context.Background()
	s, cid, cur, _ := setup(t)
	shown := []string{"1930", "3999", "6570"}
	if err := s.SetEnabled(ctx, cid, []string{"6570"}, nil); err != nil { // as right after the import
		t.Fatal(err)
	}

	// Enable 6570, keep the used ones.
	if err := s.SetEnabled(ctx, cid, shown, []string{"1930", "3999", "6570"}); err != nil {
		t.Fatal(err)
	}
	if !chartMap(t, s, cid)["6570"].Enabled {
		t.Error("6570 not enabled")
	}
	// Disable the unused 6570 again.
	if err := s.SetEnabled(ctx, cid, shown, []string{"1930", "3999"}); err != nil {
		t.Fatal(err)
	}
	if chartMap(t, s, cid)["6570"].Enabled {
		t.Error("6570 not disabled")
	}
	// Used accounts cannot be disabled, and then nothing at all changes.
	if err := s.EnableAccounts(ctx, cid, []string{"6570"}); err != nil {
		t.Fatal(err)
	}
	err := s.SetEnabled(ctx, cid, shown, []string{"3999"}) // would disable 1930 (used) and 6570 (unused)
	wantInvalid(t, err, "1930")
	m := chartMap(t, s, cid)
	if !m["1930"].Enabled || !m["6570"].Enabled {
		t.Errorf("a refused change was partly applied: %+v", m)
	}
	// Only the shown accounts are touched.
	if err := s.SetEnabled(ctx, cid, []string{"3999"}, []string{"3999"}); err != nil {
		t.Fatal(err)
	}
	if !chartMap(t, s, cid)["6570"].Enabled {
		t.Error("6570 was changed although it was not shown")
	}
	// An enabled account can be posted to; a disabled one cannot.
	if _, err := s.AddVoucher(ctx, cid, cur.ID, NewVoucher{Series: "V", Date: "2025-02-01", Text: "t", Lines: twoLines(100)}); err != nil {
		t.Error(err)
	}
	// Custom and unknown accounts are ignored by SetEnabled.
	if err := s.AddAccount(ctx, cid, "7777", "Eget", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.SetEnabled(ctx, cid, []string{"7777", "9999"}, nil); err != nil {
		t.Fatal(err)
	}
	if !chartMap(t, s, cid)["7777"].Enabled {
		t.Error("custom account was disabled through the chart")
	}
	wantInvalid(t, s.EnableAccounts(ctx, cid, []string{"9999"}), "finns inte")
}

func TestImportTemplateAndDelete(t *testing.T) {
	ctx := context.Background()
	s := mustOpen(t)
	doc := parse(t, `#FNAMN "X"
#KPTYP BAS2025
#RAR 0 20250101 20251231
#KONTO 1930 "Bank"
#KONTO 2099 "Årets resultat"
#KTYP 2099 S
`)
	id, err := s.ImportTemplate(ctx, "", doc)
	if err != nil {
		t.Fatal(err)
	}
	ts, _ := s.Templates(ctx)
	if len(ts) != 1 || ts[0].Name != "BAS2025" || ts[0].KPType != "BAS2025" || ts[0].Accounts != 2 || ts[0].Companies != 0 {
		t.Fatalf("templates = %+v", ts)
	}
	_, err = s.ImportTemplate(ctx, "BAS2025", doc)
	wantInvalid(t, err, "redan en mall")
	_, err = s.ImportTemplate(ctx, "Annan", parse(t, "#FNAMN \"X\"\n"))
	wantInvalid(t, err, "ingen kontoplan")

	cid, err := s.CreateCompany(ctx, "Co", "", "2025-01-01", "2025-12-31", id, true)
	if err != nil {
		t.Fatal(err)
	}
	if c, _ := s.Company(ctx, cid); c.KPType != "BAS2025" {
		t.Errorf("kp_type = %q", c.KPType)
	}
	wantInvalid(t, s.DeleteTemplate(ctx, id), "används av 1 företag")
	other, _ := s.ImportTemplate(ctx, "Oanvänd", doc)
	if err := s.DeleteTemplate(ctx, other); err != nil {
		t.Fatal(err)
	}
	wantInvalid(t, s.DeleteTemplate(ctx, other), "finns inte")
}

// With the real file: one template of 1213 accounts, 22 enabled.
func TestRealFileTemplate(t *testing.T) {
	files, _ := filepath.Glob("../../../test_files/*.se")
	if len(files) == 0 {
		t.Skip("no SIE file in test_files")
	}
	f, _ := os.Open(files[0])
	defer f.Close()
	doc, err := sie.Parse(f)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	s := mustOpen(t)
	cid, err := s.ImportSIE(ctx, doc)
	if err != nil {
		t.Fatal(err)
	}
	ts, _ := s.Templates(ctx)
	if len(ts) != 1 || ts[0].Name != "EUBAS97" || ts[0].Accounts != 1213 {
		t.Fatalf("templates = %+v", ts)
	}
	chart, _ := s.Chart(ctx, cid)
	enabled, custom := 0, 0
	for _, r := range chart {
		if r.Enabled {
			enabled++
			if !r.InUse {
				t.Errorf("%s is enabled but not used", r.Number)
			}
		}
		if !r.InTemplate {
			custom++
		}
	}
	if len(chart) != 1213 || enabled != 22 || custom != 0 {
		t.Errorf("chart rows %d, enabled %d, custom %d", len(chart), enabled, custom)
	}
}
