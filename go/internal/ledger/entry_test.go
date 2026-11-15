package ledger

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"testing"

	"fribok/internal/money"
	"fribok/internal/sie"
)

const smallBooks = `#FNAMN "Förening"
#RAR 0 20250101 20251231
#RAR -1 20240101 20241231
#KONTO 1930 "Bank"
#KONTO 3999 "Medlemsavgift"
#KONTO 6570 "Bankkostnader"
#VER "V" "1" 20250102 "start"
{
#TRANS 1930 {} 100.00
#TRANS 3999 {} -100.00
}
`

func setup(t *testing.T) (*Store, int64, FiscalYear, FiscalYear) {
	t.Helper()
	doc, err := sie.Parse(strings.NewReader(smallBooks))
	if err != nil {
		t.Fatal(err)
	}
	s := mustOpen(t)
	cid, err := s.ImportSIE(context.Background(), doc)
	if err != nil {
		t.Fatal(err)
	}
	// The file only uses 1930 and 3999; 6570 comes from the template.
	if err := s.EnableAccounts(context.Background(), cid, []string{"6570"}); err != nil {
		t.Fatal(err)
	}
	ys, _ := s.Years(context.Background(), cid)
	var cur, prev FiscalYear
	for _, y := range ys {
		if y.Index == 0 {
			cur = y
		} else {
			prev = y
		}
	}
	return s, cid, cur, prev
}

func twoLines(amount money.Ore) []Line {
	return []Line{{Account: "6570", Amount: amount}, {Account: "1930", Amount: -amount}}
}

func wantInvalid(t *testing.T, err error, substr string) {
	t.Helper()
	var ve *ValidationError
	if !errors.As(err, &ve) || !strings.Contains(ve.Msg, substr) {
		t.Fatalf("want validation error containing %q, got %v", substr, err)
	}
}

func TestAddVoucherNumbersWithoutGaps(t *testing.T) {
	s, cid, cur, _ := setup(t)
	ctx := context.Background()
	a, err := s.AddVoucher(ctx, cid, cur.ID, NewVoucher{Series: "V", Date: "2025-02-01", Text: "avgift", Lines: twoLines(400)})
	if err != nil {
		t.Fatal(err)
	}
	if a.Number != 2 { // the imported voucher is V1
		t.Errorf("number = %d, want 2", a.Number)
	}
	// A failed attempt must not consume a number.
	if _, err := s.AddVoucher(ctx, cid, cur.ID, NewVoucher{Series: "V", Date: "2025-02-01", Text: "x", Lines: twoLines(0)}); err == nil {
		t.Fatal("zero amount accepted")
	}
	b, err := s.AddVoucher(ctx, cid, cur.ID, NewVoucher{Series: "V", Date: "2025-02-02", Text: "avgift 2", Lines: twoLines(400)})
	if err != nil || b.Number != 3 {
		t.Fatalf("b = %+v, %v", b, err)
	}
	other, err := s.AddVoucher(ctx, cid, cur.ID, NewVoucher{Series: "A", Date: "2025-02-02", Text: "annan serie", Lines: twoLines(400)})
	if err != nil || other.Number != 1 {
		t.Fatalf("new series starts at 1: %+v, %v", other, err)
	}
}

func TestAddVoucherValidation(t *testing.T) {
	s, cid, cur, prev := setup(t)
	ctx := context.Background()
	ok := func() NewVoucher {
		return NewVoucher{Series: "V", Date: "2025-03-01", Text: "t", Lines: twoLines(100)}
	}
	cases := map[string]struct {
		mod  func(*NewVoucher)
		year int64
		want string
	}{
		"unbalanced":   {func(v *NewVoucher) { v.Lines[1].Amount = -99 }, cur.ID, "balanserar inte"},
		"one line":     {func(v *NewVoucher) { v.Lines = v.Lines[:1] }, cur.ID, "minst två rader"},
		"no text":      {func(v *NewVoucher) { v.Text = "  " }, cur.ID, "text saknas"},
		"bad series":   {func(v *NewVoucher) { v.Series = "a b" }, cur.ID, "Serie"},
		"bad date":     {func(v *NewVoucher) { v.Date = "2025-13-01" }, cur.ID, "Ogiltigt datum"},
		"outside year": {func(v *NewVoucher) { v.Date = "2026-01-01" }, cur.ID, "utanför räkenskapsåret"},
		"unknown acct": {func(v *NewVoucher) { v.Lines[0].Account = "9999" }, cur.ID, "finns inte"},
		"no account":   {func(v *NewVoucher) { v.Lines[0].Account = "" }, cur.ID, "saknar konto"},
		"wrong year":   {func(v *NewVoucher) {}, 9999, "Okänt räkenskapsår"},
		"date vs year": {func(v *NewVoucher) {}, prev.ID, "utanför räkenskapsåret"},
	}
	for name, c := range cases {
		v := ok()
		c.mod(&v)
		_, err := s.AddVoucher(ctx, cid, c.year, v)
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		wantInvalid(t, err, c.want)
	}
	// Nothing but the imported voucher exists.
	vs, _ := s.Vouchers(ctx, cur.ID)
	if len(vs) != 1 {
		t.Errorf("%d vouchers after rejected attempts, want 1", len(vs))
	}
}

