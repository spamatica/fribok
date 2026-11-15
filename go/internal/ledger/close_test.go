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

const closeBooks = `#FNAMN "Förening"
#RAR 0 20250101 20251231
#KONTO 1930 "Bank"
#KONTO 2010 "Eget kapital"
#KONTO 2098 "Vinst eller förlust från föregående år"
#KONTO 2099 "Årets resultat"
#KONTO 3999 "Medlemsavgift"
#KONTO 6570 "Bankkostnader"
#KONTO 8999 "Årets resultat"
#IB 0 1930 1000.00
#IB 0 2010 -1000.00
#VER "V" "1" 20250102 "avgifter"
{
#TRANS 1930 {} 300.00
#TRANS 3999 {} -300.00
}
#VER "V" "2" 20250103 "bank"
{
#TRANS 6570 {} 50.00
#TRANS 1930 {} -50.00
}
`

func importClose(t *testing.T, src string) (*Store, int64, FiscalYear) {
	t.Helper()
	doc, err := sie.Parse(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	s := mustOpen(t)
	cid, err := s.ImportSIE(context.Background(), doc)
	if err != nil {
		t.Fatal(err)
	}
	ys, _ := s.Years(context.Background(), cid)
	var cur FiscalYear
	for _, y := range ys {
		if y.Index == 0 {
			cur = y
		}
	}
	return s, cid, cur
}

func TestCloseYearBooksResultAndCarriesBalances(t *testing.T) {
	s, cid, cur := importClose(t, closeBooks)
	ctx := context.Background()

	// Preview changes nothing.
	plan, err := s.PlanClose(ctx, cid, cur.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if plan.ResultAccount != "2099" || plan.CarryAccount != "2098" || plan.Result != 250_00 || plan.NextExists ||
		plan.NextStart != "2026-01-01" || plan.NextEnd != "2026-12-31" {
		t.Errorf("plan = %+v", plan)
	}
	if ys, _ := s.Years(ctx, cid); len(ys) != 1 {
		t.Fatalf("preview created a year")
	}
	if vs, _ := s.Vouchers(ctx, cur.ID); len(vs) != 2 {
		t.Fatalf("preview posted a voucher")
	}

	plan, err = s.CloseYear(ctx, cid, cur.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if plan.VoucherLabel != "BS1" {
		t.Errorf("voucher label %q", plan.VoucherLabel)
	}
	vs, _ := s.Vouchers(ctx, cur.ID)
	var bs Voucher
	for _, v := range vs {
		if v.Series == "BS" {
			bs = v
		}
	}
	if bs.Date != "2025-12-31" || len(bs.Lines) != 2 || bs.Lines[0].Account != "8999" || bs.Lines[0].Amount != 250_00 ||
		bs.Lines[1].Account != "2099" || bs.Lines[1].Amount != -250_00 {
		t.Errorf("closing voucher = %+v", bs)
	}

	ys, _ := s.Years(ctx, cid)
	if len(ys) != 2 {
		t.Fatalf("years = %+v", ys)
	}
	next, old := ys[0], ys[1] // newest first
	if next.Start != "2026-01-01" || next.Index != 0 || old.Index != -1 || old.LockedUntil != "2025-12-31" {
		t.Errorf("next = %+v, old = %+v", next, old)
	}
	// 1930: 1000 + 300 - 50; equity 1000 unchanged; result 250 carried to 2098 (credit).
	rows, _ := s.TrialBalance(ctx, cid, next.ID)
	got := map[string]money.Ore{}
	var sum money.Ore
	for _, r := range rows {
		got[r.Account] = r.Closing
		sum += r.Closing
	}
	want := map[string]money.Ore{"1930": 1250_00, "2010": -1000_00, "2098": -250_00}
	if len(got) != len(want) || sum != 0 {
		t.Errorf("opening = %v (sum %s)", got, sum)
	}
	for a, v := range want {
		if got[a] != v {
			t.Errorf("opening %s = %s, want %s", a, got[a], v)
		}
	}

	// The old year is locked completely and cannot be closed twice.
	_, err = s.AddVoucher(ctx, cid, old.ID, NewVoucher{Series: "V", Date: "2025-12-31", Text: "t", Lines: twoLines(1)})
	wantInvalid(t, err, "låst")
	_, err = s.CloseYear(ctx, cid, old.ID, "", "")
	wantInvalid(t, err, "redan avslutat")

	// Close the second year too: the carried result moves on and nothing is lost.
	if _, err := s.AddVoucher(ctx, cid, next.ID, NewVoucher{Series: "V", Date: "2026-02-01", Text: "ny", Lines: []Line{
		{Account: "1930", Amount: 100_00}, {Account: "3999", Amount: -100_00}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CloseYear(ctx, cid, next.ID, "", ""); err != nil {
		t.Fatal(err)
	}
	ys, _ = s.Years(ctx, cid)
	if len(ys) != 3 || ys[0].Start != "2027-01-01" || ys[0].Index != 0 || ys[1].Index != -1 || ys[2].Index != -2 {
		t.Fatalf("years after second close: %+v", ys)
	}
	rows, _ = s.TrialBalance(ctx, cid, ys[0].ID)
	got = map[string]money.Ore{}
	for _, r := range rows {
		got[r.Account] = r.Closing
	}
	if got["1930"] != 1350_00 || got["2010"] != -1000_00 || got["2098"] != -350_00 {
		t.Errorf("second opening = %v", got)
	}
	// Exported SIE numbers the years -2, -1, 0 and stays importable.
	doc, err := s.Export(ctx, cid)
	if err != nil || len(doc.Years) != 3 {
		t.Fatalf("export: %v %+v", err, doc.Years)
	}
}

func TestCloseYearValidation(t *testing.T) {
	s, cid, cur := importClose(t, closeBooks)
	ctx := context.Background()
	for name, c := range map[string]struct{ result, carry, want string }{
		"unknown":    {"2999", "2098", "finns inte"},
		"not equity": {"1930", "2098", "skuld- eller eget kapital"},
		"same":       {"2099", "2099", "olika"},
	} {
		_, err := s.CloseYear(ctx, cid, cur.ID, c.result, c.carry)
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		wantInvalid(t, err, c.want)
	}
	// Nothing was changed by the refused attempts.
	if ys, _ := s.Years(ctx, cid); len(ys) != 1 || ys[0].LockedUntil != "" {
		t.Errorf("refused close changed state: %+v", ys)
	}
	// A chart without the usual accounts needs explicit ones.
	s2, cid2, cur2 := importClose(t, strings.NewReplacer(
		`#KONTO 2098 "Vinst eller förlust från föregående år"`+"\n", "",
		`#KONTO 2099 "Årets resultat"`+"\n", "").Replace(closeBooks))
	_, err := s2.CloseYear(ctx, cid2, cur2.ID, "", "")
	wantInvalid(t, err, "Ange konto")
}

// In the real file the year was already closed by hand against 2069/8999, so
// no result voucher may be added and the old result moves from 2069 to 2098.
func TestCloseRealFileWithResultAlreadyBooked(t *testing.T) {
	files, _ := filepath.Glob("../../../test_files/*.se")
	if len(files) == 0 {
		t.Skip("no SIE file in test_files")
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	doc, err := sie.Parse(strings.NewReader(string(data)))
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
	plan, err := s.CloseYear(ctx, cid, cur.ID, "2069", "2098")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Result != 0 || plan.VoucherLabel != "" {
		t.Errorf("result already booked, but plan = %+v", plan)
	}
	ys, _ = s.Years(ctx, cid)
	if len(ys) != 3 || ys[0].Start != "2026-01-01" {
		t.Fatalf("years: %+v", ys)
	}
	rows, _ := s.TrialBalance(ctx, cid, ys[0].ID)
	got := map[string]money.Ore{}
	var sum money.Ore
	for _, r := range rows {
		got[r.Account] = r.Closing
		sum += r.Closing
	}
	want := map[string]money.Ore{"1930": 85129_72, "2010": -94696_06, "2098": 9566_34}
	if sum != 0 || len(got) != len(want) {
		t.Errorf("opening = %v (sum %s)", got, sum)
	}
	for a, v := range want {
		if got[a] != v {
			t.Errorf("%s = %s, want %s", a, got[a], v)
		}
	}
}
