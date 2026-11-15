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
	// Only the accounts the file uses are enabled, so only those are exported.
	used := usedAccounts(orig)
	if len(again.Accounts) != len(used) || len(used) == 0 {
		t.Errorf("exported %d accounts, the file uses %d", len(again.Accounts), len(used))
	}
	origName := map[string]string{}
	for _, a := range orig.Accounts {
		origName[a.Number] = a.Name
	}
	for _, a := range again.Accounts {
		if !used[a.Number] || a.Name != origName[a.Number] {
			t.Fatalf("exported account %+v does not match the file", a)
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
	// The previous year has no vouchers; its imported closing balances must
	// survive the round trip as well.
	cur := map[int]bool{0: true, -1: true}
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

func TestGeneralLedger(t *testing.T) {
	ctx := context.Background()
	s, cid, cur, _ := setup(t)
	if _, err := s.AddVoucher(ctx, cid, cur.ID, NewVoucher{Series: "V", Date: "2025-02-01", Text: "avgift",
		Lines: []Line{{Account: "6570", Amount: 400, Text: "swish"}, {Account: "1930", Amount: -400}}}); err != nil {
		t.Fatal(err)
	}
	gl, err := s.GeneralLedger(ctx, cid, cur.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	var bank LedgerAccount
	for _, a := range gl {
		if a.Account == "1930" {
			bank = a
		}
	}
	// V1 +100.00 (10000 öre) then V2 -4.00.
	if len(bank.Entries) != 2 || bank.Entries[0].VoucherName != "V1" || bank.Entries[1].VoucherName != "V2" ||
		bank.Entries[1].Balance != 9600 || bank.Closing != 9600 {
		t.Errorf("bank = %+v", bank)
	}
	for _, a := range gl {
		if a.Account == "6570" && (len(a.Entries) != 1 || a.Entries[0].Text != "avgift – swish") {
			t.Errorf("6570 = %+v", a)
		}
	}
	// Range filter.
	gl, _ = s.GeneralLedger(ctx, cid, cur.ID, "3000", "6999")
	if len(gl) != 2 || gl[0].Account != "3999" || gl[1].Account != "6570" {
		t.Errorf("filtered = %+v", gl)
	}
}

// For the real file, every account's running balance must end at the closing
// balance of the trial balance, and the entries must sum to the turnover.
func TestGeneralLedgerRealFile(t *testing.T) {
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
	ys, _ := s.Years(ctx, cid)
	var cur FiscalYear
	for _, y := range ys {
		if y.Index == 0 {
			cur = y
		}
	}
	gl, err := s.GeneralLedger(ctx, cid, cur.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	entries := 0
	for _, a := range gl {
		bal := a.Opening
		var net money.Ore
		for _, e := range a.Entries {
			bal += e.Amount
			net += e.Amount
			if e.Balance != bal {
				t.Fatalf("%s: running balance %s, want %s", a.Account, e.Balance, bal)
			}
			entries++
		}
		if bal != a.Closing || net != a.Debit-a.Credit {
			t.Errorf("%s: closing %s vs %s, net %s vs %s", a.Account, bal, a.Closing, net, a.Debit-a.Credit)
		}
	}
	if entries != 163 { // #TRANS records in the file
		t.Errorf("%d entries, want 163", entries)
	}
}

// The previous year of the real file has only #UB/#RES; they must be shown by
// TrialBalance as imported closing balances.
func TestImportedClosingBalancesOfEarlierYear(t *testing.T) {
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
	ys, _ := s.Years(ctx, cid)
	var prev FiscalYear
	for _, y := range ys {
		if y.Index == -1 {
			prev = y
		}
	}
	rows, err := s.TrialBalance(ctx, cid, prev.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]money.Ore{}
	var sum money.Ore
	for _, r := range rows {
		if !r.Imported || r.Debit != 0 || r.Credit != 0 {
			t.Fatalf("row %+v should be marked imported", r)
		}
		got[r.Account] = r.Closing
		sum += r.Closing
	}
	n := 0
	for _, set := range [][]sie.Balance{doc.UB, doc.Res} {
		for _, b := range set {
			if b.YearIndex != -1 {
				continue
			}
			n++
			if got[b.Account] != b.Amount {
				t.Errorf("%s: %s, want %s", b.Account, got[b.Account], b.Amount)
			}
		}
	}
	if n == 0 || n != len(got) {
		t.Errorf("%d expected entries, %d rows", n, len(got))
	}
	if sum != 0 {
		t.Errorf("imported balances sum to %s, want 0", sum)
	}
	// Such a year has no transactions to list.
	gl, _ := s.GeneralLedger(ctx, cid, prev.ID, "", "")
	if len(gl) != 0 {
		t.Errorf("general ledger of a year without transactions has %d accounts", len(gl))
	}
}

func TestYearWithOnlyImportedBalancesIsReadOnly(t *testing.T) {
	doc, err := sie.Parse(strings.NewReader(`#FNAMN "X"
#RAR 0 20250101 20251231
#RAR -1 20240101 20241231
#KONTO 1930 "Bank"
#KONTO 2010 "EK"
#KONTO 2098 "Balanserat"
#KONTO 2099 "Resultat"
#UB -1 1930 50.00
#UB -1 2010 -50.00
`))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	s := mustOpen(t)
	cid, err := s.ImportSIE(ctx, doc)
	if err != nil {
		t.Fatal(err)
	}
	ys, _ := s.Years(ctx, cid)
	var prev FiscalYear
	for _, y := range ys {
		if y.Index == -1 {
			prev = y
		}
	}
	_, err = s.AddVoucher(ctx, cid, prev.ID, NewVoucher{Series: "V", Date: "2024-05-01", Text: "x", Lines: twoLines(1)})
	wantInvalid(t, err, "importerade slutsaldon")
	_, err = s.CloseYear(ctx, cid, prev.ID, "2099", "2098")
	wantInvalid(t, err, "importerade slutsaldon")
}
