// Package ledger is the bookkeeping store: companies, fiscal years, accounts
// and vouchers, on SQLite. All amounts are integer öre.
package ledger

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"strconv"
	"strings"

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
	// Writes use BEGIN IMMEDIATE so that two concurrent voucher inserts
	// cannot both pick the same number.
	dsn := "file:" + path + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_txlock=immediate"
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
	st := &Store{db: db}
	if err := st.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrating schema: %w", err)
	}
	return st, nil
}

// migrations are applied in order, once each; PRAGMA user_version records how
// many have run. schema.sql is version 0 and is never changed: add new
// statements here instead, so that existing databases are upgraded.
var migrations = []string{
	`ALTER TABLE fiscal_year ADD COLUMN locked_until TEXT NOT NULL DEFAULT '';
	 ALTER TABLE voucher ADD COLUMN corrects_id INTEGER REFERENCES voucher(id);`,
	// Closing balances (#UB/#RES) of imported years that have neither vouchers
	// nor opening balances here, so that they can be shown and re-exported.
	`CREATE TABLE year_summary (
	    fiscal_year_id INTEGER NOT NULL REFERENCES fiscal_year(id) ON DELETE CASCADE,
	    account        TEXT NOT NULL,
	    closing        INTEGER NOT NULL,
	    PRIMARY KEY (fiscal_year_id, account)
	 );`,
	// Chart templates. Existing companies get a template of their own made
	// from their current accounts, which all count as enabled.
	`CREATE TABLE chart_template (
	    id      INTEGER PRIMARY KEY,
	    name    TEXT NOT NULL UNIQUE,
	    kp_type TEXT NOT NULL DEFAULT ''
	 );
	 CREATE TABLE template_account (
	    template_id INTEGER NOT NULL REFERENCES chart_template(id) ON DELETE CASCADE,
	    number      TEXT NOT NULL,
	    name        TEXT NOT NULL,
	    type        TEXT NOT NULL CHECK (type IN ('T','S','K','I')),
	    PRIMARY KEY (template_id, number)
	 );
	 ALTER TABLE company ADD COLUMN template_id INTEGER REFERENCES chart_template(id);
	 INSERT INTO chart_template (name, kp_type)
	   SELECT 'Kontoplan för företag ' || id || ' (' || name || ')', kp_type FROM company;
	 UPDATE company SET template_id =
	   (SELECT t.id FROM chart_template t WHERE t.name LIKE 'Kontoplan för företag ' || company.id || ' (%');
	 INSERT INTO template_account (template_id, number, name, type)
	   SELECT c.template_id, a.number, a.name, a.type FROM account a JOIN company c ON c.id = a.company_id;`,
}

func (s *Store) migrate() error {
	var v int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		return err
	}
	for i := v; i < len(migrations); i++ {
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, i+1)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) Close() error { return s.db.Close() }

type Company struct {
	ID       int64
	Name     string
	OrgNr    string
	Currency string
	KPType   string
	// TemplateID is the chart template the company's accounts come from, or 0.
	TemplateID   int64
	TemplateName string
}

const companyCols = `c.id, c.name, c.org_nr, c.currency, c.kp_type, COALESCE(c.template_id, 0), COALESCE(t.name, '')`

func scanCompany(sc interface{ Scan(...any) error }) (Company, error) {
	var c Company
	err := sc.Scan(&c.ID, &c.Name, &c.OrgNr, &c.Currency, &c.KPType, &c.TemplateID, &c.TemplateName)
	return c, err
}

type FiscalYear struct {
	ID         int64
	Index      int
	Start, End string
	// LockedUntil is the last locked date (inclusive), or "" if nothing is locked.
	LockedUntil string
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
	// CorrectsID/CorrectsLabel: set when this voucher reverses another one.
	CorrectsID    int64
	CorrectsLabel string
	// Corrected: a correction voucher exists for this voucher.
	Corrected bool
}

