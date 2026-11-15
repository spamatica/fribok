package ledger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"fribok/internal/money"
)

// closeSeries is the voucher series used for the year-end result voucher.
const closeSeries = "BS"

// Opening is one opening balance of the new year.
type Opening struct {
	Account, Name string
	Amount        money.Ore
}

// ClosePlan describes what closing a fiscal year does (or did).
type ClosePlan struct {
	Year          FiscalYear
	ResultAccount string // equity account that receives the year's result
	CarryAccount  string // equity account the result is carried to in the new year
	// Result is the result not yet booked against ResultAccount (profit
	// positive). Zero if the year already holds a closing voucher.
	Result       money.Ore
	NextStart    string
	NextEnd      string
	NextExists   bool
	Opening      []Opening
	VoucherLabel string // set when applied and a result voucher was posted
	// Enabled lists accounts that closing enables from the template because
	// it needs them (the result accounts and 8999).
	Enabled []string
}

// DefaultCloseAccounts proposes the account for the year's result (2099 or
// 2069) and the one for the carried result (2098 or 2091). Of each pair it
// prefers the account the company already uses, then an enabled one, then one
// in the company's template. Either may be "" if none of them exists.
func (s *Store) DefaultCloseAccounts(ctx context.Context, companyID int64) (result, carry string, err error) {
	used, err := inUseSet(ctx, s.db, companyID)
	if err != nil {
		return "", "", err
	}
	enabled, err := s.Accounts(ctx, companyID)
	if err != nil {
		return "", "", err
	}
	isEnabled := map[string]bool{}
	for _, a := range enabled {
		isEnabled[a.Number] = true
	}
	pick := func(cands ...string) (string, error) {
		for _, c := range cands {
			if used[c] {
				return c, nil
			}
		}
		for _, c := range cands {
			if isEnabled[c] {
				return c, nil
			}
		}
		for _, c := range cands {
			ok, err := s.accountAvailable(ctx, companyID, c)
			if err != nil {
				return "", err
			}
			if ok {
				return c, nil
			}
		}
		return "", nil
	}
	if result, err = pick("2099", "2069"); err != nil {
		return
	}
	carry, err = pick("2098", "2091")
	return
}

// PlanClose shows what CloseYear would do, without changing anything.
func (s *Store) PlanClose(ctx context.Context, companyID, yearID int64, resultAcct, carryAcct string) (ClosePlan, error) {
	return s.closeYear(ctx, companyID, yearID, resultAcct, carryAcct, false)
}

// CloseYear closes a fiscal year in one transaction:
//  1. Books the year's not yet booked result as a voucher in series "BS"
//     dated the last day: 8999 against ResultAccount. (Result accounts are
//     left as they are; a new year simply has no opening balance for them.)
//  2. Creates the following fiscal year if it does not exist.
//  3. Writes its opening balances: every balance account's closing balance,
//     with the result account's balance moved to CarryAccount.
//  4. Locks the closed year completely.
//
// Empty account arguments use DefaultCloseAccounts.
func (s *Store) CloseYear(ctx context.Context, companyID, yearID int64, resultAcct, carryAcct string) (ClosePlan, error) {
	return s.closeYear(ctx, companyID, yearID, resultAcct, carryAcct, true)
}

