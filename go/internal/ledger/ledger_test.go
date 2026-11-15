package ledger

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fribok/internal/money"
	"fribok/internal/sie"
)

func mustOpen(t *testing.T) *Store {
	t.Helper()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestImportRejectsUnbalancedVoucher(t *testing.T) {
	doc, err := sie.Parse(strings.NewReader(`#FNAMN "X"
#RAR 0 20250101 20251231
#KONTO 1930 "Bank"
#KONTO 3999 "Intäkt"
#VER "V" "1" 20250102 "x"
{
#TRANS 1930 {} 200.00
#TRANS 3999 {} -199.00
}
`))
	if err != nil {
		t.Fatal(err)
	}
	s := mustOpen(t)
	if _, err := s.ImportSIE(context.Background(), doc); err == nil || !strings.Contains(err.Error(), "does not balance") {
		t.Fatalf("expected balance error, got %v", err)
	}
	// Nothing must remain from the failed import.
	cs, _ := s.Companies(context.Background())
	if len(cs) != 0 {
		t.Errorf("failed import left %d companies", len(cs))
	}
}

// TestRealFile imports the SIE file in ../../../test_files (if present) and
// verifies that our computed balances match the #UB and #RES records of the
// exporting program, which is a strong end-to-end check of parser and store.
func TestRealFile(t *testing.T) {
	files, _ := filepath.Glob("../../../test_files/*.se")
	if len(files) == 0 {
		t.Skip("no SIE file in test_files")
	}
	for _, path := range files {
		t.Run(filepath.Base(path), func(t *testing.T) {
			f, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
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
			years, _ := s.Years(ctx, cid)
			yearByIdx := map[int]FiscalYear{}
			for _, y := range years {
				yearByIdx[y.Index] = y
			}
			t.Logf("%s: %d accounts, %d vouchers, %d years", doc.CompanyName, len(doc.Accounts), len(doc.Vouchers), len(years))

			closing := map[int]map[string]money.Ore{}
			for idx, y := range yearByIdx {
				rows, err := s.TrialBalance(ctx, cid, y.ID)
				if err != nil {
					t.Fatal(err)
				}
				m := map[string]money.Ore{}
				for _, r := range rows {
					m[r.Account] = r.Closing
				}
				closing[idx] = m
			}
			// Only years that have vouchers in the file can be recomputed; for the
			// previous year SIE files normally carry just the closing balances.
			hasVouchers := map[int]bool{}
			for _, v := range doc.Vouchers {
				for _, y := range doc.Years {
					if v.Date >= y.Start && v.Date <= y.End {
						hasVouchers[y.Index] = true
					}
				}
			}
			for _, ub := range doc.UB {
				if !hasVouchers[ub.YearIndex] {
					continue
				}
				if got := closing[ub.YearIndex][ub.Account]; got != ub.Amount {
					t.Errorf("#UB year %d account %s: computed %s, file says %s", ub.YearIndex, ub.Account, got, ub.Amount)
				}
			}
			// #RES: result accounts have no opening balance, so closing == turnover.
			for _, r := range doc.Res {
				if !hasVouchers[r.YearIndex] {
					continue
				}
				if got := closing[r.YearIndex][r.Account]; got != r.Amount {
					t.Errorf("#RES year %d account %s: computed %s, file says %s", r.YearIndex, r.Account, got, r.Amount)
				}
			}
		})
	}
}

// TestExportRoundTrip imports the real file, exports it as SIE, parses the
// export again and compares everything the store keeps.
func TestExportRoundTrip(t *testing.T) {
	files, _ := filepath.Glob("../../../test_files/*.se")
	if len(files) == 0 {
		t.Skip("no SIE file in test_files")
	}
	f, err := os.Open(files[0])
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	orig, err := sie.Parse(f)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	s := mustOpen(t)
	cid, err := s.ImportSIE(ctx, orig)
	if err != nil {
		t.Fatal(err)
	}
	exp, err := s.Export(ctx, cid)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := sie.Write(&buf, exp, time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	again, err := sie.Parse(&buf)
	if err != nil {
		t.Fatalf("exported file does not parse: %v", err)
	}

	if again.CompanyName != orig.CompanyName || again.OrgNr != orig.OrgNr || again.KPType != orig.KPType || again.SieType != 4 {
		t.Errorf("header differs: %q/%q/%q vs %q/%q/%q", again.CompanyName, again.OrgNr, again.KPType, orig.CompanyName, orig.OrgNr, orig.KPType)
	}
	if len(again.Years) != len(orig.Years) {
		t.Errorf("years: %d vs %d", len(again.Years), len(orig.Years))
	}
	for i := range orig.Accounts {
		if again.Accounts[i].Number != orig.Accounts[i].Number || again.Accounts[i].Name != orig.Accounts[i].Name {
			t.Fatalf("account %d differs: %+v vs %+v", i, again.Accounts[i], orig.Accounts[i])
		}
	}
	// Compare balances ignoring zero entries: the store does not keep
	// zero-valued #RES/#UB rows for accounts without activity.
	nonzero := func(bs []sie.Balance, onlyYear map[int]bool) map[string]money.Ore {
		m := map[string]money.Ore{}
		for _, b := range bs {
			if b.Amount != 0 && onlyYear[b.YearIndex] {
				m[fmt.Sprintf("%d/%s", b.YearIndex, b.Account)] = b.Amount
			}
		}
		return m
	}
	// Year 0 is the only year with vouchers, so only there UB/RES are comparable.
	cur := map[int]bool{0: true}
	all := map[int]bool{0: true, -1: true}
	for _, c := range []struct {
		name      string
		got, want []sie.Balance
		onlyYears map[int]bool
	}{{"IB", again.IB, orig.IB, all}, {"UB", again.UB, orig.UB, cur}, {"RES", again.Res, orig.Res, cur}} {
		g, w := nonzero(c.got, c.onlyYears), nonzero(c.want, c.onlyYears)
		if len(g) != len(w) {
			t.Errorf("%s: %d entries vs %d", c.name, len(g), len(w))
		}
		for k, v := range w {
			if g[k] != v {
				t.Errorf("%s %s: %s vs %s", c.name, k, g[k], v)
			}
		}
	}

	if len(again.Vouchers) != len(orig.Vouchers) {
		t.Fatalf("vouchers: %d vs %d", len(again.Vouchers), len(orig.Vouchers))
	}
	for i, ov := range orig.Vouchers {
		gv := again.Vouchers[i]
		if gv.Series != ov.Series || gv.Number != ov.Number || gv.Date != ov.Date || gv.Text != ov.Text || len(gv.Lines) != len(ov.Lines) {
			t.Fatalf("voucher %d differs: %+v vs %+v", i, gv, ov)
		}
		for j, ol := range ov.Lines {
			gl := gv.Lines[j]
			if gl.Account != ol.Account || gl.Amount != ol.Amount || gl.Text != ol.Text {
				t.Fatalf("voucher %s%s line %d differs: %+v vs %+v", ov.Series, ov.Number, j, gl, ol)
			}
		}
	}
}
