package ledger

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
