// Package report builds the income statement (resultaträkning) and balance
// sheet (balansräkning) from a trial balance, grouping accounts by their BAS
// number ranges. Accounts that fall outside every named group are collected
// in an "unclassified" group, so a total never silently drops an account.
package report

import (
	"sort"
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
	Prev    money.Ore // the previous year, if the statement has one
}

type Statement struct {
	Title   string
	Lines   []Line
	HasPrev bool
	// Result is the year's result: income minus costs (positive is profit).
	Result money.Ore
	// PrevResult is the same for the previous year.
	PrevResult money.Ore
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

// pair is an account with its closing balance in both years.
type pair struct {
	Account, Name string
	Cur, Prev     money.Ore
}

// merge joins the two trial balances on account number.
func merge(cur, prev []ledger.TrialRow) []pair {
	idx := map[string]int{}
	var out []pair
	for _, r := range cur {
		idx[r.Account] = len(out)
		out = append(out, pair{Account: r.Account, Name: r.Name, Cur: r.Closing})
	}
	for _, r := range prev {
		if i, ok := idx[r.Account]; ok {
			out[i].Prev = r.Closing
			continue
		}
		out = append(out, pair{Account: r.Account, Name: r.Name, Prev: r.Closing})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Account < out[j].Account })
	return out
}

// amount is a pair of sums, for the current and the previous year.
type amount struct{ cur, prev money.Ore }

func (a amount) plus(b amount) amount { return amount{a.cur + b.cur, a.prev + b.prev} }

type builder struct {
	rows  []pair
	sign  money.Ore // 1 or -1: turns the debit-positive closing balance into the shown amount
	used  map[int]bool
	lines []Line
}

func newBuilder(rows []pair, sign money.Ore) *builder {
	return &builder{rows: rows, sign: sign, used: map[int]bool{}}
}

// group adds the accounts with from <= number <= to that have a non-zero
// balance in either year, under a heading and with a sum line.
func (b *builder) group(title string, from, to int) amount {
	return b.collect(title, func(n int) bool { return n >= from && n <= to })
}

func (b *builder) collect(title string, match func(int) bool) amount {
	var rows []pair
	var sum amount
	for i, r := range b.rows {
		if b.used[i] || !match(number(r.Account)) {
			continue
		}
		b.used[i] = true
		if r.Cur != 0 || r.Prev != 0 {
			rows = append(rows, r)
			sum = sum.plus(amount{b.sign * r.Cur, b.sign * r.Prev})
		}
	}
	if len(rows) == 0 {
		return amount{}
	}
	b.lines = append(b.lines, Line{Kind: Heading, Label: title})
	for _, r := range rows {
		b.lines = append(b.lines, Line{Kind: Account, Account: r.Account, Label: r.Name,
			Amount: b.sign * r.Cur, Prev: b.sign * r.Prev})
	}
	b.lines = append(b.lines, Line{Kind: Subtotal, Label: "Summa " + lower(title), Amount: sum.cur, Prev: sum.prev})
	return sum
}

