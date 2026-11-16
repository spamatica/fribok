package web

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"fribok/internal/ledger"
	"fribok/internal/money"
	"fribok/internal/pdf"
	"fribok/internal/report"
)

const (
	pdfBottomMargin = 42.0
	pdfRowHeight    = 13.0
)

type pdfMeta struct {
	Company  ledger.Company
	Year     ledger.FiscalYear
	Title    string
	Subtitle string
}

type trialTotals struct {
	Opening, Debit, Credit, Closing money.Ore
}

// statementData loads the current and previous year's trial balances and
// builds a statement. Both the HTML page and the PDF use it.
func (s *Server) statementData(ctx context.Context, p page, build func(cur, prev []ledger.TrialRow) report.Statement) (report.Statement, ledger.FiscalYear, error) {
	rows, err := s.store.TrialBalance(ctx, p.Company.ID, p.Year.ID)
	if err != nil {
		return report.Statement{}, ledger.FiscalYear{}, err
	}
	var prevRows []ledger.TrialRow
	var prev ledger.FiscalYear
	// The previous year is the next older one in the list (newest first).
	for i, y := range p.Years {
		if y.ID != p.Year.ID || i+1 >= len(p.Years) {
			continue
		}
		prev = p.Years[i+1]
		if prevRows, err = s.store.TrialBalance(ctx, p.Company.ID, prev.ID); err != nil {
			return report.Statement{}, ledger.FiscalYear{}, err
		}
		if prevRows == nil {
			prevRows = []ledger.TrialRow{}
		}
	}
	return build(rows, prevRows), prev, nil
}

func (s *Server) trialData(ctx context.Context, p page) ([]ledger.TrialRow, trialTotals, error) {
	rows, err := s.store.TrialBalance(ctx, p.Company.ID, p.Year.ID)
	if err != nil {
		return nil, trialTotals{}, err
	}
	var tot trialTotals
	for _, x := range rows {
		tot.Opening += x.Opening
		tot.Debit += x.Debit
		tot.Credit += x.Credit
		tot.Closing += x.Closing
	}
	return rows, tot, nil
}

func pdfPageFooter(page, total int) string {
	return fmt.Sprintf("Sida %d av %d", page, total)
}

func pdfReportHeader(d *pdf.Doc, m pdfMeta) float64 {
	x, right := d.Margin(), d.Width()-d.Margin()
	d.Text(x, 45, 15, true, m.Company.Name)
	line := "Räkenskapsår " + m.Year.Start + " – " + m.Year.End
	if m.Company.OrgNr != "" {
		line = m.Company.OrgNr + " · " + line
	}
	d.Text(x, 61, 8.5, false, line)
	d.TextRight(right, 45, 8, false, "Utskrivet "+time.Now().Format("2006-01-02"))
	d.Text(x, 83, 13, true, m.Title)
	if m.Subtitle != "" {
		d.Text(x, 97, 8.5, false, m.Subtitle)
	}
	d.Line(x, 108, right, 0.8)
	return 124
}

// pdfEnsure starts a new page and draws the report header when the next row
// does not fit. It reports whether a page break happened.
func pdfEnsure(d *pdf.Doc, y *float64, need float64, m pdfMeta) bool {
	if *y+need <= d.Height()-pdfBottomMargin {
		return false
	}
	d.AddPage()
	*y = pdfReportHeader(d, m)
	return true
}

func (s *Server) writePDF(w http.ResponseWriter, base string, year ledger.FiscalYear, d *pdf.Doc) {
	data := d.Bytes()
	name := fmt.Sprintf("%s-%s.pdf", base, year.Start)
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Write(data)
}

func (s *Server) statementPDF(path string, build func(cur, prev []ledger.TrialRow) report.Statement) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := s.load(w, r, true)
		if !ok {
			return
		}
		st, prev, err := s.statementData(r.Context(), p, build)
		if err != nil {
			s.fail(w, err)
			return
		}
		meta := pdfMeta{Company: p.Company, Year: p.Year, Title: st.Title}
		if st.HasPrev {
			meta.Subtitle = "Jämförelseår " + prev.Start + " – " + prev.End
		}
		d := pdf.NewA4(false)
		d.SetFooter(pdfPageFooter)
		y := pdfReportHeader(d, meta)
		drawStatementPDF(d, meta, st, prev, y)
		base := "resultatrakning"
		if path == "balance" {
			base = "balansrakning"
		}
		s.writePDF(w, base, p.Year, d)
	}
}