func (s *Store) closeYear(ctx context.Context, companyID, yearID int64, resultAcct, carryAcct string, apply bool) (ClosePlan, error) {
	var plan ClosePlan
	if resultAcct == "" || carryAcct == "" {
		dr, dc, err := s.DefaultCloseAccounts(ctx, companyID)
		if err != nil {
			return plan, err
		}
		if resultAcct == "" {
			resultAcct = dr
		}
		if carryAcct == "" {
			carryAcct = dc
		}
	}
	resultAcct, carryAcct = strings.TrimSpace(resultAcct), strings.TrimSpace(carryAcct)
	plan.ResultAccount, plan.CarryAccount = resultAcct, carryAcct

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return plan, err
	}
	defer tx.Rollback()

	fy, err := fiscalYear(ctx, tx, companyID, yearID)
	if err != nil {
		return plan, err
	}
	plan.Year = fy
	if fy.LockedUntil >= fy.End {
		return plan, invalid("Räkenskapsåret %s – %s är redan avslutat", fy.Start, fy.End)
	}
	if imp, err := hasImportedSummary(ctx, tx, yearID); err != nil {
		return plan, err
	} else if imp {
		return plan, invalid("Räkenskapsåret består av importerade slutsaldon utan verifikationer och kan inte avslutas")
	}
	for _, a := range []struct{ label, num string }{{"årets resultat", resultAcct}, {"balanserat resultat", carryAcct}} {
		if a.num == "" {
			return plan, invalid("Ange konto för %s", a.label)
		}
		newly, err := enableAccount(ctx, tx, companyID, a.num)
		if err != nil {
			return plan, invalid("Kontot %s (%s) finns inte i kontoplanen eller dess mall", a.num, a.label)
		}
		if newly {
			plan.Enabled = append(plan.Enabled, a.num)
		}
		var typ string
		if err := tx.QueryRowContext(ctx, `SELECT type FROM account WHERE company_id=? AND number=?`, companyID, a.num).Scan(&typ); err != nil {
			return plan, err
		}
		if typ != "S" {
			return plan, invalid("Kontot %s (%s) måste vara ett skuld- eller eget kapital-konto", a.num, a.label)
		}
	}
	if resultAcct == carryAcct {
		return plan, invalid("Konto för årets resultat och balanserat resultat måste vara olika")
	}

	// The following year: existing, or to be created directly after this one.
	endDay, _ := time.Parse("2006-01-02", fy.End)
	plan.NextStart = endDay.AddDate(0, 0, 1).Format("2006-01-02")
	plan.NextEnd = endDay.AddDate(0, 0, 1).AddDate(1, 0, -1).Format("2006-01-02")
	var nextID int64
	err = tx.QueryRowContext(ctx, `SELECT id, end_date FROM fiscal_year WHERE company_id=? AND start_date=?`,
		companyID, plan.NextStart).Scan(&nextID, &plan.NextEnd)
	switch {
	case err == nil:
		plan.NextExists = true
	case errors.Is(err, sql.ErrNoRows):
		var later int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM fiscal_year WHERE company_id=? AND start_date > ?`,
			companyID, fy.End).Scan(&later); err != nil {
			return plan, err
		}
		if later > 0 {
			return plan, invalid("Det finns ett senare räkenskapsår som inte börjar direkt efter %s", fy.End)
		}
	default:
		return plan, err
	}

	// 1. The result not yet booked: net of all result accounts including 8999.
	rows, err := trialBalance(ctx, tx, companyID, yearID)
	if err != nil {
		return plan, err
	}
	var pl money.Ore
	for _, r := range rows {
		if r.Type == "K" || r.Type == "I" {
			pl += r.Closing
		}
	}
	plan.Result = -pl
	if plan.Result != 0 {
		newly, err := enableAccount(ctx, tx, companyID, "8999")
		if err != nil {
			return plan, invalid("Kontot 8999 (Årets resultat) finns inte i kontoplanen eller dess mall")
		}
		if newly {
			plan.Enabled = append(plan.Enabled, "8999")
		}
		v, err := addVoucher(ctx, tx, companyID, yearID, NewVoucher{
			Series: closeSeries, Date: fy.End, Text: "Årets resultat",
			Lines: []Line{
				{Account: "8999", Amount: plan.Result},
				{Account: resultAcct, Amount: -plan.Result},
			}}, 0)
		if err != nil {
			return plan, err
		}
		plan.VoucherLabel = v.Label()
		if rows, err = trialBalance(ctx, tx, companyID, yearID); err != nil {
			return plan, err
		}
	}

	// 3. Opening balances: closing balances of balance accounts, with the
	// result account moved to the carry account.
	open := map[string]*Opening{}
	var order []string
	add := func(acct, name string, amt money.Ore) {
		o := open[acct]
		if o == nil {
			o = &Opening{Account: acct, Name: name}
			open[acct] = o
			order = append(order, acct)
		}
		o.Amount += amt
	}
	for _, r := range rows {
		if (r.Type != "T" && r.Type != "S") || r.Closing == 0 {
			continue
		}
		if r.Account == resultAcct {
			add(carryAcct, "", r.Closing)
		} else {
			add(r.Account, r.Name, r.Closing)
		}
	}
	var sum money.Ore
	for _, a := range order {
		if open[a].Name == "" {
			if err := tx.QueryRowContext(ctx, `SELECT name FROM account WHERE company_id=? AND number=?`, companyID, a).
				Scan(&open[a].Name); err != nil {
				return plan, err
			}
		}
		if open[a].Amount != 0 {
			plan.Opening = append(plan.Opening, *open[a])
			sum += open[a].Amount
		}
	}
	if sum != 0 {
		return plan, fmt.Errorf("opening balances do not sum to zero (%s); the books are out of balance", sum)
	}
	sortOpenings(plan.Opening)

	if !apply {
		return plan, nil
	}

	// 2. Create the following year, which becomes index 0 (SIE numbering).
	if !plan.NextExists {
		if _, err := tx.ExecContext(ctx, `UPDATE fiscal_year SET idx = idx - 1000000 WHERE company_id=?`, companyID); err != nil {
			return plan, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE fiscal_year SET idx = idx + 1000000 - 1 WHERE company_id=?`, companyID); err != nil {
			return plan, err
		}
		res, err := tx.ExecContext(ctx,
			`INSERT INTO fiscal_year (company_id, idx, start_date, end_date) VALUES (?,0,?,?)`,
			companyID, plan.NextStart, plan.NextEnd)
		if err != nil {
			return plan, err
		}
		nextID, _ = res.LastInsertId()
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM opening_balance WHERE fiscal_year_id=?`, nextID); err != nil {
		return plan, err
	}
	for _, o := range plan.Opening {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO opening_balance (fiscal_year_id, account, amount) VALUES (?,?,?)`,
			nextID, o.Account, int64(o.Amount)); err != nil {
			return plan, err
		}
	}
	// 4. Lock the closed year.
	if _, err := tx.ExecContext(ctx, `UPDATE fiscal_year SET locked_until=? WHERE id=?`, fy.End, yearID); err != nil {
		return plan, err
	}
	return plan, tx.Commit()
}

func sortOpenings(o []Opening) {
	for i := 1; i < len(o); i++ {
		for j := i; j > 0 && o[j].Account < o[j-1].Account; j-- {
			o[j], o[j-1] = o[j-1], o[j]
		}
	}
}
