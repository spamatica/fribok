package ledger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"

	"fribok/internal/sie"
)

// A chart template (kontoplansmall, e.g. BAS) is a named reference list of
// accounts shared by companies. A company's own chart (table account) holds
// the accounts it has enabled; those are the ones that can be posted to and
// that appear in reports and exports. An enabled account that is not in the
// company's template is a custom account.

type TemplateInfo struct {
	ID        int64
	Name      string
	KPType    string // the #KPTYP of the source, e.g. EUBAS97
	Accounts  int
	Companies int // companies using the template
}

// ChartRow is one row of a company's chart: an account of its template, or a
// custom account.
type ChartRow struct {
	Number, Name, Type string
	InTemplate         bool // false: a custom account
	Enabled            bool
	InUse              bool // has postings or balances, so it cannot be disabled
}

// createTemplate stores a template. Number and name must not be empty; types
// come from #KTYP or, if missing, from the BAS class of the number.
func createTemplate(ctx context.Context, q dbtx, name, kpType string, accounts []sie.Account) (int64, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, invalid("Mallen måste ha ett namn")
	}
	res, err := q.ExecContext(ctx, `INSERT INTO chart_template (name, kp_type) VALUES (?,?)`, name, strings.TrimSpace(kpType))
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return 0, invalid("Det finns redan en mall med namnet %q", name)
		}
		return 0, err
	}
	id, _ := res.LastInsertId()
	for _, a := range accounts {
		num, nm := strings.TrimSpace(a.Number), strings.TrimSpace(a.Name)
		if num == "" || len(num) > 10 {
			return 0, invalid("Ogiltigt kontonummer %q i kontoplanen", a.Number)
		}
		if nm == "" {
			nm = num
		}
		if _, err := q.ExecContext(ctx,
			`INSERT INTO template_account (template_id, number, name, type) VALUES (?,?,?,?)`,
			id, num, nm, accountType(a)); err != nil {
			return 0, invalid("Konto %s förekommer flera gånger i kontoplanen", num)
		}
	}
	return id, nil
}

