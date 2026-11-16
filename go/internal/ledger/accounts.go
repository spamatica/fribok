package ledger

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strings"
	"time"
)

var accountNumberRe = regexp.MustCompile(`^[1-9][0-9]{3,5}$`)

// DefaultAccountType returns the account type by BAS convention: class 1
// assets (T), class 2 equity and liabilities (S), class 3 income (I), classes
// 4-8 costs (K).
func DefaultAccountType(number string) string {
	switch {
	case number == "":
		return "K"
	case number[0] == '1':
		return "T"
	case number[0] == '2':
		return "S"
	case number[0] == '3':
		return "I"
	default:
		return "K"
	}
}

// CreateCompany creates a company with its first fiscal year. If templateID
// is not zero the company uses that chart template; enableAll then enables
// every account of the template, otherwise none are enabled yet. The first
// year may be up to 18 months long.
func (s *Store) CreateCompany(ctx context.Context, name, orgNr, start, end string, templateID int64, enableAll bool) (int64, error) {
	name, orgNr = strings.TrimSpace(name), strings.TrimSpace(orgNr)
	if name == "" {
		return 0, invalid("Företagsnamn saknas")
	}
	if len(name) > 200 || len(orgNr) > 20 {
		return 0, invalid("Namn eller organisationsnummer är för långt")
	}
	st, err := time.Parse("2006-01-02", start)
	if err != nil {
		return 0, invalid("Ogiltigt startdatum %q", start)
	}
	en, err := time.Parse("2006-01-02", end)
	if err != nil {
		return 0, invalid("Ogiltigt slutdatum %q", end)
	}
	if en.Before(st) {
		return 0, invalid("Räkenskapsårets slut ligger före dess start")
	}
	if en.After(st.AddDate(0, 18, -1)) {
		return 0, invalid("Ett räkenskapsår får vara högst 18 månader")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	taken, err := companyNameTaken(ctx, tx, name, 0)
	if err != nil {
		return 0, err
	}
	if taken {
		return 0, invalid("Det finns redan ett företag med namnet %q. Välj ett annat namn.", name)
	}
	kpType := ""
	var tpl any
	if templateID != 0 {
		err := tx.QueryRowContext(ctx, `SELECT kp_type FROM chart_template WHERE id=?`, templateID).Scan(&kpType)
		if errors.Is(err, sql.ErrNoRows) {
			return 0, invalid("Kontoplansmallen finns inte")
		}
		if err != nil {
			return 0, err
		}
		tpl = templateID
	}
	res, err := tx.ExecContext(ctx,
		`INSERT INTO company (name, org_nr, currency, kp_type, template_id) VALUES (?,?, 'SEK', ?, ?)`,
		name, orgNr, kpType, tpl)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO fiscal_year (company_id, idx, start_date, end_date) VALUES (?,0,?,?)`, id, start, end); err != nil {
		return 0, err
	}
	if templateID != 0 && enableAll {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO account (company_id, number, name, type) SELECT ?, number, name, type FROM template_account WHERE template_id=?`,
			id, templateID); err != nil {
			return 0, err
		}
	}
	return id, tx.Commit()
}

func validateAccount(number, name, typ string) (string, string, string, error) {
	number, name, typ = strings.TrimSpace(number), strings.TrimSpace(name), strings.TrimSpace(typ)
	if !accountNumberRe.MatchString(number) {
		return "", "", "", invalid("Kontonummer måste vara 4–6 siffror och börja med 1–9")
	}
	if name == "" || len(name) > 100 {
		return "", "", "", invalid("Kontonamn måste vara 1–100 tecken")
	}
	if typ == "" {
		typ = DefaultAccountType(number)
	}
	switch typ {
	case "T", "S", "K", "I":
	default:
		return "", "", "", invalid("Kontotyp måste vara T, S, K eller I")
	}
	return number, name, typ, nil
}

// AddAccount adds a custom account to the company's chart. An empty type is
// taken from the first digit of the number. Accounts of the company's
// template are enabled in the chart instead (see SetEnabled).
func (s *Store) AddAccount(ctx context.Context, companyID int64, number, name, typ string) error {
	number, name, typ, err := validateAccount(number, name, typ)
	if err != nil {
		return err
	}
	if _, err := s.Company(ctx, companyID); err != nil {
		return err
	}
	var one int
	err = s.db.QueryRowContext(ctx, `SELECT 1 FROM account WHERE company_id=? AND number=?`, companyID, number).Scan(&one)
	if err == nil {
		return invalid("Konto %s finns redan", number)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	inTemplate, err := s.accountAvailable(ctx, companyID, number)
	if err != nil {
		return err
	}
	if inTemplate {
		return invalid("Konto %s finns i kontoplansmallen: aktivera det i listan istället", number)
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO account (company_id, number, name, type) VALUES (?,?,?,?)`,
		companyID, number, name, typ)
	return err
}

// AccountInUse reports whether the account has postings or balances.
func (s *Store) AccountInUse(ctx context.Context, companyID int64, number string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `
SELECT (SELECT COUNT(*) FROM voucher_line l JOIN voucher v ON v.id = l.voucher_id WHERE v.company_id = ?1 AND l.account = ?2)
     + (SELECT COUNT(*) FROM opening_balance o JOIN fiscal_year f ON f.id = o.fiscal_year_id WHERE f.company_id = ?1 AND o.account = ?2)
     + (SELECT COUNT(*) FROM year_summary y JOIN fiscal_year f ON f.id = y.fiscal_year_id WHERE f.company_id = ?1 AND y.account = ?2)`,
		companyID, number).Scan(&n)
	return n > 0, err
}

// UpdateAccount renames an account and, if it is not in use, changes its type.
func (s *Store) UpdateAccount(ctx context.Context, companyID int64, number, name, typ string) error {
	_, name, typ, err := validateAccount(number, name, typ)
	if err != nil {
		return err
	}
	var oldType string
	err = s.db.QueryRowContext(ctx, `SELECT type FROM account WHERE company_id=? AND number=?`, companyID, number).Scan(&oldType)
	if errors.Is(err, sql.ErrNoRows) {
		return invalid("Konto %s finns inte", number)
	}
	if err != nil {
		return err
	}
	if typ != oldType {
		used, err := s.AccountInUse(ctx, companyID, number)
		if err != nil {
			return err
		}
		if used {
			return invalid("Kontotypen kan inte ändras eftersom kontot har bokföringar eller saldon")
		}
	}
	_, err = s.db.ExecContext(ctx, `UPDATE account SET name=?, type=? WHERE company_id=? AND number=?`,
		name, typ, companyID, number)
	return err
}

// DeleteAccount removes an account that has no postings or balances.
func (s *Store) DeleteAccount(ctx context.Context, companyID int64, number string) error {
	var one int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM account WHERE company_id=? AND number=?`, companyID, number).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return invalid("Konto %s finns inte", number)
	}
	if err != nil {
		return err
	}
	used, err := s.AccountInUse(ctx, companyID, number)
	if err != nil {
		return err
	}
	if used {
		return invalid("Kontot kan inte tas bort eftersom det har bokföringar eller saldon")
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM account WHERE company_id=? AND number=?`, companyID, number)
	return err
}

// Account returns one account.
func (s *Store) Account(ctx context.Context, companyID int64, number string) (Account, error) {
	var a Account
	err := s.db.QueryRowContext(ctx, `SELECT number, name, type FROM account WHERE company_id=? AND number=?`,
		companyID, number).Scan(&a.Number, &a.Name, &a.Type)
	return a, err
}
