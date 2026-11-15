package report

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"fribok/internal/ledger"
	"fribok/internal/money"
	"fribok/internal/sie"
)

func row(acct, name string, closing money.Ore) ledger.TrialRow {
	return ledger.TrialRow{Account: acct, Name: name, Closing: closing}
}

func find(s Statement, label string) (money.Ore, bool) {
	for _, l := range s.Lines {
		if l.Label == label && (l.Kind == Total || l.Kind == Subtotal) {
			return l.Amount, true
		}
	}
	return 0, false
}

func TestStatementsFromSyntheticRows(t *testing.T) {
	rows := []ledger.TrialRow{
		row("1930", "Bank", 160_00),
		row("2010", "Eget kapital", -100_00),
		row("2440", "Leverantörsskulder", -20_00),
		row("3010", "Försäljning", -80_00),
		row("5010", "Lokalhyra", 30_00),
		row("7010", "Löner", 15_00),
		row("8310", "Ränteintäkter", -5_00),
		row("8999", "Årets resultat", 0),
		row("4711", "Konto utanför", 0),
	}
	is := IncomeStatement(rows)
	// income 80, costs -45, financial +5 => profit 40
	if is.Result != 40_00 {
		t.Errorf("result = %s, want 40.00", is.Result)
	}
	if v, _ := find(is, "Rörelseresultat"); v != 35_00 {
		t.Errorf("rörelseresultat = %s", v)
	}
	bs := BalanceSheet(rows)
	if bs.Check != 0 {
		t.Errorf("balance sheet does not balance: %s", bs.Check)
	}
	if v, _ := find(bs, "SUMMA TILLGÅNGAR"); v != 160_00 {
		t.Errorf("assets = %s", v)
	}
	if v, _ := find(bs, "Summa eget kapital inklusive beräknat resultat"); v != 140_00 {
		t.Errorf("equity = %s (100 booked + 40 calculated)", v)
	}
}

func TestUnclassifiedAccountsAreKept(t *testing.T) {
	rows := []ledger.TrialRow{row("1930", "Bank", 10_00), row("2010", "EK", -10_00),
		row("3010", "Intäkt", -5_00), row("9500", "Udda", 5_00)}
	is := IncomeStatement(rows) // 9500 is outside 3000-8999 and is ignored by design
	if is.Result != 5_00 {
		t.Errorf("result = %s", is.Result)
	}
	rows = []ledger.TrialRow{row("1930", "Bank", 10_00), row("1abc", "Konstig", 5_00), row("2010", "EK", -15_00)}
	bs := BalanceSheet(rows)
	if bs.Check != 0 {
		// Accounts that match no balance sheet range count as result accounts,
		// so the sheet still balances: it is derived from the whole trial.
		t.Errorf("check = %s", bs.Check)
	}
}

// With the real file (the year was closed with a voucher against 2069/8999)
// the sheet must balance and the result must equal the file's #RES sum.
func TestRealFile(t *testing.T) {
	files, _ := filepath.Glob("../../../test_files/*.se")
	if len(files) == 0 {
		t.Skip("no SIE file in test_files")
	}
	f, err := os.Open(files[0])
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	doc, err := sie.Parse(f)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	s, err := ledger.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cid, err := s.ImportSIE(ctx, doc)
	if err != nil {
		t.Fatal(err)
	}
	years, _ := s.Years(ctx, cid)
	var cur ledger.FiscalYear
	for _, y := range years {
		if y.Index == 0 {
			cur = y
		}
	}
	rows, err := s.TrialBalance(ctx, cid, cur.ID)
	if err != nil {
		t.Fatal(err)
	}
	var want money.Ore
	for _, r := range doc.Res {
		if r.YearIndex == 0 && r.Account != "8999" {
			want -= r.Amount
		}
	}
	is := IncomeStatement(rows)
	if is.Result != want {
		t.Errorf("income statement result %s, want %s", is.Result, want)
	}
	bs := BalanceSheet(rows)
	if bs.Check != 0 {
		t.Errorf("balance sheet check = %s", bs.Check)
	}
	var assets money.Ore
	for _, ub := range doc.UB {
		if ub.YearIndex == 0 && ub.Account[0] == '1' {
			assets += ub.Amount
		}
	}
	if got, _ := find(bs, "SUMMA TILLGÅNGAR"); got != assets {
		t.Errorf("assets %s, want %s", got, assets)
	}
	t.Logf("result %s, assets %s", is.Result, assets)
}
