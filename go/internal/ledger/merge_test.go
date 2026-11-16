package ledger

import (
	"context"
	"strings"
	"testing"
)

const merge2024 = `#FNAMN "Förening"
#RAR 0 20240101 20241231
#KONTO 1930 "Bank"
#KONTO 2010 "Eget kapital"
#KONTO 3999 "Medlemsavgift"
#KONTO 6570 "Bankkostnader"
#IB 0 1930 1000.00
#IB 0 2010 -1000.00
#VER "V" "1" 20240102 "avgift"
{
#TRANS 1930 {} 100.00
#TRANS 3999 {} -100.00
}
#UB 0 1930 1100.00
#UB 0 2010 -1000.00
#RES 0 3999 -100.00
`

const merge2025 = `#FNAMN "Förening"
#RAR 0 20250101 20251231
#RAR -1 20240101 20241231
#KONTO 1930 "Bank"
#KONTO 2010 "Eget kapital"
#KONTO 3999 "Medlemsavgift"
#KONTO 6570 "Bankkostnader"
#KONTO 6571 "Extra kostnad"
#IB 0 1930 1100.00
#IB 0 2010 -1100.00
#IB -1 1930 1000.00
#IB -1 2010 -1000.00
#UB -1 1930 1100.00
#UB -1 2010 -1000.00
#RES -1 3999 -100.00
#VER "V" "1" 20250102 "avgift"
{
#TRANS 1930 {} 200.00
#TRANS 3999 {} -200.00
}
#VER "V" "2" 20250103 "bank"
{
#TRANS 6571 {} 50.00
#TRANS 1930 {} -50.00
}
#UB 0 1930 1250.00
#UB 0 2010 -1100.00
#RES 0 3999 -200.00
#RES 0 6571 50.00
`

const merge2026 = `#FNAMN "Förening"
#RAR 0 20260101 20261231
#RAR -1 20250101 20251231
#KONTO 1930 "Bank"
#KONTO 2010 "Eget kapital"
#KONTO 3999 "Medlemsavgift"
#KONTO 6570 "Bankkostnader"
#KONTO 6571 "Extra kostnad"
#IB 0 1930 1250.00
#IB 0 2010 -1250.00
#IB -1 1930 1100.00
#IB -1 2010 -1100.00
#UB -1 1930 1250.00
#UB -1 2010 -1100.00
#RES -1 3999 -200.00
#RES -1 6571 50.00
#VER "V" "1" 20260102 "avgift"
{
#TRANS 1930 {} 300.00
#TRANS 3999 {} -300.00
}
#UB 0 1930 1550.00
#UB 0 2010 -1250.00
#RES 0 3999 -300.00
`

func importMerge(t *testing.T, s *Store, src string) int64 {
	t.Helper()
	cid, err := s.ImportSIE(context.Background(), parse(t, src))
	if err != nil {
		t.Fatal(err)
	}
	return cid
}