// Label is the voucher identifier as printed on paper, e.g. "V37".
func (v Voucher) Label() string { return fmt.Sprintf("%s%d", v.Series, v.Number) }

type TrialRow struct {
	Account, Name string
	Type          string // T, S, K or I
	// Imported: only Closing is known (from the #UB/#RES of an imported year
	// without transactions); Opening, Debit and Credit are zero.
	Imported      bool
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

	// The file's chart becomes (or reuses) a template; only the accounts the
	// file actually uses are enabled for the company.
	tplID, err := templateForImport(ctx, tx, doc)
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE company SET template_id=? WHERE id=?`, tplID, companyID); err != nil {
		return 0, err
	}
	used := usedAccounts(doc)
	known := map[string]bool{}
	for _, a := range doc.Accounts {
		if known[a.Number] {
			return 0, fmt.Errorf("account %s is declared twice", a.Number)
		}
		known[a.Number] = true
		if !used[a.Number] {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO account (company_id, number, name, type) VALUES (?,?,?,?)`,
			companyID, a.Number, a.Name, accountType(a)); err != nil {
			return 0, fmt.Errorf("account %s: %w", a.Number, err)
		}
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
	// Years without vouchers keep the closing balances of the file.
	hasData := map[int]bool{}
	for _, v := range doc.Vouchers {
		if fy, ok := yearFor(doc.Years, v.Date); ok {
			hasData[fy] = true
		}
	}
	for _, set := range [][]sie.Balance{doc.UB, doc.Res} {
		for _, b := range set {
			id, ok := yearID[b.YearIndex]
			if !ok || hasData[b.YearIndex] {
				continue
			}
			if !known[b.Account] {
				return 0, fmt.Errorf("#UB/#RES refers to undefined account %s", b.Account)
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO year_summary (fiscal_year_id, account, closing) VALUES (?,?,?)`,
				id, b.Account, int64(b.Amount)); err != nil {
				return 0, fmt.Errorf("closing balance %d %s: %w", b.YearIndex, b.Account, err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return companyID, nil
}

// usedAccounts is the set of accounts the file posts to or gives balances for.
func usedAccounts(doc *sie.Document) map[string]bool {
	used := map[string]bool{}
	for _, v := range doc.Vouchers {
		for _, l := range v.Lines {
			used[l.Account] = true
		}
	}
	for _, set := range [][]sie.Balance{doc.IB, doc.UB, doc.Res} {
		for _, b := range set {
			used[b.Account] = true
		}
	}
	return used
}

// templateForImport finds the template named like the file's #KPTYP, or
// creates one from the file's chart. Without #KPTYP the template is named
// after the company.
func templateForImport(ctx context.Context, tx *sql.Tx, doc *sie.Document) (int64, error) {
	name := strings.TrimSpace(doc.KPType)
	if name == "" {
		name = "Importerad kontoplan: " + doc.CompanyName
	}
	var id int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM chart_template WHERE name=?`, name).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	return createTemplate(ctx, tx, name, doc.KPType, doc.Accounts)
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
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+companyCols+` FROM company c LEFT JOIN chart_template t ON t.id = c.template_id ORDER BY c.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Company
	for rows.Next() {
		c, err := scanCompany(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) Company(ctx context.Context, id int64) (Company, error) {
	return scanCompany(s.db.QueryRowContext(ctx,
		`SELECT `+companyCols+` FROM company c LEFT JOIN chart_template t ON t.id = c.template_id WHERE c.id=?`, id))
}

// Years returns the company's fiscal years, newest first.
func (s *Store) Years(ctx context.Context, companyID int64) ([]FiscalYear, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, idx, start_date, end_date, locked_until FROM fiscal_year WHERE company_id=? ORDER BY start_date DESC`, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FiscalYear
	for rows.Next() {
		var y FiscalYear
		if err := rows.Scan(&y.ID, &y.Index, &y.Start, &y.End, &y.LockedUntil); err != nil {
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
		`SELECT v.id, v.series, v.number, v.date, v.text,
		        COALESCE(v.corrects_id, 0),
		        COALESCE((SELECT o.series || o.number FROM voucher o WHERE o.id = v.corrects_id), ''),
		        EXISTS (SELECT 1 FROM voucher c WHERE c.corrects_id = v.id)
		   FROM voucher v WHERE v.fiscal_year_id=? ORDER BY v.series, v.number`, yearID)
	if err != nil {
		return nil, err
	}
	var out []Voucher
	byID := map[int64]int{}
	for rows.Next() {
		var v Voucher
		if err := rows.Scan(&v.ID, &v.Series, &v.Number, &v.Date, &v.Text, &v.CorrectsID, &v.CorrectsLabel, &v.Corrected); err != nil {
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
	return trialBalance(ctx, s.db, companyID, yearID)
}

func trialBalance(ctx context.Context, q dbtx, companyID, yearID int64) ([]TrialRow, error) {
	if rows, err := importedTrial(ctx, q, companyID, yearID); err != nil || len(rows) > 0 {
		return rows, err
	}
	rows, err := q.QueryContext(ctx, `
WITH t AS (
  SELECT l.account,
         SUM(CASE WHEN l.amount > 0 THEN l.amount ELSE 0 END)  AS d,
         SUM(CASE WHEN l.amount < 0 THEN -l.amount ELSE 0 END) AS c
    FROM voucher_line l JOIN voucher v ON v.id = l.voucher_id
   WHERE v.fiscal_year_id = ?
   GROUP BY l.account)
SELECT a.number, a.name, a.type, COALESCE(ib.amount, 0), COALESCE(t.d, 0), COALESCE(t.c, 0)
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
		if err := rows.Scan(&r.Account, &r.Name, &r.Type, &ib, &d, &c); err != nil {
			return nil, err
		}
		r.Opening, r.Debit, r.Credit = money.Ore(ib), money.Ore(d), money.Ore(c)
		r.Closing = r.Opening + r.Debit - r.Credit
		out = append(out, r)
	}
	return out, rows.Err()
}

// importedTrial returns the imported closing balances of a year that has no
// vouchers, or nothing if the year has vouchers or no imported balances.
func importedTrial(ctx context.Context, q dbtx, companyID, yearID int64) ([]TrialRow, error) {
	rows, err := q.QueryContext(ctx, `
SELECT a.number, a.name, a.type, s.closing
  FROM year_summary s JOIN account a ON a.company_id = ? AND a.number = s.account
 WHERE s.fiscal_year_id = ?
   AND NOT EXISTS (SELECT 1 FROM voucher v WHERE v.fiscal_year_id = s.fiscal_year_id)
 ORDER BY a.number`, companyID, yearID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TrialRow
	for rows.Next() {
		r := TrialRow{Imported: true}
		var c int64
		if err := rows.Scan(&r.Account, &r.Name, &r.Type, &c); err != nil {
			return nil, err
		}
		r.Closing = money.Ore(c)
		out = append(out, r)
	}
	return out, rows.Err()
}

// Voucher returns one voucher with its lines.
func (s *Store) Voucher(ctx context.Context, companyID, voucherID int64) (Voucher, error) {
	var v Voucher
	err := s.db.QueryRowContext(ctx,
		`SELECT id, series, number, date, text FROM voucher WHERE id=? AND company_id=?`, voucherID, companyID).
		Scan(&v.ID, &v.Series, &v.Number, &v.Date, &v.Text)
	if err != nil {
		return v, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT account, amount, text FROM voucher_line WHERE voucher_id=? ORDER BY seq`, voucherID)
	if err != nil {
		return v, err
	}
	defer rows.Close()
	for rows.Next() {
		var l Line
		var amt int64
		if err := rows.Scan(&l.Account, &amt, &l.Text); err != nil {
			return v, err
		}
		l.Amount = money.Ore(amt)
		v.Lines = append(v.Lines, l)
	}
	return v, rows.Err()
}
