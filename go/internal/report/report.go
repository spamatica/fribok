// Package report builds the income statement (resultaträkning) and balance
// sheet (balansräkning) from a trial balance, grouping accounts by their BAS
// number ranges. Accounts that fall outside every named group are collected
// in an "unclassified" group, so a total never silently drops an account.
package report

import (
	"strconv"

	"fribok/internal/ledger"
	"fribok/internal/money"
)

type Kind int

const (
	Heading  Kind = iota // group title
	Account              // one account row
	Subtotal             // sum of one group
	Total                // sum over several groups
)

type Line struct {
	Kind    Kind
	Account string // Account rows only
	Label   string
	Amount  money.Ore
}

type Statement struct {
	Title string
	Lines []Line
	// Result is the year's result: income minus costs (positive is profit).
	Result money.Ore
	// Check must be zero: assets minus equity, provisions and liabilities
	// (including the calculated result). Only set on the balance sheet.
	Check money.Ore
}

// number maps an account number to a four digit key for range matching:
// "1930" -> 1930, "19301" -> 1930, "19" -> 1900. Non numeric gives -1.
func number(acct string) int {
	s := acct
	if len(s) > 4 {
		s = s[:4]
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return -1
	}
	for l := len(s); l < 4; l++ {
		n *= 10
	}
	return n
}

type builder struct {
	rows  []ledger.TrialRow
	sign  money.Ore // 1 or -1: turns the debit-positive closing balance into the shown amount
	used  map[int]bool
	lines []Line
}

func newBuilder(rows []ledger.TrialRow, sign money.Ore) *builder {
	return &builder{rows: rows, sign: sign, used: map[int]bool{}}
}

// group adds the accounts with from <= number <= to that have a non-zero
// balance, under a heading and with a sum line. It returns the sum.
func (b *builder) group(title string, from, to int) money.Ore {
	return b.collect(title, func(n int) bool { return n >= from && n <= to })
}

func (b *builder) collect(title string, match func(int) bool) money.Ore {
	var rows []ledger.TrialRow
	var sum money.Ore
	for i, r := range b.rows {
		if b.used[i] || !match(number(r.Account)) {
			continue
		}
		b.used[i] = true
		if r.Closing != 0 {
			rows = append(rows, r)
			sum += b.sign * r.Closing
		}
	}
	if len(rows) == 0 {
		return 0
	}
	b.lines = append(b.lines, Line{Kind: Heading, Label: title})
	for _, r := range rows {
		b.lines = append(b.lines, Line{Kind: Account, Account: r.Account, Label: r.Name, Amount: b.sign * r.Closing})
	}
	b.lines = append(b.lines, Line{Kind: Subtotal, Label: "Summa " + lower(title), Amount: sum})
	return sum
}

func (b *builder) total(label string, amount money.Ore) money.Ore {
	b.lines = append(b.lines, Line{Kind: Total, Label: label, Amount: amount})
	return amount
}

func lower(s string) string {
	if s == "" {
		return s
	}
	// Only the first letter: "Övriga externa kostnader" -> "övriga externa kostnader".
	r := []rune(s)
	switch r[0] {
	case 'Å':
		r[0] = 'å'
	case 'Ä':
		r[0] = 'ä'
	case 'Ö':
		r[0] = 'ö'
	default:
		if r[0] >= 'A' && r[0] <= 'Z' {
			r[0] += 'a' - 'A'
		}
	}
	return string(r)
}