func mergeDoc(t *testing.T, s *Store, cid int64, src string) MergeResult {
	t.Helper()
	res, err := s.MergeSIE(context.Background(), cid, parse(t, src))
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func yearStarting(t *testing.T, s *Store, cid int64, start string) FiscalYear {
	t.Helper()
	ys, err := s.Years(context.Background(), cid)
	if err != nil {
		t.Fatal(err)
	}
	for _, y := range ys {
		if y.Start == start {
			return y
		}
	}
	t.Fatalf("no fiscal year starting %s", start)
	return FiscalYear{}
}

func rowFor(t *testing.T, rows []TrialRow, account string) TrialRow {
	t.Helper()
	for _, r := range rows {
		if r.Account == account {
			return r
		}
	}
	t.Fatalf("no trial balance row for %s", account)
	return TrialRow{}
}

func TestMergeAddsConsecutiveYearAndSkipsPreviousSummary(t *testing.T) {
	ctx := context.Background()
	s := mustOpen(t)
	cid := importMerge(t, s, merge2024)

	res := mergeDoc(t, s, cid, merge2025)
	if len(res.Added) != 1 || res.Added[0].Start != "2025-01-01" {
		t.Errorf("added = %+v", res.Added)
	}
	if len(res.Skipped) != 1 || res.Skipped[0].Start != "2024-01-01" {
		t.Errorf("skipped = %+v", res.Skipped)
	}
	if res.Vouchers != 2 || res.Accounts != 1 {
		t.Errorf("vouchers=%d accounts=%d", res.Vouchers, res.Accounts)
	}

	ys, _ := s.Years(ctx, cid)
	if len(ys) != 2 || ys[0].Start != "2025-01-01" || ys[0].Index != 0 ||
		ys[1].Start != "2024-01-01" || ys[1].Index != -1 {
		t.Fatalf("years = %+v", ys)
	}
	y2024, y2025 := yearStarting(t, s, cid, "2024-01-01"), yearStarting(t, s, cid, "2025-01-01")
	if vs, _ := s.Vouchers(ctx, y2024.ID); len(vs) != 1 {
		t.Errorf("2024 vouchers = %d, want 1", len(vs))
	}
	if vs, _ := s.Vouchers(ctx, y2025.ID); len(vs) != 2 {
		t.Errorf("2025 vouchers = %d, want 2", len(vs))
	}

	// The 2025 file also carries 2024 closing balances. Because 2024 already
	// has vouchers here, those must not be stored as a year summary.
	var summaries int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM year_summary WHERE fiscal_year_id=?`, y2024.ID).Scan(&summaries); err != nil {
		t.Fatal(err)
	}
	if summaries != 0 {
		t.Errorf("2024 got %d imported summaries", summaries)
	}

	rows, err := s.TrialBalance(ctx, cid, y2025.ID)
	if err != nil {
		t.Fatal(err)
	}
	if r := rowFor(t, rows, "1930"); r.Opening != 110000 || r.Closing != 125000 || r.Imported {
		t.Errorf("2025 account 1930 = %+v", r)
	}
	// 6571 was not in the first file's chart, but the merge enables it as a
	// custom account because the new year uses it.
	if r := rowFor(t, rows, "6571"); r.Closing != 5000 {
		t.Errorf("2025 account 6571 = %+v", r)
	}
	chart, err := s.Chart(ctx, cid)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, r := range chart {
		if r.Number == "6571" {
			found = true
			if !r.Enabled || r.InTemplate || r.Name != "Extra kostnad" {
				t.Errorf("6571 chart row = %+v", r)
			}
		}
	}
	if !found {
		t.Error("6571 missing from the chart")
	}
}

func TestMergeReplacesImportedSummaryWithActualVouchers(t *testing.T) {
	ctx := context.Background()
	s := mustOpen(t)
	// Import the 2025 file first: it has 2025 vouchers but only closing
	// balances for 2024.
	cid := importMerge(t, s, merge2025)
	y2024 := yearStarting(t, s, cid, "2024-01-01")
	if vs, _ := s.Vouchers(ctx, y2024.ID); len(vs) != 0 {
		t.Fatalf("precondition: 2024 has %d vouchers", len(vs))
	}
	rows, _ := s.TrialBalance(ctx, cid, y2024.ID)
	if r := rowFor(t, rows, "1930"); !r.Imported || r.Closing != 110000 {
		t.Fatalf("precondition: 2024 account 1930 = %+v", r)
	}

	res := mergeDoc(t, s, cid, merge2024)
	if len(res.Updated) != 1 || res.Updated[0].Start != "2024-01-01" || res.Vouchers != 1 {
		t.Errorf("result = %+v", res)
	}
	y2024 = yearStarting(t, s, cid, "2024-01-01")
	if vs, _ := s.Vouchers(ctx, y2024.ID); len(vs) != 1 {
		t.Errorf("2024 vouchers = %d, want 1", len(vs))
	}
	rows, _ = s.TrialBalance(ctx, cid, y2024.ID)
	if r := rowFor(t, rows, "1930"); r.Imported || r.Closing != 110000 || r.Opening != 100000 {
		t.Errorf("2024 account 1930 = %+v", r)
	}
	var summaries int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM year_summary WHERE fiscal_year_id=?`, y2024.ID).Scan(&summaries); err != nil {
		t.Fatal(err)
	}
	if summaries != 0 {
		t.Errorf("2024 kept %d summaries", summaries)
	}
	// The newer year is still the current one (idx 0).
	ys, _ := s.Years(ctx, cid)
	if ys[0].Start != "2025-01-01" || ys[0].Index != 0 || ys[1].Index != -1 {
		t.Errorf("years = %+v", ys)
	}
}

