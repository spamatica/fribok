package ledger

import (
	"context"
	"database/sql"
	"errors"
)

// Deleting a voucher is a narrow exception to "posted vouchers are never
// removed", meant for undoing a slip right after it was made:
//   - only the most recently posted voucher of the company can be deleted,
//   - never in a locked period,
//   - its number is reused by the next voucher, so numbering stays gap-free.

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
// voucherID and not in a locked period. It returns the id of the fiscal year
// the voucher belonged to.
func (s *Store) DeleteLastVoucher(ctx context.Context, companyID, voucherID int64) (int64, error) {
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
	if _, err := tx.ExecContext(ctx, `DELETE FROM voucher WHERE id=?`, id); err != nil { // lines cascade
		return 0, err
	}
	return yearID, tx.Commit()
}
