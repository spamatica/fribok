package ledger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"fribok/internal/money"
	"fribok/internal/sie"
)

// MergeYear identifies a fiscal year in a MergeResult.
type MergeYear struct {
	Start, End string
}

// MergeResult describes what merging a SIE file into an existing company did.
// Years whose fiscal year already exists are either skipped (when they already
// have data) or updated in place (when the company only had imported closing
// balances for them and the file has the actual vouchers).
type MergeResult struct {
	Added   []MergeYear
	Updated []MergeYear
	Skipped []MergeYear
	// Accounts is the number of accounts the merge enabled (new account rows).
	Accounts int
	// Vouchers is the number of vouchers imported by the merge.
	Vouchers int
}

type mergeYearData struct {
	ib, ub, res []sie.Balance
	vouchers    []sie.Voucher
}

func (d *mergeYearData) empty() bool {
	return len(d.ib)+len(d.ub)+len(d.res)+len(d.vouchers) == 0
}

type mergeExistingYear struct {
	FiscalYear
	vouchers   int
	hasOpening bool
	hasSummary bool
}

type mergeAction int

const (
	mergeSkip mergeAction = iota
	mergeAdd
	mergeUpdate
)

type mergePlan struct {
	year     sie.Year
	data     *mergeYearData
	existing *mergeExistingYear
	action   mergeAction
	yearID   int64
}