func TestMergeThreeYearsRenumbersNewestZero(t *testing.T) {
	ctx := context.Background()
	s := mustOpen(t)
	cid := importMerge(t, s, merge2024)
	mergeDoc(t, s, cid, merge2025)
	mergeDoc(t, s, cid, merge2026)

	ys, _ := s.Years(ctx, cid)
	if len(ys) != 3 {
		t.Fatalf("years = %+v", ys)
	}
	want := []struct {
		start string
		idx   int
	}{{"2026-01-01", 0}, {"2025-01-01", -1}, {"2024-01-01", -2}}
	for i, w := range want {
		if ys[i].Start != w.start || ys[i].Index != w.idx {
			t.Errorf("year %d = %+v, want start %s idx %d", i, ys[i], w.start, w.idx)
		}
	}
	// Every year has its vouchers; the two overlapping summaries are ignored.
	for _, start := range []string{"2024-01-01", "2025-01-01", "2026-01-01"} {
		y := yearStarting(t, s, cid, start)
		rows, err := s.TrialBalance(ctx, cid, y.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rows {
			if r.Imported {
				t.Errorf("%s has imported summary row %+v", start, r)
			}
		}
	}
	if rows, _ := s.TrialBalance(ctx, cid, yearStarting(t, s, cid, "2026-01-01").ID); rowFor(t, rows, "1930").Closing != 155000 {
		t.Errorf("2026 account 1930 = %+v", rowFor(t, rows, "1930"))
	}
}

func TestMergeRejectsFileWhoseYearsAlreadyHaveVouchers(t *testing.T) {
	ctx := context.Background()
	s := mustOpen(t)
	cid := importMerge(t, s, merge2024)
	if _, err := s.MergeSIE(ctx, cid, parse(t, merge2024)); err == nil ||
		!strings.Contains(err.Error(), "finns redan med") {
		t.Fatalf("expected duplicate-voucher error, got %v", err)
	}
	ys, _ := s.Years(ctx, cid)
	if len(ys) != 1 {
		t.Errorf("failed merge changed the years: %+v", ys)
	}
}

func TestMergeIsAtomicOnMalformedNewYear(t *testing.T) {
	ctx := context.Background()
	s := mustOpen(t)
	cid := importMerge(t, s, merge2024)
	// The duplicate #UB for 2026 is not caught during planning; it fails while
	// writing the new year and must roll the whole merge back.
	bad := `#FNAMN "Förening"
#RAR 0 20260101 20261231
#KONTO 1930 "Bank"
#KONTO 2010 "Eget kapital"
#IB 0 1930 1000.00
#IB 0 2010 -1000.00
#UB 0 1930 1000.00
#UB 0 1930 1000.00
`
	if _, err := s.MergeSIE(ctx, cid, parse(t, bad)); err == nil {
		t.Fatal("expected an error")
	}
	ys, _ := s.Years(ctx, cid)
	if len(ys) != 1 || ys[0].Start != "2024-01-01" || ys[0].Index != 0 {
		t.Errorf("failed merge left changes behind: %+v", ys)
	}
	if vs, _ := s.Vouchers(ctx, ys[0].ID); len(vs) != 1 {
		t.Errorf("2024 vouchers = %d", len(vs))
	}
}

func TestMergeRejectsOverlappingFiscalYear(t *testing.T) {
	ctx := context.Background()
	s := mustOpen(t)
	cid := importMerge(t, s, merge2024)
	overlap := `#FNAMN "Förening"
#RAR 0 20240601 20250531
#KONTO 1930 "Bank"
#KONTO 2010 "Eget kapital"
#IB 0 1930 1000.00
#IB 0 2010 -1000.00
`
	if _, err := s.MergeSIE(ctx, cid, parse(t, overlap)); err == nil ||
		!strings.Contains(err.Error(), "överlappar") {
		t.Fatalf("expected overlap error, got %v", err)
	}
}

func TestMergeKeepsExistingAccountName(t *testing.T) {
	ctx := context.Background()
	s := mustOpen(t)
	cid := importMerge(t, s, merge2024)
	// 1930 is used in both files, with a different name in the second one.
	renamed := strings.Replace(merge2025, `#KONTO 1930 "Bank"`, `#KONTO 1930 "Företagskonto"`, 1)
	mergeDoc(t, s, cid, renamed)
	chart, err := s.Chart(ctx, cid)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range chart {
		if r.Number == "1930" && r.Name != "Bank" {
			t.Errorf("existing account was renamed: %+v", r)
		}
	}
}

func TestMergeWithNoNewDataOnlyRenumbers(t *testing.T) {
	ctx := context.Background()
	s := mustOpen(t)
	cid := importMerge(t, s, merge2024)
	// A file with only the existing year, and no vouchers or balances, is a no-op.
	same := `#FNAMN "Förening"
#RAR 0 20240101 20241231
#KONTO 1930 "Bank"
#KONTO 2010 "Eget kapital"
`
	res := mergeDoc(t, s, cid, same)
	if len(res.Added)+len(res.Updated) != 0 || len(res.Skipped) != 1 {
		t.Errorf("result = %+v", res)
	}
	ys, _ := s.Years(ctx, cid)
	if len(ys) != 1 || ys[0].Index != 0 {
		t.Errorf("years = %+v", ys)
	}
}
