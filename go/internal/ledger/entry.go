package ledger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"fribok/internal/money"
)

// ValidationError is a problem with user input, safe to show to the user.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

func invalid(format string, a ...any) error {
	return &ValidationError{Msg: fmt.Sprintf(format, a...)}
}

var seriesRe = regexp.MustCompile(`^[A-Za-z0-9]{1,8}$`)

// NewVoucher is a voucher to be posted. Lines use the store's sign
// convention: debit positive, credit negative.
type NewVoucher struct {
	Series string
	Date   string // ISO
	Text   string
	Lines  []Line
}

func (nv *NewVoucher) validate(fy FiscalYear) error {
	nv.Series = strings.TrimSpace(nv.Series)
	nv.Text = strings.TrimSpace(nv.Text)
	if !seriesRe.MatchString(nv.Series) {
		return invalid("Serie måste vara 1–8 bokstäver eller siffror")
	}
	if nv.Text == "" {
		return invalid("Verifikationstext saknas")
	}
	if len(nv.Text) > 200 {
		return invalid("Verifikationstexten är för lång (högst 200 tecken)")
	}
	if _, err := time.Parse("2006-01-02", nv.Date); err != nil {
		return invalid("Ogiltigt datum %q", nv.Date)
	}
	if nv.Date < fy.Start || nv.Date > fy.End {
		return invalid("Datum %s ligger utanför räkenskapsåret %s – %s", nv.Date, fy.Start, fy.End)
	}
	if fy.LockedUntil != "" && nv.Date <= fy.LockedUntil {
		return invalid("Perioden är låst till och med %s", fy.LockedUntil)
	}
	if len(nv.Lines) < 2 {
		return invalid("En verifikation måste ha minst två rader")
	}
	var sum money.Ore
	for i, l := range nv.Lines {
		nv.Lines[i].Account = strings.TrimSpace(l.Account)
		if nv.Lines[i].Account == "" {
			return invalid("Rad %d saknar konto", i+1)
		}
		if l.Amount == 0 {
			return invalid("Rad %d har beloppet noll", i+1)
		}
		sum += l.Amount
	}
	if sum != 0 {
		return invalid("Verifikationen balanserar inte: differens %s (debet minus kredit)", sum.Format())
	}
	return nil
}

type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func fiscalYear(ctx context.Context, q queryer, companyID, yearID int64) (FiscalYear, error) {
	fy := FiscalYear{ID: yearID}
	err := q.QueryRowContext(ctx,
		`SELECT idx, start_date, end_date, locked_until FROM fiscal_year WHERE id=? AND company_id=?`,
		yearID, companyID).Scan(&fy.Index, &fy.Start, &fy.End, &fy.LockedUntil)
	if errors.Is(err, sql.ErrNoRows) {
		return fy, invalid("Okänt räkenskapsår")
	}
	return fy, err
}

// addVoucher posts a voucher inside tx. It validates, checks the accounts,
// assigns the next gap-free number in the series and stores the lines.
func addVoucher(ctx context.Context, tx *sql.Tx, companyID, yearID int64, nv NewVoucher, corrects int64) (Voucher, error) {
	fy, err := fiscalYear(ctx, tx, companyID, yearID)
	if err != nil {
		return Voucher{}, err
	}
	if err := nv.validate(fy); err != nil {
		return Voucher{}, err
	}
	for _, l := range nv.Lines {
		var one int
		err := tx.QueryRowContext(ctx, `SELECT 1 FROM account WHERE company_id=? AND number=?`, companyID, l.Account).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) {
			return Voucher{}, invalid("Kontot %s finns inte i kontoplanen", l.Account)
		}
		if err != nil {
			return Voucher{}, err
		}
	}
	var num int
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(number), 0) + 1 FROM voucher WHERE fiscal_year_id=? AND series=?`,
		yearID, nv.Series).Scan(&num); err != nil {
		return Voucher{}, err
	}
	var corr any
	if corrects != 0 {
		corr = corrects
	}
	res, err := tx.ExecContext(ctx,
		`INSERT INTO voucher (company_id, fiscal_year_id, series, number, date, text, corrects_id) VALUES (?,?,?,?,?,?,?)`,
		companyID, yearID, nv.Series, num, nv.Date, nv.Text, corr)
	if err != nil {
		return Voucher{}, err
	}
	id, _ := res.LastInsertId()
	for i, l := range nv.Lines {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO voucher_line (voucher_id, seq, account, amount, text) VALUES (?,?,?,?,?)`,
			id, i+1, l.Account, int64(l.Amount), strings.TrimSpace(l.Text)); err != nil {
			return Voucher{}, err
		}
	}
	return Voucher{ID: id, Series: nv.Series, Number: num, Date: nv.Date, Text: nv.Text, Lines: nv.Lines, CorrectsID: corrects}, nil
}