// IncomeStatement shows income as positive amounts and costs as negative.
// Account 8999 ("Årets resultat", the closing account) is excluded; the
// result is calculated from the other result accounts.
func IncomeStatement(rows []ledger.TrialRow) Statement {
	var pl []ledger.TrialRow
	for _, r := range rows {
		if n := number(r.Account); n >= 3000 && n <= 8999 && r.Account != "8999" {
			pl = append(pl, r)
		}
	}
	b := newBuilder(pl, -1)
	netSales := b.group("Nettoomsättning", 3000, 3799)
	otherIncome := b.group("Övriga rörelseintäkter", 3800, 3999)
	income := b.total("Rörelsens intäkter", netSales+otherIncome)

	costs := b.group("Råvaror och förnödenheter", 4000, 4999)
	costs += b.group("Övriga externa kostnader", 5000, 6999)
	costs += b.group("Personalkostnader", 7000, 7699)
	costs += b.group("Av- och nedskrivningar", 7700, 7899)
	costs += b.group("Övriga rörelsekostnader", 7900, 7999)
	b.total("Rörelsens kostnader", costs)
	operating := b.total("Rörelseresultat", income+costs)

	fin := b.group("Finansiella intäkter", 8000, 8399)
	fin += b.group("Finansiella kostnader", 8400, 8799)
	afterFin := b.total("Resultat efter finansiella poster", operating+fin)

	disp := b.group("Bokslutsdispositioner", 8800, 8899)
	tax := b.group("Skatt", 8900, 8998)
	rest := b.collect("Ej klassificerade konton", func(int) bool { return true })
	result := b.total("Årets resultat", afterFin+disp+tax+rest)
	return Statement{Title: "Resultaträkning", Lines: b.lines, Result: result}
}

// BalanceSheet shows assets as debit balances and equity and liabilities as
// credit balances. The year's result is included in equity as a calculated
// line, so the sheet balances whether or not the year has been closed with a
// voucher: it is the net of all result accounts, closing account included.
func BalanceSheet(rows []ledger.TrialRow) Statement {
	var assets, eql []ledger.TrialRow
	var plSum money.Ore
	for _, r := range rows {
		n := number(r.Account)
		switch {
		case n >= 1000 && n <= 1999:
			assets = append(assets, r)
		case n >= 2000 && n <= 2999:
			eql = append(eql, r)
		default:
			plSum += r.Closing
		}
	}

	a := newBuilder(assets, 1)
	fixed := a.group("Immateriella anläggningstillgångar", 1000, 1099)
	fixed += a.group("Materiella anläggningstillgångar", 1100, 1299)
	fixed += a.group("Finansiella anläggningstillgångar", 1300, 1399)
	a.total("Summa anläggningstillgångar", fixed)
	current := a.group("Varulager m.m.", 1400, 1499)
	current += a.group("Kortfristiga fordringar", 1500, 1799)
	current += a.group("Kortfristiga placeringar", 1800, 1899)
	current += a.group("Kassa och bank", 1900, 1999)
	current += a.collect("Ej klassificerade tillgångar", func(int) bool { return true })
	a.total("Summa omsättningstillgångar", current)
	totalAssets := a.total("SUMMA TILLGÅNGAR", fixed+current)

	e := newBuilder(eql, -1)
	equity := e.group("Eget kapital", 2000, 2099)
	calc := -plSum
	if calc != 0 {
		e.lines = append(e.lines,
			Line{Kind: Heading, Label: "Beräknat resultat"},
			Line{Kind: Account, Label: "Årets resultat enligt resultaträkningen, ej bokfört mot eget kapital", Amount: calc},
			Line{Kind: Subtotal, Label: "Summa beräknat resultat", Amount: calc})
	}
	equity += calc
	if calc != 0 {
		e.total("Summa eget kapital inklusive beräknat resultat", equity)
	}
	liab := e.group("Obeskattade reserver", 2100, 2199)
	liab += e.group("Avsättningar", 2200, 2299)
	liab += e.group("Långfristiga skulder", 2300, 2399)
	liab += e.group("Kortfristiga skulder", 2400, 2999)
	liab += e.collect("Ej klassificerade skulder", func(int) bool { return true })
	e.total("Summa skulder och avsättningar", liab)
	totalEL := e.total("SUMMA EGET KAPITAL OCH SKULDER", equity+liab)

	lines := append(a.lines, e.lines...)
	return Statement{Title: "Balansräkning", Lines: lines, Result: calc, Check: totalAssets - totalEL}
}