func TestLocking(t *testing.T) {
	s, cid, cur, _ := setup(t)
	ctx := context.Background()
	if err := s.LockYear(ctx, cid, cur.ID, "2025-03-31"); err != nil {
		t.Fatal(err)
	}
	_, err := s.AddVoucher(ctx, cid, cur.ID, NewVoucher{Series: "V", Date: "2025-03-31", Text: "t", Lines: twoLines(1)})
	wantInvalid(t, err, "låst")
	if _, err := s.AddVoucher(ctx, cid, cur.ID, NewVoucher{Series: "V", Date: "2025-04-01", Text: "t", Lines: twoLines(1)}); err != nil {
		t.Errorf("first day after the lock should be open: %v", err)
	}
	wantInvalid(t, s.LockYear(ctx, cid, cur.ID, "2025-02-28"), "kan inte hävas")
	wantInvalid(t, s.LockYear(ctx, cid, cur.ID, "2026-01-01"), "utanför")
	if err := s.LockYear(ctx, cid, cur.ID, "2025-06-30"); err != nil {
		t.Errorf("moving the lock forward: %v", err)
	}
	ys, _ := s.Years(ctx, cid)
	for _, y := range ys {
		if y.ID == cur.ID && y.LockedUntil != "2025-06-30" {
			t.Errorf("locked_until = %q", y.LockedUntil)
		}
	}
}

func TestCorrectVoucher(t *testing.T) {
	s, cid, cur, prev := setup(t)
	ctx := context.Background()
	orig, err := s.AddVoucher(ctx, cid, cur.ID, NewVoucher{Series: "V", Date: "2025-02-01", Text: "fel", Lines: twoLines(500)})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := s.TrialBalance(ctx, cid, cur.ID)

	corr, err := s.CorrectVoucher(ctx, cid, orig.ID, "2025-02-05", "fel konto")
	if err != nil {
		t.Fatal(err)
	}
	if corr.CorrectsID != orig.ID || corr.Number != orig.Number+1 || !strings.HasPrefix(corr.Text, "Rättelse av V2: fel konto") {
		t.Errorf("correction = %+v", corr)
	}
	// The correction cancels the original in the trial balance (only the
	// 6570/1930 turnover changes by an equal debit and credit).
	after, _ := s.TrialBalance(ctx, cid, cur.ID)
	closing := func(rows []TrialRow, acct string) money.Ore {
		for _, r := range rows {
			if r.Account == acct {
				return r.Closing
			}
		}
		return 0
	}
	if closing(after, "6570") != closing(before, "6570")-500 || closing(after, "1930") != closing(before, "1930")+500 {
		t.Errorf("balances after correction: 6570 %s->%s, 1930 %s->%s",
			closing(before, "6570"), closing(after, "6570"), closing(before, "1930"), closing(after, "1930"))
	}
	vs, _ := s.Vouchers(ctx, cur.ID)
	for _, v := range vs {
		switch v.ID {
		case orig.ID:
			if !v.Corrected {
				t.Error("original not flagged as corrected")
			}
		case corr.ID:
			if v.CorrectsLabel != "V2" {
				t.Errorf("CorrectsLabel = %q", v.CorrectsLabel)
			}
		}
	}

	_, err = s.CorrectVoucher(ctx, cid, orig.ID, "2025-02-06", "igen")
	wantInvalid(t, err, "redan rättad")
	_, err = s.CorrectVoucher(ctx, cid, orig.ID+1000, "2025-02-06", "x")
	wantInvalid(t, err, "Okänd verifikation")
	_, err = s.CorrectVoucher(ctx, cid, orig.ID, "2025-02-06", " ")
	wantInvalid(t, err, "anledning")
	_, err = s.CorrectVoucher(ctx, cid, 1, "2030-01-01", "x")
	wantInvalid(t, err, "inte i något räkenskapsår")
	_ = prev
}