// MergeSIE adds the fiscal years and vouchers of a parsed SIE document to an
// existing company. Years already present are left alone when they have
// vouchers or other data; a year that only has imported closing balances is
// replaced by the file's vouchers. The merge is atomic: on any error nothing
// is changed.
func (s *Store) MergeSIE(ctx context.Context, companyID int64, doc *sie.Document) (MergeResult, error) {
	var result MergeResult
	if doc == nil {
		return result, invalid("Ingen SIE-fil lästes in")
	}
	if len(doc.Years) == 0 {
		return result, invalid("SIE-filen saknar räkenskapsår (#RAR)")
	}
	known, data, err := validateMergeDocument(doc)
	if err != nil {
		return result, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()

	var one int
	err = tx.QueryRowContext(ctx, `SELECT 1 FROM company WHERE id=?`, companyID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return result, invalid("Företaget finns inte")
	}
	if err != nil {
		return result, err
	}

	existing, err := mergeExistingYears(ctx, tx, companyID)
	if err != nil {
		return result, err
	}
	plans, err := planMerge(doc, data, existing)
	if err != nil {
		return result, err
	}

	// Free the unique idx values first. New years get a temporary positive idx
	// and every year is renumbered by date at the end.
	if _, err := tx.ExecContext(ctx,
		`UPDATE fiscal_year SET idx = idx - 1000000 WHERE company_id=?`, companyID); err != nil {
		return result, err
	}
	nextNewIdx := 1000000
	changed := false
	for i := range plans {
		p := &plans[i]
		switch p.action {
		case mergeAdd:
			res, err := tx.ExecContext(ctx,
				`INSERT INTO fiscal_year (company_id, idx, start_date, end_date) VALUES (?,?,?,?)`,
				companyID, nextNewIdx, p.year.Start, p.year.End)
			if err != nil {
				return result, fmt.Errorf("räkenskapsår %s–%s: %w", p.year.Start, p.year.End, err)
			}
			nextNewIdx++
			p.yearID, _ = res.LastInsertId()
			result.Added = append(result.Added, MergeYear{p.year.Start, p.year.End})
			changed = true
		case mergeUpdate:
			p.yearID = p.existing.ID
			if _, err := tx.ExecContext(ctx,
				`DELETE FROM opening_balance WHERE fiscal_year_id=?`, p.yearID); err != nil {
				return result, err
			}
			if _, err := tx.ExecContext(ctx,
				`DELETE FROM year_summary WHERE fiscal_year_id=?`, p.yearID); err != nil {
				return result, err
			}
			result.Updated = append(result.Updated, MergeYear{p.year.Start, p.year.End})
			changed = true
		default:
			p.yearID = p.existing.ID
			result.Skipped = append(result.Skipped, MergeYear{p.year.Start, p.year.End})
		}
	}

	if changed {
		n, err := enableMergeAccounts(ctx, tx, companyID, doc, known)
		if err != nil {
			return result, err
		}
		result.Accounts = n
	}

	for _, p := range plans {
		if p.action == mergeSkip {
			continue
		}
		if err := writeMergeYear(ctx, tx, companyID, p.yearID, p.data); err != nil {
			return result, err
		}
		result.Vouchers += len(p.data.vouchers)
	}

	if err := renumberYears(ctx, tx, companyID); err != nil {
		return result, err
	}

	// Fill in company data that the target did not have.
	if doc.OrgNr != "" {
		if _, err := tx.ExecContext(ctx,
			`UPDATE company SET org_nr=? WHERE id=? AND org_nr=''`, doc.OrgNr, companyID); err != nil {
			return result, err
		}
	}
	if doc.KPType != "" {
		if _, err := tx.ExecContext(ctx,
			`UPDATE company SET kp_type=? WHERE id=? AND kp_type=''`, doc.KPType, companyID); err != nil {
			return result, err
		}
	}

	sortMergeYears(result.Added)
	sortMergeYears(result.Updated)
	sortMergeYears(result.Skipped)
	return result, tx.Commit()
}

// validateMergeDocument checks the parts of the document the merge relies on
// and groups its records by fiscal year index.
func validateMergeDocument(doc *sie.Document) (map[string]bool, map[int]*mergeYearData, error) {
	known := map[string]bool{}
	for _, a := range doc.Accounts {
		num := strings.TrimSpace(a.Number)
		if num == "" {
			return nil, nil, invalid("Kontoplanen innehåller ett konto utan nummer")
		}
		if known[num] {
			return nil, nil, invalid("Konto %s förekommer flera gånger i kontoplanen", num)
		}
		known[num] = true
	}

	yearIdx := map[int]bool{}
	for i, y := range doc.Years {
		if y.Start == "" || y.End == "" || y.Start > y.End {
			return nil, nil, invalid("Räkenskapsåret %s–%s har ogiltiga datum", y.Start, y.End)
		}
		if yearIdx[y.Index] {
			return nil, nil, invalid("Räkenskapsåret %s–%s förekommer flera gånger i filen", y.Start, y.End)
		}
		yearIdx[y.Index] = true
		for _, other := range doc.Years[:i] {
			if y.Start <= other.End && other.Start <= y.End {
				return nil, nil, invalid("Räkenskapsåren %s–%s och %s–%s överlappar varandra i filen",
					y.Start, y.End, other.Start, other.End)
			}
		}
	}

	data := map[int]*mergeYearData{}
	for _, y := range doc.Years {
		data[y.Index] = &mergeYearData{}
	}
	balance := func(label string, b sie.Balance) error {
		if _, ok := data[b.YearIndex]; !ok {
			return invalid("%s avser ett räkenskapsår som inte finns i filen (%d)", label, b.YearIndex)
		}
		if !known[b.Account] {
			return invalid("%s avser kontot %s, som inte finns i filens kontoplan", label, b.Account)
		}
		return nil
	}
	for _, b := range doc.IB {
		if err := balance("#IB", b); err != nil {
			return nil, nil, err
		}
		data[b.YearIndex].ib = append(data[b.YearIndex].ib, b)
	}
	for _, b := range doc.UB {
		if err := balance("#UB", b); err != nil {
			return nil, nil, err
		}
		data[b.YearIndex].ub = append(data[b.YearIndex].ub, b)
	}
	for _, b := range doc.Res {
		if err := balance("#RES", b); err != nil {
			return nil, nil, err
		}
		data[b.YearIndex].res = append(data[b.YearIndex].res, b)
	}

	seenVoucher := map[string]bool{}
	for _, v := range doc.Vouchers {
		idx, ok := yearFor(doc.Years, v.Date)
		if !ok {
			return nil, nil, invalid("Verifikation %s%s har datumet %s, som ligger utanför filens räkenskapsår",
				v.Series, v.Number, v.Date)
		}
		if _, err := strconv.Atoi(v.Number); err != nil {
			return nil, nil, invalid("Verifikation %s%s har ett verifikationsnummer som inte är ett tal",
				v.Series, v.Number)
		}
		key := fmt.Sprintf("%d/%s/%s", idx, v.Series, v.Number)
		if seenVoucher[key] {
			return nil, nil, invalid("Verifikation %s%s förekommer flera gånger i räkenskapsåret",
				v.Series, v.Number)
		}
		seenVoucher[key] = true
		var sum money.Ore
		for _, l := range v.Lines {
			if !known[l.Account] {
				return nil, nil, invalid("Verifikation %s%s använder kontot %s, som inte finns i filens kontoplan",
					v.Series, v.Number, l.Account)
			}
			sum += l.Amount
		}
		if sum != 0 {
			return nil, nil, invalid("Verifikation %s%s balanserar inte: differens %s",
				v.Series, v.Number, sum.Format())
		}
		data[idx].vouchers = append(data[idx].vouchers, v)
	}
	return known, data, nil
}

func mergeExistingYears(ctx context.Context, tx *sql.Tx, companyID int64) ([]mergeExistingYear, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT f.id, f.idx, f.start_date, f.end_date, f.locked_until,
       (SELECT COUNT(*) FROM voucher v WHERE v.fiscal_year_id = f.id),
       (SELECT COUNT(*) FROM opening_balance o WHERE o.fiscal_year_id = f.id),
       (SELECT COUNT(*) FROM year_summary s WHERE s.fiscal_year_id = f.id)
  FROM fiscal_year f WHERE f.company_id = ? ORDER BY f.start_date`, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []mergeExistingYear
	for rows.Next() {
		var y mergeExistingYear
		var opening, summary int
		if err := rows.Scan(&y.ID, &y.Index, &y.Start, &y.End, &y.LockedUntil,
			&y.vouchers, &opening, &summary); err != nil {
			return nil, err
		}
		y.hasOpening, y.hasSummary = opening > 0, summary > 0
		out = append(out, y)
	}
	return out, rows.Err()
}

func planMerge(doc *sie.Document, data map[int]*mergeYearData, existing []mergeExistingYear) ([]mergePlan, error) {
	byDates := map[string]*mergeExistingYear{}
	for i := range existing {
		byDates[existing[i].Start+"\x00"+existing[i].End] = &existing[i]
	}
	plans := make([]mergePlan, 0, len(doc.Years))
	for _, y := range doc.Years {
		p := mergePlan{year: y, data: data[y.Index]}
		if ex, ok := byDates[y.Start+"\x00"+y.End]; ok {
			p.existing = ex
			switch {
			case ex.vouchers > 0 && len(p.data.vouchers) > 0:
				return nil, invalid("Räkenskapsåret %s–%s finns redan med %d verifikationer; filens %d verifikationer kan inte läggas till",
					y.Start, y.End, ex.vouchers, len(p.data.vouchers))
			case ex.vouchers > 0:
				// The year already has books; the file's summary records add nothing.
				p.action = mergeSkip
			case len(p.data.vouchers) > 0:
				// A year known only from imported closing balances can be
				// replaced by the file's actual vouchers.
				if ex.LockedUntil != "" {
					return nil, invalid("Räkenskapsåret %s–%s är låst och kan inte ändras", y.Start, y.End)
				}
				p.action = mergeUpdate
			case ex.hasOpening || ex.hasSummary:
				// Do not overwrite existing opening or closing balances with a
				// summary-only year from another file.
				p.action = mergeSkip
			case p.data.empty():
				p.action = mergeSkip
			default:
				if ex.LockedUntil != "" {
					return nil, invalid("Räkenskapsåret %s–%s är låst och kan inte ändras", y.Start, y.End)
				}
				p.action = mergeUpdate
			}
			plans = append(plans, p)
			continue
		}
		for _, ex := range existing {
			if y.Start <= ex.End && ex.Start <= y.End {
				return nil, invalid("Räkenskapsåret %s–%s överlappar det befintliga räkenskapsåret %s–%s",
					y.Start, y.End, ex.Start, ex.End)
			}
		}
		p.action = mergeAdd
		plans = append(plans, p)
	}
	return plans, nil
}

func enableMergeAccounts(ctx context.Context, tx *sql.Tx, companyID int64, doc *sie.Document, known map[string]bool) (int, error) {
	used := usedAccounts(doc)
	added := 0
	for _, a := range doc.Accounts {
		num := strings.TrimSpace(a.Number)
		if !known[num] || !used[num] {
			continue
		}
		name := strings.TrimSpace(a.Name)
		if name == "" {
			name = num
		}
		res, err := tx.ExecContext(ctx,
			`INSERT INTO account (company_id, number, name, type) VALUES (?,?,?,?)
			 ON CONFLICT (company_id, number) DO NOTHING`,
			companyID, num, name, accountType(a))
		if err != nil {
			return added, fmt.Errorf("konto %s: %w", num, err)
		}
		if n, _ := res.RowsAffected(); n == 1 {
			added++
		}
	}
	return added, nil
}

func writeMergeYear(ctx context.Context, tx *sql.Tx, companyID, yearID int64, d *mergeYearData) error {
	for _, b := range d.ib {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO opening_balance (fiscal_year_id, account, amount) VALUES (?,?,?)`,
			yearID, b.Account, int64(b.Amount)); err != nil {
			return fmt.Errorf("#IB %s: %w", b.Account, err)
		}
	}
	if len(d.vouchers) > 0 {
		for _, v := range d.vouchers {
			num, _ := strconv.Atoi(v.Number)
			res, err := tx.ExecContext(ctx,
				`INSERT INTO voucher (company_id, fiscal_year_id, series, number, date, text) VALUES (?,?,?,?,?,?)`,
				companyID, yearID, v.Series, num, v.Date, v.Text)
			if err != nil {
				return fmt.Errorf("verifikation %s%s: %w", v.Series, v.Number, err)
			}
			vid, _ := res.LastInsertId()
			for i, l := range v.Lines {
				if _, err := tx.ExecContext(ctx,
					`INSERT INTO voucher_line (voucher_id, seq, account, amount, text) VALUES (?,?,?,?,?)`,
					vid, i+1, l.Account, int64(l.Amount), l.Text); err != nil {
					return fmt.Errorf("verifikation %s%s rad %d: %w", v.Series, v.Number, i+1, err)
				}
			}
		}
		return nil
	}
	// A year without vouchers keeps the file's closing balances.
	for _, set := range [][]sie.Balance{d.ub, d.res} {
		for _, b := range set {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO year_summary (fiscal_year_id, account, closing) VALUES (?,?,?)`,
				yearID, b.Account, int64(b.Amount)); err != nil {
				return fmt.Errorf("slutsaldo %s: %w", b.Account, err)
			}
		}
	}
	return nil
}

// renumberYears sets idx to 0 for the newest fiscal year, -1 for the one
// before it, and so on, as SIE does.
func renumberYears(ctx context.Context, tx *sql.Tx, companyID int64) error {
	rows, err := tx.QueryContext(ctx,
		`SELECT id FROM fiscal_year WHERE company_id=? ORDER BY start_date DESC, end_date DESC, id DESC`, companyID)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for i, id := range ids {
		if _, err := tx.ExecContext(ctx,
			`UPDATE fiscal_year SET idx=? WHERE id=?`, -i, id); err != nil {
			return err
		}
	}
	return nil
}

func sortMergeYears(years []MergeYear) {
	sort.Slice(years, func(i, j int) bool {
		if years[i].Start != years[j].Start {
			return years[i].Start < years[j].Start
		}
		return years[i].End < years[j].End
	})
}