func (s *Server) trialPDF(w http.ResponseWriter, r *http.Request) {
	p, ok := s.load(w, r, true)
	if !ok {
		return
	}
	rows, tot, err := s.trialData(r.Context(), p)
	if err != nil {
		s.fail(w, err)
		return
	}
	meta := pdfMeta{Company: p.Company, Year: p.Year, Title: "Saldobalans"}
	d := pdf.NewA4(false)
	d.SetFooter(pdfPageFooter)
	y := pdfReportHeader(d, meta)
	drawTrialPDF(d, meta, rows, tot, y)
	s.writePDF(w, "saldobalans", p.Year, d)
}

func (s *Server) ledgerPDF(w http.ResponseWriter, r *http.Request) {
	p, ok := s.load(w, r, true)
	if !ok {
		return
	}
	from, to := strings.TrimSpace(r.URL.Query().Get("from")), strings.TrimSpace(r.URL.Query().Get("to"))
	accounts, err := s.store.GeneralLedger(r.Context(), p.Company.ID, p.Year.ID, from, to)
	if err != nil {
		s.fail(w, err)
		return
	}
	meta := pdfMeta{Company: p.Company, Year: p.Year, Title: "Huvudbok"}
	switch {
	case from != "" && to != "":
		meta.Subtitle = "Konton " + from + " – " + to
	case from != "":
		meta.Subtitle = "Konton från " + from
	case to != "":
		meta.Subtitle = "Konton till " + to
	}
	d := pdf.NewA4(false)
	d.SetFooter(pdfPageFooter)
	y := pdfReportHeader(d, meta)
	drawLedgerPDF(d, meta, accounts, y)
	s.writePDF(w, "huvudbok", p.Year, d)
}

func drawStatementPDF(d *pdf.Doc, meta pdfMeta, st report.Statement, prev ledger.FiscalYear, y float64) {
	x, right := d.Margin(), d.Width()-d.Margin()
	accountW, amountW := 60.0, 90.0
	labelW := right - x - accountW - amountW
	if st.HasPrev {
		labelW -= amountW
	}
	labelX := x + accountW
	curRight := labelX + labelW + amountW
	prevRight := curRight + amountW

	tableHead := func(y float64) float64 {
		d.Text(x, y, 8.5, true, "Konto")
		d.Text(labelX, y, 8.5, true, "Benämning")
		d.TextRight(curRight, y, 8.5, true, meta.Year.End)
		if st.HasPrev {
			d.TextRight(prevRight, y, 8.5, true, prev.End)
		}
		d.Line(x, y+4, right, 0.6)
		return y + 16
	}
	y = tableHead(y)

	for _, line := range st.Lines {
		if pdfEnsure(d, &y, pdfRowHeight, meta) {
			y = tableHead(y)
		}
		switch line.Kind {
		case report.Heading:
			d.Text(x, y, 9.5, true, pdf.Fit(d, 9.5, true, right-x, line.Label))
			y += 15
		case report.Account:
			d.Text(x, y, 9, false, line.Account)
			d.Text(labelX, y, 9, false, pdf.Fit(d, 9, false, labelW, line.Label))
			d.TextRight(curRight, y, 9, false, line.Amount.Format())
			if st.HasPrev {
				d.TextRight(prevRight, y, 9, false, line.Prev.Format())
			}
			y += pdfRowHeight
		case report.Subtotal:
			d.Text(labelX, y, 9, true, pdf.Fit(d, 9, true, labelW, line.Label))
			d.TextRight(curRight, y, 9, true, line.Amount.Format())
			if st.HasPrev {
				d.TextRight(prevRight, y, 9, true, line.Prev.Format())
			}
			y += pdfRowHeight
		case report.Total:
			d.Line(x, y-9, right, 0.6)
			d.Text(labelX, y, 9, true, pdf.Fit(d, 9, true, labelW, line.Label))
			d.TextRight(curRight, y, 9, true, line.Amount.Format())
			if st.HasPrev {
				d.TextRight(prevRight, y, 9, true, line.Prev.Format())
			}
			y += 14
		}
	}
	d.Line(x, y-9, right, 0.8)
	if st.Check != 0 {
		y += 8
		d.Text(x, y, 9, true, "Balansräkningen balanserar inte: differens "+st.Check.Format())
	}
}

