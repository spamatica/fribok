package ledger

import (
	"context"
	"fmt"
	"strconv"

	"fribok/internal/money"
	"fribok/internal/sie"
)

// Export builds a SIE document with all of a company's fiscal years, accounts,
// opening balances, vouchers and, for every year that has data, the closing
// balances (#UB) of balance accounts and results (#RES) of result accounts.
func (s *Store) Export(ctx context.Context, companyID int64) (*sie.Document, error) {
	c, err := s.Company(ctx, companyID)
	if err != nil {
		return nil, err
	}
	doc := &sie.Document{
		SieType:     4,
		CompanyName: c.Name,
		OrgNr:       c.OrgNr,
		Currency:    c.Currency,
		KPType:      c.KPType,
	}
	accounts, err := s.Accounts(ctx, companyID)
	if err != nil {
		return nil, err
	}
	isBalance := map[string]bool{}
	for _, a := range accounts {
		doc.Accounts = append(doc.Accounts, sie.Account{Number: a.Number, Name: a.Name, Type: a.Type})
		isBalance[a.Number] = a.Type == "T" || a.Type == "S"
	}
	years, err := s.Years(ctx, companyID)
	if err != nil {
		return nil, err
	}
	// Oldest year first so that vouchers come out chronologically.
	for i := len(years) - 1; i >= 0; i-- {
		y := years[i]
		doc.Years = append(doc.Years, sie.Year{Index: y.Index, Start: y.Start, End: y.End})

		rows, err := s.db.QueryContext(ctx,
			`SELECT account, amount FROM opening_balance WHERE fiscal_year_id=? ORDER BY account`, y.ID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var acct string
			var amt int64
			if err := rows.Scan(&acct, &amt); err != nil {
				rows.Close()
				return nil, err
			}
			doc.IB = append(doc.IB, sie.Balance{YearIndex: y.Index, Account: acct, Amount: money.Ore(amt)})
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}

		trial, err := s.TrialBalance(ctx, companyID, y.ID)
		if err != nil {
			return nil, err
		}
		for _, r := range trial {
			b := sie.Balance{YearIndex: y.Index, Account: r.Account, Amount: r.Closing}
			if isBalance[r.Account] {
				doc.UB = append(doc.UB, b)
			} else {
				doc.Res = append(doc.Res, b)
			}
		}

		vs, err := s.Vouchers(ctx, y.ID)
		if err != nil {
			return nil, err
		}
		for _, v := range vs {
			sv := sie.Voucher{Series: v.Series, Number: strconv.Itoa(v.Number), Date: v.Date, Text: v.Text}
			for _, l := range v.Lines {
				sv.Lines = append(sv.Lines, sie.Line{Account: l.Account, Amount: l.Amount, Text: l.Text})
			}
			doc.Vouchers = append(doc.Vouchers, sv)
		}
	}
	if len(doc.Years) == 0 {
		return nil, fmt.Errorf("company %d has no fiscal years", companyID)
	}
	return doc, nil
}
