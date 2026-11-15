package ledger

import (
	"context"
	"strconv"

	"fribok/internal/money"
)

// LedgerEntry is one transaction in an account's general ledger.
type LedgerEntry struct {
	Date        string
	VoucherID   int64
	VoucherName string // e.g. "V37"
	Text        string // voucher text, followed by the line text if any
	Amount      money.Ore
	Balance     money.Ore // running balance after this entry
}

// LedgerAccount is one account of the general ledger.
type LedgerAccount struct {
	Account, Name string
	Opening       money.Ore
	Entries       []LedgerEntry
	Debit, Credit money.Ore // turnover, credit positive
	Closing       money.Ore
}

// GeneralLedger returns every account that has an opening balance or
// transactions in the year, with its transactions in date order and a running
// balance. If from or to are not empty, only accounts with from <= number <= to
// are included (plain string comparison, as account numbers have equal length).
func (s *Store) GeneralLedger(ctx context.Context, companyID, yearID int64, from, to string) ([]LedgerAccount, error) {
	trial, err := s.TrialBalance(ctx, companyID, yearID)
	if err != nil {
		return nil, err
	}
	var out []LedgerAccount
	idx := map[string]int{}
	for _, r := range trial {
		if r.Imported || (from != "" && r.Account < from) || (to != "" && r.Account > to) {
			continue
		}
		idx[r.Account] = len(out)
		out = append(out, LedgerAccount{Account: r.Account, Name: r.Name, Opening: r.Opening,
			Debit: r.Debit, Credit: r.Credit, Closing: r.Closing})
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT l.account, v.date, v.id, v.series, v.number, v.text, l.text, l.amount
  FROM voucher_line l JOIN voucher v ON v.id = l.voucher_id
 WHERE v.fiscal_year_id = ?
 ORDER BY l.account, v.date, v.series, v.number, l.seq`, yearID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cur := ""
	var bal money.Ore
	for rows.Next() {
		var acct, date, series, vtext, ltext string
		var vid, amt int64
		var num int
		if err := rows.Scan(&acct, &date, &vid, &series, &num, &vtext, &ltext, &amt); err != nil {
			return nil, err
		}
		i, ok := idx[acct]
		if !ok {
			continue
		}
		if acct != cur {
			cur, bal = acct, out[i].Opening
		}
		bal += money.Ore(amt)
		text := vtext
		if ltext != "" {
			text += " – " + ltext
		}
		out[i].Entries = append(out[i].Entries, LedgerEntry{
			Date: date, VoucherID: vid, VoucherName: series + strconv.Itoa(num), Text: text,
			Amount: money.Ore(amt), Balance: bal})
	}
	return out, rows.Err()
}