// ImportTemplate creates a template from the #KONTO records of a SIE file.
// An empty name defaults to the file's #KPTYP.
func (s *Store) ImportTemplate(ctx context.Context, name string, doc *sie.Document) (int64, error) {
	if strings.TrimSpace(name) == "" {
		name = doc.KPType
	}
	if len(doc.Accounts) == 0 {
		return 0, invalid("Filen innehåller ingen kontoplan (#KONTO)")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	id, err := createTemplate(ctx, tx, name, doc.KPType, doc.Accounts)
	if err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

// Templates lists the templates by name.
func (s *Store) Templates(ctx context.Context) ([]TemplateInfo, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT t.id, t.name, t.kp_type,
       (SELECT COUNT(*) FROM template_account a WHERE a.template_id = t.id),
       (SELECT COUNT(*) FROM company c WHERE c.template_id = t.id)
  FROM chart_template t ORDER BY t.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TemplateInfo
	for rows.Next() {
		var t TemplateInfo
		if err := rows.Scan(&t.ID, &t.Name, &t.KPType, &t.Accounts, &t.Companies); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// DeleteTemplate removes a template that no company uses.
func (s *Store) DeleteTemplate(ctx context.Context, id int64) error {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM company WHERE template_id=?`, id).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return invalid("Mallen används av %d företag och kan inte tas bort", n)
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM chart_template WHERE id=?`, id)
	if err != nil {
		return err
	}
	if c, _ := res.RowsAffected(); c == 0 {
		return invalid("Mallen finns inte")
	}
	return nil
}

// inUseSet returns the accounts of a company that have postings or balances.
func inUseSet(ctx context.Context, q dbtx, companyID int64) (map[string]bool, error) {
	rows, err := q.QueryContext(ctx, `
SELECT l.account FROM voucher_line l JOIN voucher v ON v.id = l.voucher_id WHERE v.company_id = ?1
UNION SELECT o.account FROM opening_balance o JOIN fiscal_year f ON f.id = o.fiscal_year_id WHERE f.company_id = ?1
UNION SELECT y.account FROM year_summary y JOIN fiscal_year f ON f.id = y.fiscal_year_id WHERE f.company_id = ?1`, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	set := map[string]bool{}
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			return nil, err
		}
		set[a] = true
	}
	return set, rows.Err()
}

// Chart returns the company's template accounts with their enabled state,
// followed (in number order) by its custom accounts.
func (s *Store) Chart(ctx context.Context, companyID int64) ([]ChartRow, error) {
	used, err := inUseSet(ctx, s.db, companyID)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT t.number, COALESCE(a.name, t.name), COALESCE(a.type, t.type), 1, a.number IS NOT NULL
  FROM template_account t
  JOIN company c ON c.id = ?1 AND c.template_id = t.template_id
  LEFT JOIN account a ON a.company_id = c.id AND a.number = t.number
UNION ALL
SELECT a.number, a.name, a.type, 0, 1
  FROM account a
 WHERE a.company_id = ?1
   AND NOT EXISTS (SELECT 1 FROM template_account t JOIN company c ON c.template_id = t.template_id
                    WHERE c.id = ?1 AND t.number = a.number)
ORDER BY 1`, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ChartRow
	for rows.Next() {
		var r ChartRow
		if err := rows.Scan(&r.Number, &r.Name, &r.Type, &r.InTemplate, &r.Enabled); err != nil {
			return nil, err
		}
		r.InUse = used[r.Number]
		out = append(out, r)
	}
	return out, rows.Err()
}

// enableAccount makes sure the account is in the company's chart, copying it
// from the company's template if necessary. It reports whether it was added.
func enableAccount(ctx context.Context, q dbtx, companyID int64, number string) (bool, error) {
	var one int
	err := q.QueryRowContext(ctx, `SELECT 1 FROM account WHERE company_id=? AND number=?`, companyID, number).Scan(&one)
	if err == nil {
		return false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	res, err := q.ExecContext(ctx, `
INSERT INTO account (company_id, number, name, type)
SELECT c.id, t.number, t.name, t.type
  FROM template_account t JOIN company c ON c.id = ? AND c.template_id = t.template_id
 WHERE t.number = ?`, companyID, number)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return false, invalid("Kontot %s finns inte i kontoplanen eller dess mall", number)
	}
	return true, nil
}

// EnableAccounts enables the given template accounts.
func (s *Store) EnableAccounts(ctx context.Context, companyID int64, numbers []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, n := range numbers {
		if _, err := enableAccount(ctx, tx, companyID, strings.TrimSpace(n)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SetEnabled applies the checkboxes of the chart page: of the template
// accounts in shown, those in on are enabled and the others disabled. Custom
// accounts are not affected. All or nothing: if any account to disable has
// postings or balances, nothing is changed.
func (s *Store) SetEnabled(ctx context.Context, companyID int64, shown, on []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	used, err := inUseSet(ctx, tx, companyID)
	if err != nil {
		return err
	}
	onSet := map[string]bool{}
	for _, n := range on {
		onSet[strings.TrimSpace(n)] = true
	}
	seen := map[string]bool{}
	var blocked []string
	for _, n := range shown {
		n = strings.TrimSpace(n)
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		var inTemplate int
		err := tx.QueryRowContext(ctx, `
SELECT 1 FROM template_account t JOIN company c ON c.id = ? AND c.template_id = t.template_id WHERE t.number = ?`,
			companyID, n).Scan(&inTemplate)
		if errors.Is(err, sql.ErrNoRows) {
			continue // a custom account or an unknown number: not togglable
		}
		if err != nil {
			return err
		}
		if onSet[n] {
			if _, err := enableAccount(ctx, tx, companyID, n); err != nil {
				return err
			}
			continue
		}
		if used[n] {
			blocked = append(blocked, n)
			continue
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM account WHERE company_id=? AND number=?`, companyID, n); err != nil {
			return err
		}
	}
	if len(blocked) > 0 {
		sort.Strings(blocked)
		list := strings.Join(blocked, ", ")
		if len(blocked) > 8 {
			list = strings.Join(blocked[:8], ", ") + fmt.Sprintf(" och %d till", len(blocked)-8)
		}
		return invalid("Kontona kan inte inaktiveras eftersom de har bokföringar eller saldon: %s", list)
	}
	return tx.Commit()
}

// accountAvailable reports whether the account is enabled or in the template.
func (s *Store) accountAvailable(ctx context.Context, companyID int64, number string) (bool, error) {
	var one int
	err := s.db.QueryRowContext(ctx, `
SELECT 1 FROM account WHERE company_id = ?1 AND number = ?2
UNION SELECT 1 FROM template_account t JOIN company c ON c.id = ?1 AND c.template_id = t.template_id WHERE t.number = ?2
LIMIT 1`, companyID, number).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}
