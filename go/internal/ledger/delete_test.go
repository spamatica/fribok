package ledger

import (
	"context"
	"strings"
	"testing"

	"fribok/internal/money"
)

func post(t *testing.T, s *Store, cid, yearID int64, series, date, text string, amount money.Ore) Voucher {
	t.Helper()
	v, err := s.AddVoucher(context.Background(), cid, yearID, NewVoucher{Series: series, Date: date, Text: text, Lines: twoLines(amount)})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestDeleteLastVoucher(t *testing.T) {
	ctx := context.Background()
	s, cid, cur, _ := setup(t)
	a := post(t, s, cid, cur.ID, "V", "2025-02-01", "första", 400)
	b := post(t, s, cid, cur.ID, "V", "2025-02-02", "andra", 700)

	if id, _ := s.LastDeletableVoucher(ctx, cid); id != b.ID {
		t.Errorf("deletable = %d, want %d", id, b.ID)
	}
	// Only the last one.
	_, err := s.DeleteLastVoucher(ctx, cid, a.ID, "")
	wantInvalid(t, err, "Bara den senast bokförda")
	_, err = s.DeleteLastVoucher(ctx, cid, b.ID+99, "")
	wantInvalid(t, err, "Bara den senast bokförda")
	_, err = s.DeleteLastVoucher(ctx, cid, b.ID, strings.Repeat("x", 201))
	wantInvalid(t, err, "för lång")

	yearID, err := s.DeleteLastVoucher(ctx, cid, b.ID, " fel belopp ")
	if err != nil {
		t.Fatal(err)
	}
	if yearID != cur.ID {
		t.Errorf("year id = %d, want %d", yearID, cur.ID)
	}
	// It is gone from the books, the earlier voucher is now the deletable one.
	vs, _ := s.Vouchers(ctx, cur.ID)
	if len(vs) != 2 || vs[1].ID != a.ID {
		t.Errorf("vouchers after delete: %+v", vs)
	}
	rows, _ := s.TrialBalance(ctx, cid, cur.ID)
	for _, r := range rows {
		if r.Account == "6570" && r.Debit != 400 {
			t.Errorf("6570 debit = %s after deleting the 700 voucher", r.Debit)
		}
	}
	if id, _ := s.LastDeletableVoucher(ctx, cid); id != a.ID {
		t.Errorf("deletable after delete = %d, want %d", id, a.ID)
	}

	// The number is reused, so numbering has no gap.
	c := post(t, s, cid, cur.ID, "V", "2025-02-03", "tredje", 100)
	if c.Number != b.Number {
		t.Errorf("new voucher number %d, want the freed %d", c.Number, b.Number)
	}

	// The log keeps everything.
	log, err := s.DeletedVouchers(ctx, cid)
	if err != nil || len(log) != 1 {
		t.Fatalf("log = %+v, %v", log, err)
	}
	d := log[0]
	if d.Series != "V" || d.Number != b.Number || d.Date != "2025-02-02" || d.Text != "andra" || d.Reason != "fel belopp" ||
		len(d.Lines) != 2 || d.Lines[0].Account != "6570" || d.Lines[0].Amount != 700 || d.Lines[1].Account != "1930" || d.Lines[1].Amount != -700 ||
		!strings.HasSuffix(d.DeletedAt, "Z") {
		t.Errorf("logged voucher = %+v", d)
	}
}

func TestDeleteRefusedInLockedPeriodAndWhenNothingToDelete(t *testing.T) {
	ctx := context.Background()
	s, cid, cur, _ := setup(t)
	// The imported voucher V1 is the only one.
	vs, _ := s.Vouchers(ctx, cur.ID)
	if err := s.LockYear(ctx, cid, cur.ID, "2025-03-31"); err != nil {
		t.Fatal(err)
	}
	if id, _ := s.LastDeletableVoucher(ctx, cid); id != 0 {
		t.Errorf("a voucher in a locked period is deletable: %d", id)
	}
	_, err := s.DeleteLastVoucher(ctx, cid, vs[0].ID, "")
	wantInvalid(t, err, "låst")
	if log, _ := s.DeletedVouchers(ctx, cid); len(log) != 0 {
		t.Error("a refused delete was logged")
	}
	if v, _ := s.Vouchers(ctx, cur.ID); len(v) != 1 {
		t.Error("a refused delete removed the voucher")
	}

	// A company without vouchers.
	empty, err := s.CreateCompany(ctx, "Tom", "", "2026-01-01", "2026-12-31", 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if id, _ := s.LastDeletableVoucher(ctx, empty); id != 0 {
		t.Errorf("deletable in an empty company: %d", id)
	}
	_, err = s.DeleteLastVoucher(ctx, empty, 1, "")
	wantInvalid(t, err, "ingen verifikation")
	// The vouchers of another company are out of reach.
	_, err = s.DeleteLastVoucher(ctx, empty, vs[0].ID, "")
	wantInvalid(t, err, "ingen verifikation")
}

func TestLastMeansLastPostedNotHighestNumber(t *testing.T) {
	ctx := context.Background()
	s, cid, cur, _ := setup(t)
	a := post(t, s, cid, cur.ID, "V", "2025-05-01", "v", 100)
	b := post(t, s, cid, cur.ID, "A", "2025-01-15", "annan serie, tidigare datum", 100)
	if id, _ := s.LastDeletableVoucher(ctx, cid); id != b.ID {
		t.Errorf("deletable = %d, want the one posted last (%d)", id, b.ID)
	}
	_, err := s.DeleteLastVoucher(ctx, cid, a.ID, "")
	wantInvalid(t, err, "Bara den senast")
	if _, err := s.DeleteLastVoucher(ctx, cid, b.ID, ""); err != nil {
		t.Fatal(err)
	}
	if id, _ := s.LastDeletableVoucher(ctx, cid); id != a.ID {
		t.Errorf("deletable after = %d, want %d", id, a.ID)
	}
}

func TestDeletingACorrectionRestoresTheOriginal(t *testing.T) {
	ctx := context.Background()
	s, cid, cur, _ := setup(t)
	orig := post(t, s, cid, cur.ID, "V", "2025-02-01", "fel", 500)
	corr, err := s.CorrectVoucher(ctx, cid, orig.ID, "2025-02-02", "slarv")
	if err != nil {
		t.Fatal(err)
	}
	if id, _ := s.LastDeletableVoucher(ctx, cid); id != corr.ID {
		t.Fatalf("deletable = %d, want the correction %d", id, corr.ID)
	}
	if _, err := s.DeleteLastVoucher(ctx, cid, corr.ID, "ångrade rättelsen"); err != nil {
		t.Fatal(err)
	}
	vs, _ := s.Vouchers(ctx, cur.ID)
	for _, v := range vs {
		if v.ID == orig.ID && v.Corrected {
			t.Error("the original is still marked as corrected")
		}
	}
	// ...and it can be corrected again.
	if _, err := s.CorrectVoucher(ctx, cid, orig.ID, "2025-02-03", "igen"); err != nil {
		t.Errorf("correcting again: %v", err)
	}
	log, _ := s.DeletedVouchers(ctx, cid)
	if len(log) != 1 || log[0].Corrects != "V2" || !strings.HasPrefix(log[0].Text, "Rättelse av V2") {
		t.Errorf("log = %+v", log)
	}
}

func TestDeletedLogSurvivesAcrossCompaniesAndOrder(t *testing.T) {
	ctx := context.Background()
	s, cid, cur, _ := setup(t)
	other, err := s.CreateCompany(ctx, "Annan", "", "2026-01-01", "2026-12-31", 0, false)
	if err != nil {
		t.Fatal(err)
	}
	post(t, s, cid, cur.ID, "V", "2025-02-01", "ett", 100)
	if _, err := s.DeleteLastVoucher(ctx, cid, mustLast(t, s, cid), "först"); err != nil {
		t.Fatal(err)
	}
	post(t, s, cid, cur.ID, "V", "2025-02-02", "två", 100)
	if _, err := s.DeleteLastVoucher(ctx, cid, mustLast(t, s, cid), "sist"); err != nil {
		t.Fatal(err)
	}
	log, _ := s.DeletedVouchers(ctx, cid)
	if len(log) != 2 || log[0].Reason != "sist" || log[1].Reason != "först" {
		t.Errorf("log order = %+v", log)
	}
	if log, _ := s.DeletedVouchers(ctx, other); len(log) != 0 {
		t.Errorf("another company sees %d entries", len(log))
	}
}

func mustLast(t *testing.T, s *Store, cid int64) int64 {
	t.Helper()
	id, err := s.LastDeletableVoucher(context.Background(), cid)
	if err != nil || id == 0 {
		t.Fatalf("no deletable voucher: %d %v", id, err)
	}
	return id
}
