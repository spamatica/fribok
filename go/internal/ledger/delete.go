package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"fribok/internal/money"
)

// Deleting a voucher is a narrow exception to "posted vouchers are never
// removed", meant for undoing a slip right after it was made:
//   - only the most recently posted voucher of the company can be deleted,
//   - never in a locked period,
//   - the voucher is kept in full in a log of deleted vouchers, with the time
//     and an optional reason,
//   - its number is reused by the next voucher, so numbering stays gap-free.

// DeletedVoucher is a row of the log of deleted vouchers.
type DeletedVoucher struct {
	ID        int64
	Series    string
	Number    int
	Date      string
	Text      string
	Corrects  string // label of the voucher it corrected, if it was a correction
	Reason    string
	DeletedAt string // RFC 3339, UTC
	Lines     []Line
}

type loggedLine struct {
	Account string `json:"account"`
	Amount  int64  `json:"amount"`
	Text    string `json:"text"`
}

// lastVoucher returns the most recently posted voucher of the company, or
// id 0 if there is none. "Most recent" is by posting order, not by date.
func lastVoucher(ctx context.Context, q dbtx, companyID int64) (id, yearID int64, date string, err error) {
	err = q.QueryRowContext(ctx,
		`SELECT id, fiscal_year_id, date FROM voucher WHERE company_id=? ORDER BY id DESC LIMIT 1`, companyID).
		Scan(&id, &yearID, &date)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, "", nil
	}
	return
}

// LastDeletableVoucher returns the id of the voucher that may be deleted now,
// or 0 if there is none (no vouchers, or the latest one is in a locked period).
func (s *Store) LastDeletableVoucher(ctx context.Context, companyID int64) (int64, error) {
	id, yearID, date, err := lastVoucher(ctx, s.db, companyID)
	if err != nil || id == 0 {
		return 0, err
	}
	fy, err := fiscalYear(ctx, s.db, companyID, yearID)
	if err != nil {
		return 0, err
	}
	if fy.LockedUntil != "" && date <= fy.LockedUntil {
		return 0, nil
	}
	return id, nil
}

// DeleteLastVoucher deletes the most recently posted voucher, if it is
// voucherID and not in a locked period, and logs it. It returns the id of the
// fiscal year the voucher belonged to.
func (s *Store) DeleteLastVoucher(ctx context.Context, companyID, voucherID int64, reason string) (int64, error) {
	reason = strings.TrimSpace(reason)
	if len(reason) > 200 {
		return 0, invalid("Anledningen är för lång (högst 200 tecken)")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	id, yearID, date, err := lastVoucher(ctx, tx, companyID)
	if err != nil {
		return 0, err
	}
	if id == 0 {
		return 0, invalid("Det finns ingen verifikation att radera")
	}
	if id != voucherID {
		return 0, invalid("Bara den senast bokförda verifikationen kan raderas")
	}
	fy, err := fiscalYear(ctx, tx, companyID, yearID)
	if err != nil {
		return 0, err
	}
	if fy.LockedUntil != "" && date <= fy.LockedUntil {
		return 0, invalid("Perioden är låst till och med %s", fy.LockedUntil)
	}

	var series, text, corrects string
	var number int
	if err := tx.QueryRowContext(ctx, `
SELECT v.series, v.number, v.text, COALESCE((SELECT o.series || o.number FROM voucher o WHERE o.id = v.corrects_id), '')
  FROM voucher v WHERE v.id = ?`, id).Scan(&series, &number, &text, &corrects); err != nil {
		return 0, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT account, amount, text FROM voucher_line WHERE voucher_id=? ORDER BY seq`, id)
	if err != nil {
		return 0, err
	}
	var lines []loggedLine
	for rows.Next() {
		var l loggedLine
		if err := rows.Scan(&l.Account, &l.Amount, &l.Text); err != nil {
			rows.Close()
			return 0, err
		}
		lines = append(lines, l)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	js, err := json.Marshal(lines)
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO deleted_voucher (company_id, series, number, date, text, lines, corrects, reason, deleted_at)
VALUES (?,?,?,?,?,?,?,?,?)`,
		companyID, series, number, date, text, string(js), corrects, reason, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM voucher WHERE id=?`, id); err != nil { // lines cascade
		return 0, err
	}
	return yearID, tx.Commit()
}

// DeletedVouchers returns the log of deleted vouchers, newest first.
func (s *Store) DeletedVouchers(ctx context.Context, companyID int64) ([]DeletedVoucher, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, series, number, date, text, lines, corrects, reason, deleted_at
  FROM deleted_voucher WHERE company_id=? ORDER BY id DESC`, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DeletedVoucher
	for rows.Next() {
		var d DeletedVoucher
		var js string
		if err := rows.Scan(&d.ID, &d.Series, &d.Number, &d.Date, &d.Text, &js, &d.Corrects, &d.Reason, &d.DeletedAt); err != nil {
			return nil, err
		}
		var ls []loggedLine
		if err := json.Unmarshal([]byte(js), &ls); err != nil {
			return nil, err
		}
		for _, l := range ls {
			d.Lines = append(d.Lines, Line{Account: l.Account, Amount: money.Ore(l.Amount), Text: l.Text})
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