func (b *builder) total(label string, a amount) amount {
	b.lines = append(b.lines, Line{Kind: Total, Label: label, Amount: a.cur, Prev: a.prev})
	return a
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
// prev is the previous year's trial balance, or nil if there is none.
// Account 8999 ("Årets resultat", the closing account) is excluded; the
// result is calculated from the other result accounts.
func IncomeStatement(cur, prev []ledger.TrialRow) Statement {
	var pl []pair
	for _, r := range merge(cur, prev) {
		if n := number(r.Account); n >= 3000 && n <= 8999 && r.Account != "8999" {
			pl = append(pl, r)
		}
	}
	b := newBuilder(pl, -1)
	income := b.total("Rörelsens intäkter",
		b.group("Nettoomsättning", 3000, 3799).plus(b.group("Övriga rörelseintäkter", 3800, 3999)))

	costs := b.group("Råvaror och förnödenheter", 4000, 4999)
	costs = costs.plus(b.group("Övriga externa kostnader", 5000, 6999))
	costs = costs.plus(b.group("Personalkostnader", 7000, 7699))
	costs = costs.plus(b.group("Av- och nedskrivningar", 7700, 7899))
	costs = costs.plus(b.group("Övriga rörelsekostnader", 7900, 7999))
	b.total("Rörelsens kostnader", costs)
	operating := b.total("Rörelseresultat", income.plus(costs))

	fin := b.group("Finansiella intäkter", 8000, 8399)
	fin = fin.plus(b.group("Finansiella kostnader", 8400, 8799))
	afterFin := b.total("Resultat efter finansiella poster", operating.plus(fin))

	disp := b.group("Bokslutsdispositioner", 8800, 8899)
	tax := b.group("Skatt", 8900, 8998)
	rest := b.collect("Ej klassificerade konton", func(int) bool { return true })
	result := b.total("Årets resultat", afterFin.plus(disp).plus(tax).plus(rest))
	return Statement{Title: "Resultaträkning", Lines: b.lines, HasPrev: prev != nil,
		Result: result.cur, PrevResult: result.prev}
}

// BalanceSheet shows assets as debit balances and equity and liabilities as
// credit balances. The year's result is included in equity as a calculated
// line, so the sheet balances whether or not the year has been closed with a
// voucher: it is the net of all result accounts, closing account included.
// prev is the previous year's trial balance, or nil if there is none.
func BalanceSheet(cur, prev []ledger.TrialRow) Statement {
	var assets, eql []pair
	var plSum amount
	for _, r := range merge(cur, prev) {
		n := number(r.Account)
		switch {
		case n >= 1000 && n <= 1999:
			assets = append(assets, r)
		case n >= 2000 && n <= 2999:
			eql = append(eql, r)
		default:
			plSum = plSum.plus(amount{r.Cur, r.Prev})
		}
	}

	a := newBuilder(assets, 1)
	fixed := a.group("Immateriella anläggningstillgångar", 1000, 1099)
	fixed = fixed.plus(a.group("Materiella anläggningstillgångar", 1100, 1299))
	fixed = fixed.plus(a.group("Finansiella anläggningstillgångar", 1300, 1399))
	a.total("Summa anläggningstillgångar", fixed)
	current := a.group("Varulager m.m.", 1400, 1499)
	current = current.plus(a.group("Kortfristiga fordringar", 1500, 1799))
	current = current.plus(a.group("Kortfristiga placeringar", 1800, 1899))
	current = current.plus(a.group("Kassa och bank", 1900, 1999))
	current = current.plus(a.collect("Ej klassificerade tillgångar", func(int) bool { return true }))
	a.total("Summa omsättningstillgångar", current)
	totalAssets := a.total("SUMMA TILLGÅNGAR", fixed.plus(current))

	e := newBuilder(eql, -1)
	equity := e.group("Eget kapital", 2000, 2099)
	calc := amount{-plSum.cur, -plSum.prev}
	if calc != (amount{}) {
		e.lines = append(e.lines,
			Line{Kind: Heading, Label: "Beräknat resultat"},
			Line{Kind: Account, Label: "Årets resultat enligt resultaträkningen, ej bokfört mot eget kapital", Amount: calc.cur, Prev: calc.prev},
			Line{Kind: Subtotal, Label: "Summa beräknat resultat", Amount: calc.cur, Prev: calc.prev})
	}
	equity = equity.plus(calc)
	if calc != (amount{}) {
		e.total("Summa eget kapital inklusive beräknat resultat", equity)
	}
	liab := e.group("Obeskattade reserver", 2100, 2199)
	liab = liab.plus(e.group("Avsättningar", 2200, 2299))
	liab = liab.plus(e.group("Långfristiga skulder", 2300, 2399))
	liab = liab.plus(e.group("Kortfristiga skulder", 2400, 2999))
	liab = liab.plus(e.collect("Ej klassificerade skulder", func(int) bool { return true }))
	e.total("Summa skulder och avsättningar", liab)
	totalEL := e.total("SUMMA EGET KAPITAL OCH SKULDER", equity.plus(liab))

	return Statement{Title: "Balansräkning", Lines: append(a.lines, e.lines...), HasPrev: prev != nil,
		Result: calc.cur, PrevResult: calc.prev, Check: totalAssets.cur - totalEL.cur}
}