// AddVoucher posts a new voucher in the given fiscal year. Posted vouchers are
// never edited or deleted (see CorrectVoucher), and numbers within a series
// have no gaps.
func (s *Store) AddVoucher(ctx context.Context, companyID, yearID int64, nv NewVoucher) (Voucher, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Voucher{}, err
	}
	defer tx.Rollback()
	v, err := addVoucher(ctx, tx, companyID, yearID, nv, 0)
	if err != nil {
		return Voucher{}, err
	}
	return v, tx.Commit()
}

// CorrectVoucher posts a reversing voucher that cancels voucherID, dated
// date (which may fall in a later fiscal year than the original). The reason
// is recorded in the voucher text. A voucher can be corrected only once.
// The reversal is linked to the original, so both stay in the books.
func (s *Store) CorrectVoucher(ctx context.Context, companyID, voucherID int64, date, reason string) (Voucher, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return Voucher{}, invalid("Ange en anledning till rättelsen")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Voucher{}, err
	}
	defer tx.Rollback()

	var orig NewVoucher
	var number int
	err = tx.QueryRowContext(ctx, `SELECT series, number, text FROM voucher WHERE id=? AND company_id=?`,
		voucherID, companyID).Scan(&orig.Series, &number, &orig.Text)
	if errors.Is(err, sql.ErrNoRows) {
		return Voucher{}, invalid("Okänd verifikation")
	}
	if err != nil {
		return Voucher{}, err
	}
	var already int
	err = tx.QueryRowContext(ctx, `SELECT id FROM voucher WHERE corrects_id=?`, voucherID).Scan(&already)
	if err == nil {
		return Voucher{}, invalid("Verifikationen är redan rättad")
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Voucher{}, err
	}

	var yearID int64
	err = tx.QueryRowContext(ctx,
		`SELECT id FROM fiscal_year WHERE company_id=? AND start_date <= ? AND end_date >= ?`,
		companyID, date, date).Scan(&yearID)
	if errors.Is(err, sql.ErrNoRows) {
		return Voucher{}, invalid("Datum %s ligger inte i något räkenskapsår", date)
	}
	if err != nil {
		return Voucher{}, err
	}

	rows, err := tx.QueryContext(ctx, `SELECT account, amount, text FROM voucher_line WHERE voucher_id=? ORDER BY seq`, voucherID)
	if err != nil {
		return Voucher{}, err
	}
	var lines []Line
	for rows.Next() {
		var l Line
		var amt int64
		if err := rows.Scan(&l.Account, &amt, &l.Text); err != nil {
			rows.Close()
			return Voucher{}, err
		}
		l.Amount = -money.Ore(amt)
		lines = append(lines, l)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return Voucher{}, err
	}

	text := fmt.Sprintf("Rättelse av %s%d: %s", orig.Series, number, reason)
	v, err := addVoucher(ctx, tx, companyID, yearID,
		NewVoucher{Series: orig.Series, Date: date, Text: text, Lines: lines}, voucherID)
	if err != nil {
		return Voucher{}, err
	}
	return v, tx.Commit()
}

// LockYear locks all dates up to and including until in a fiscal year: no
// voucher can be posted with a date on or before it. A lock can be moved
// forward but never lifted.
func (s *Store) LockYear(ctx context.Context, companyID, yearID int64, until string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	fy, err := fiscalYear(ctx, tx, companyID, yearID)
	if err != nil {
		return err
	}
	if _, err := time.Parse("2006-01-02", until); err != nil {
		return invalid("Ogiltigt datum %q", until)
	}
	if until < fy.Start || until > fy.End {
		return invalid("Datumet ligger utanför räkenskapsåret %s – %s", fy.Start, fy.End)
	}
	if until < fy.LockedUntil {
		return invalid("Perioden är redan låst till %s; en låsning kan inte hävas", fy.LockedUntil)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE fiscal_year SET locked_until=? WHERE id=?`, until, yearID); err != nil {
		return err
	}
	return tx.Commit()
}