func drawTrialPDF(d *pdf.Doc, meta pdfMeta, rows []ledger.TrialRow, tot trialTotals, y float64) {
	x, right := d.Margin(), d.Width()-d.Margin()
	widths := []float64{55, 160, 75, 75, 75, 75}
	titles := []string{"Konto", "Namn", "Ing. balans", "Debet", "Kredit", "Utg. balans"}
	numeric := []bool{false, false, true, true, true, true}
	xs := make([]float64, len(widths)+1)
	xs[0] = x
	for i, w := range widths {
		xs[i+1] = xs[i] + w
	}

	tableHead := func(y float64) float64 {
		for i, title := range titles {
			if numeric[i] {
				d.TextRight(xs[i+1], y, 8.5, true, title)
			} else {
				d.Text(xs[i], y, 8.5, true, title)
			}
		}
		d.Line(x, y+4, right, 0.6)
		return y + 16
	}
	y = tableHead(y)

	for _, r := range rows {
		if pdfEnsure(d, &y, pdfRowHeight, meta) {
			y = tableHead(y)
		}
		d.Text(xs[0], y, 9, false, r.Account)
		d.Text(xs[1], y, 9, false, pdf.Fit(d, 9, false, widths[1]-4, r.Name))
		if r.Imported {
			d.Text(xs[2], y, 8.5, false, "importerad slutsaldo")
		} else {
			d.TextRight(xs[3], y, 9, false, r.Opening.Format())
			d.TextRight(xs[4], y, 9, false, r.Debit.Format())
			d.TextRight(xs[5], y, 9, false, r.Credit.Format())
		}
		d.TextRight(xs[6], y, 9, false, r.Closing.Format())
		y += pdfRowHeight
	}
	if len(rows) == 0 {
		d.Text(x, y, 9, false, "Inga bokförda belopp för året.")
		y += pdfRowHeight
	}

	if pdfEnsure(d, &y, 28, meta) {
		y = tableHead(y)
	}
	d.Line(x, y-9, right, 0.6)
	d.Text(x, y, 9, true, "Summa")
	d.TextRight(xs[3], y, 9, true, tot.Opening.Format())
	d.TextRight(xs[4], y, 9, true, tot.Debit.Format())
	d.TextRight(xs[5], y, 9, true, tot.Credit.Format())
	d.TextRight(xs[6], y, 9, true, tot.Closing.Format())
	d.Line(x, y+4, right, 0.8)
}

func drawLedgerPDF(d *pdf.Doc, meta pdfMeta, accounts []ledger.LedgerAccount, y float64) {
	x, right := d.Margin(), d.Width()-d.Margin()
	widths := []float64{55, 45, 235, 55, 55, 70}
	titles := []string{"Datum", "Ver.", "Text", "Debet", "Kredit", "Saldo"}
	numeric := []bool{false, false, false, true, true, true}
	xs := make([]float64, len(widths)+1)
	xs[0] = x
	for i, w := range widths {
		xs[i+1] = xs[i] + w
	}
	tableHead := func(y float64) float64 {
		for i, title := range titles {
			if numeric[i] {
				d.TextRight(xs[i+1], y, 8.5, true, title)
			} else {
				d.Text(xs[i], y, 8.5, true, title)
			}
		}
		d.Line(x, y+4, right, 0.6)
		return y + 16
	}
	accountHead := func(acct ledger.LedgerAccount, suffix string) float64 {
		label := acct.Account + " " + acct.Name + suffix
		d.Text(x, y+4, 10.5, true, pdf.Fit(d, 10.5, true, right-x, label))
		return y + 19
	}

	if len(accounts) == 0 {
		d.Text(x, y, 9, false, "Inga konton med saldo eller transaktioner.")
		return
	}
	for _, acct := range accounts {
		if pdfEnsure(d, &y, 66, meta) {
			// The page break already added the report header.
		}
		y = accountHead(acct, "")
		y = tableHead(y)
		d.Text(x, y, 9, false, "Ingående balans")
		d.TextRight(xs[6], y, 9, false, acct.Opening.Format())
		y += pdfRowHeight
		for _, e := range acct.Entries {
			if pdfEnsure(d, &y, pdfRowHeight, meta) {
				y = accountHead(acct, " (forts.)")
				y = tableHead(y)
			}
			d.Text(xs[0], y, 9, false, e.Date)
			d.Text(xs[1], y, 9, false, e.VoucherName)
			d.Text(xs[2], y, 9, false, pdf.Fit(d, 9, false, widths[2]-4, e.Text))
			if e.Amount > 0 {
				d.TextRight(xs[4], y, 9, false, e.Amount.Format())
			} else if e.Amount < 0 {
				d.TextRight(xs[5], y, 9, false, (-e.Amount).Format())
			}
			d.TextRight(xs[6], y, 9, false, e.Balance.Format())
			y += pdfRowHeight
		}
		if pdfEnsure(d, &y, 28, meta) {
			y = accountHead(acct, " (forts.)")
			y = tableHead(y)
		}
		d.Line(x, y-9, right, 0.6)
		d.Text(x, y, 9, true, "Utgående balans")
		d.TextRight(xs[4], y, 9, true, acct.Debit.Format())
		d.TextRight(xs[5], y, 9, true, acct.Credit.Format())
		d.TextRight(xs[6], y, 9, true, acct.Closing.Format())
		y += 22
	}
}
