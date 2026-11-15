// Package ledger is the bookkeeping store: companies, fiscal years, accounts
// and vouchers, on SQLite. All amounts are integer öre.
package ledger

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"strconv"

	_ "modernc.org/sqlite"

	"fribok/internal/money"
	"fribok/internal/sie"
)

//go:embed schema.sql
var schema string

type Store struct{ db *sql.DB }

// Open opens (creating if needed) the SQLite database at path.
// Use ":memory:" for tests.
func Open(path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	if path == ":memory:" {
		dsn = "file::memory:?_pragma=foreign_keys(1)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if path == ":memory:" {
		db.SetMaxOpenConns(1) // each connection would otherwise get its own memory DB
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("creating schema: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

type Company struct {
	ID    int64
	Name  string
	OrgNr string
}

type FiscalYear struct {
	ID         int64
	Index      int
	Start, End string
}

type Account struct {
	Number, Name, Type string
}

type Line struct {
	Account string
	Amount  money.Ore
	Text    string
}

type Voucher struct {
	ID     int64
	Series string
	Number int
	Date   string
	Text   string
	Lines  []Line
}

type TrialRow struct {
	Account, Name string
	Opening       money.Ore
	Debit, Credit money.Ore // credit is positive here
	Closing       money.Ore
}

// accountType returns T/S/K/I; falls back on the BAS convention by first digit.
func accountType(a sie.Account) string {
	switch a.Type {
	case "T", "S", "K", "I":
		return a.Type
	}
	switch {
	case a.Number == "":
		return "K"
	case a.Number[0] == '1':
		return "T"
	case a.Number[0] == '2':
		return "S"
	case a.Number[0] == '3':
		return "I"
	default:
		return "K"
	}
}

// ImportSIE stores a parsed SIE document as a new company, atomically.
// Every voucher must balance and fall within a fiscal year of the file.
func (s *Store) ImportSIE(ctx context.Context, doc *sie.Document) (int64, error) {
	if doc.CompanyName == "" {
		return 0, fmt.Errorf("SIE file has no #FNAMN company name")
	}
	if len(doc.Years) == 0 {
		return 0, fmt.Errorf("SIE file has no #RAR fiscal year")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx,
		`INSERT INTO company (name, org_nr, currency, kp_type) VALUES (?,?,?,?)`,
		doc.CompanyName, doc.OrgNr, doc.Currency, doc.KPType)
	if err != nil {
		return 0, err
	}
	companyID, _ := res.LastInsertId()

	yearID := map[int]int64{}
	for _, y := range doc.Years {
		r, err := tx.ExecContext(ctx,
			`INSERT INTO fiscal_year (company_id, idx, start_date, end_date) VALUES (?,?,?,?)`,
			companyID, y.Index, y.Start, y.End)
		if err != nil {
			return 0, fmt.Errorf("fiscal year %d: %w", y.Index, err)
		}
		yearID[y.Index], _ = r.LastInsertId()
	}

	known := map[string]bool{}
	for _, a := range doc.Accounts {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO account (company_id, number, name, type) VALUES (?,?,?,?)`,
			companyID, a.Number, a.Name, accountType(a)); err != nil {
			return 0, fmt.Errorf("account %s: %w", a.Number, err)
		}
		known[a.Number] = true
	}

	for _, b := range doc.IB {
		id, ok := yearID[b.YearIndex]
		if !ok {
			return 0, fmt.Errorf("#IB refers to unknown year %d", b.YearIndex)
		}
		if !known[b.Account] {
			return 0, fmt.Errorf("#IB refers to undefined account %s", b.Account)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO opening_balance (fiscal_year_id, account, amount) VALUES (?,?,?)`,
			id, b.Account, int64(b.Amount)); err != nil {
			return 0, fmt.Errorf("#IB %d %s: %w", b.YearIndex, b.Account, err)
		}
	}

	for _, v := range doc.Vouchers {
		label := fmt.Sprintf("voucher %s%s", v.Series, v.Number)
		fy, ok := yearFor(doc.Years, v.Date)
		if !ok {
			return 0, fmt.Errorf("%s dated %s is outside every fiscal year", label, v.Date)
		}
		num, err := strconv.Atoi(v.Number)
		if err != nil {
			return 0, fmt.Errorf("%s: voucher number must be numeric", label)
		}
		var sum money.Ore
		for _, l := range v.Lines {
			if !known[l.Account] {
				return 0, fmt.Errorf("%s uses undefined account %s", label, l.Account)
			}
			sum += l.Amount
		}
		if sum != 0 {
			return 0, fmt.Errorf("%s does not balance (difference %s)", label, sum)
		}
		r, err := tx.ExecContext(ctx,
			`INSERT INTO voucher (company_id, fiscal_year_id, series, number, date, text) VALUES (?,?,?,?,?,?)`,
			companyID, yearID[fy], v.Series, num, v.Date, v.Text)
		if err != nil {
			return 0, fmt.Errorf("%s: %w", label, err)
		}
		vid, _ := r.LastInsertId()
		for i, l := range v.Lines {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO voucher_line (voucher_id, seq, account, amount, text) VALUES (?,?,?,?,?)`,
				vid, i+1, l.Account, int64(l.Amount), l.Text); err != nil {
				return 0, fmt.Errorf("%s line %d: %w", label, i+1, err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return companyID, nil
}

func yearFor(years []sie.Year, date string) (int, bool) {
	for _, y := range years {
		if date >= y.Start && date <= y.End { // ISO dates compare lexically
			return y.Index, true
		}
	}
	return 0, false
}

func (s *Store) Companies(ctx context.Context) ([]Company, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, org_nr FROM company ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Company
	for rows.Next() {
		var c Company
		if err := rows.Scan(&c.ID, &c.Name, &c.OrgNr); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) Company(ctx context.Context, id int64) (Company, error) {
	var c Company
	err := s.db.QueryRowContext(ctx, `SELECT id, name, org_nr FROM company WHERE id=?`, id).
		Scan(&c.ID, &c.Name, &c.OrgNr)
	return c, err
}

// Years returns the company's fiscal years, newest first.
func (s *Store) Years(ctx context.Context, companyID int64) ([]FiscalYear, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, idx, start_date, end_date FROM fiscal_year WHERE company_id=? ORDER BY start_date DESC`, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FiscalYear
	for rows.Next() {
		var y FiscalYear
		if err := rows.Scan(&y.ID, &y.Index, &y.Start, &y.End); err != nil {
			return nil, err
		}
		out = append(out, y)
	}
	return out, rows.Err()
}

func (s *Store) Accounts(ctx context.Context, companyID int64) ([]Account, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT number, name, type FROM account WHERE company_id=? ORDER BY number`, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Account
	for rows.Next() {
		var a Account
		if err := rows.Scan(&a.Number, &a.Name, &a.Type); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Vouchers returns all vouchers of a fiscal year with their lines,
// ordered by series and number.
func (s *Store) Vouchers(ctx context.Context, yearID int64) ([]Voucher, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, series, number, date, text FROM voucher WHERE fiscal_year_id=? ORDER BY series, number`, yearID)
	if err != nil {
		return nil, err
	}
	var out []Voucher
	byID := map[int64]int{}
	for rows.Next() {
		var v Voucher
		if err := rows.Scan(&v.ID, &v.Series, &v.Number, &v.Date, &v.Text); err != nil {
			rows.Close()
			return nil, err
		}
		byID[v.ID] = len(out)
		out = append(out, v)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	lrows, err := s.db.QueryContext(ctx,
		`SELECT l.voucher_id, l.account, l.amount, l.text
		   FROM voucher_line l JOIN voucher v ON v.id = l.voucher_id
		  WHERE v.fiscal_year_id=? ORDER BY l.voucher_id, l.seq`, yearID)
	if err != nil {
		return nil, err
	}
	defer lrows.Close()
	for lrows.Next() {
		var vid, amt int64
		var l Line
		if err := lrows.Scan(&vid, &l.Account, &amt, &l.Text); err != nil {
			return nil, err
		}
		l.Amount = money.Ore(amt)
		i := byID[vid]
		out[i].Lines = append(out[i].Lines, l)
	}
	return out, lrows.Err()
}

// TrialBalance returns, per account with an opening balance or any
// transactions in the year: opening balance, debit and credit turnover and
// the closing balance (opening + debit - credit).
func (s *Store) TrialBalance(ctx context.Context, companyID, yearID int64) ([]TrialRow, error) {
	rows, err := s.db.QueryContext(ctx, `
WITH t AS (
  SELECT l.account,
         SUM(CASE WHEN l.amount > 0 THEN l.amount ELSE 0 END)  AS d,
         SUM(CASE WHEN l.amount < 0 THEN -l.amount ELSE 0 END) AS c
    FROM voucher_line l JOIN voucher v ON v.id = l.voucher_id
   WHERE v.fiscal_year_id = ?
   GROUP BY l.account)
SELECT a.number, a.name, COALESCE(ib.amount, 0), COALESCE(t.d, 0), COALESCE(t.c, 0)
  FROM account a
  LEFT JOIN opening_balance ib ON ib.fiscal_year_id = ? AND ib.account = a.number
  LEFT JOIN t ON t.account = a.number
 WHERE a.company_id = ? AND (ib.account IS NOT NULL OR t.account IS NOT NULL)
 ORDER BY a.number`, yearID, yearID, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TrialRow
	for rows.Next() {
		var r TrialRow
		var ib, d, c int64
		if err := rows.Scan(&r.Account, &r.Name, &ib, &d, &c); err != nil {
			return nil, err
		}
		r.Opening, r.Debit, r.Credit = money.Ore(ib), money.Ore(d), money.Ore(c)
		r.Closing = r.Opening + r.Debit - r.Credit
		out = append(out, r)
	}
	return out, rows.Err()
}