func TestCorrectionCanLandInALaterYear(t *testing.T) {
	// The original is in a locked year; the correction is dated in the next.
	doc, _ := sie.Parse(strings.NewReader(strings.Replace(smallBooks, "#RAR 0 20250101 20251231\n#RAR -1 20240101 20241231",
		"#RAR 0 20250101 20251231\n#RAR 1 20260101 20261231", 1)))
	s := mustOpen(t)
	ctx := context.Background()
	cid, err := s.ImportSIE(ctx, doc)
	if err != nil {
		t.Fatal(err)
	}
	s.EnableAccounts(ctx, cid, []string{"6570"})
	ys, _ := s.Years(ctx, cid)
	var y0, y1 FiscalYear
	for _, y := range ys {
		if y.Index == 0 {
			y0 = y
		} else {
			y1 = y
		}
	}
	if err := s.LockYear(ctx, cid, y0.ID, "2025-12-31"); err != nil {
		t.Fatal(err)
	}
	vs, _ := s.Vouchers(ctx, y0.ID)
	if _, err := s.CorrectVoucher(ctx, cid, vs[0].ID, "2025-12-31", "x"); err == nil {
		t.Error("correction inside the locked period accepted")
	}
	c, err := s.CorrectVoucher(ctx, cid, vs[0].ID, "2026-01-10", "rättas i nytt år")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := s.Vouchers(ctx, y1.ID)
	if len(got) != 1 || got[0].ID != c.ID {
		t.Errorf("correction not in the later year: %+v", got)
	}
}

func TestConcurrentVouchersGetDistinctNumbers(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir + "/c.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	doc, _ := sie.Parse(strings.NewReader(smallBooks))
	ctx := context.Background()
	cid, err := s.ImportSIE(ctx, doc)
	if err != nil {
		t.Fatal(err)
	}
	s.EnableAccounts(ctx, cid, []string{"6570"})
	ys, _ := s.Years(ctx, cid)
	var cur FiscalYear
	for _, y := range ys {
		if y.Index == 0 {
			cur = y
		}
	}
	const n = 20
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.AddVoucher(ctx, cid, cur.ID, NewVoucher{Series: "V", Date: "2025-05-01", Text: "c", Lines: twoLines(1)})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
	vs, _ := s.Vouchers(ctx, cur.ID)
	if len(vs) != n+1 {
		t.Fatalf("%d vouchers, want %d", len(vs), n+1)
	}
	for i, v := range vs {
		if v.Number != i+1 {
			t.Fatalf("gap or duplicate: position %d has number %d", i, v.Number)
		}
	}
}

// TestMigrationUpgradesAnOldDatabase builds a database as it was at schema
// version 2 (before chart templates), with data, and opens it.
func TestMigrationUpgradesAnOldDatabase(t *testing.T) {
	path := t.TempDir() + "/old.db"
	raw, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range append([]string{schema}, migrations[:2]...) {
		if _, err := raw.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	for _, stmt := range []string{
		`INSERT INTO company (id, name, org_nr, kp_type) VALUES (7, 'Gammal förening', '1-2', 'EUBAS97')`,
		`INSERT INTO fiscal_year (id, company_id, idx, start_date, end_date) VALUES (1, 7, 0, '2025-01-01', '2025-12-31')`,
		`INSERT INTO account VALUES (7, '1930', 'Bank', 'T'), (7, '3999', 'Avgift', 'I')`,
		`INSERT INTO voucher (id, company_id, fiscal_year_id, series, number, date, text) VALUES (1, 7, 1, 'V', 1, '2025-01-02', 'start')`,
		`INSERT INTO voucher_line (voucher_id, seq, account, amount) VALUES (1, 1, '1930', 10000), (1, 2, '3999', -10000)`,
		`PRAGMA user_version = 2`,
	} {
		if _, err := raw.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	raw.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatalf("open old database: %v", err)
	}
	defer s.Close()
	ctx := context.Background()
	var v int
	s.db.QueryRow(`PRAGMA user_version`).Scan(&v)
	if v != len(migrations) {
		t.Errorf("user_version = %d, want %d", v, len(migrations))
	}
	c, err := s.Company(ctx, 7)
	if err != nil || c.TemplateID == 0 || !strings.Contains(c.TemplateName, "Gammal förening") {
		t.Fatalf("company = %+v, %v", c, err)
	}
	chart, err := s.Chart(ctx, 7)
	if err != nil || len(chart) != 2 {
		t.Fatalf("chart = %+v, %v", chart, err)
	}
	for _, r := range chart {
		if !r.InTemplate || !r.Enabled || !r.InUse {
			t.Errorf("migrated account %+v should be an enabled template account in use", r)
		}
	}
	// Still bookable, and a second open is a no-op.
	ys, _ := s.Years(ctx, 7)
	if _, err := s.AddVoucher(ctx, 7, ys[0].ID, NewVoucher{Series: "V", Date: "2025-02-01", Text: "t",
		Lines: []Line{{Account: "1930", Amount: 100}, {Account: "3999", Amount: -100}}}); err != nil {
		t.Error(err)
	}
	s.Close()
	s2, err := Open(path)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	s2.Close()
}
