package web

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"fribok/internal/ledger"
	"fribok/internal/money"
)

const minFormRows = 8

type formRow struct{ Account, Debit, Credit, Text string }

type voucherForm struct {
	P        page
	YearID   int64
	Series   string
	Date     string
	Text     string
	Rows     []formRow
	Accounts []ledger.Account
	Error    string
	Notice   string
}

func padRows(rows []formRow) []formRow {
	n := minFormRows
	if len(rows)+2 > n {
		n = len(rows) + 2
	}
	for len(rows) < n {
		rows = append(rows, formRow{})
	}
	return rows
}

// defaultDate is today if it falls within the year, else the year's last day.
func defaultDate(y ledger.FiscalYear) string {
	today := time.Now().Format("2006-01-02")
	if today >= y.Start && today <= y.End {
		return today
	}
	return y.End
}

func (s *Server) newVoucherForm(w http.ResponseWriter, r *http.Request) {
	p, ok := s.load(w, r, true)
	if !ok {
		return
	}
	f := voucherForm{P: p, YearID: p.Year.ID, Series: "V", Date: defaultDate(p.Year),
		Notice: r.URL.Query().Get("notice")}
	// ?copy=<voucher id> prefills the form from an existing voucher, used
	// after a correction to enter the right postings.
	if c := r.URL.Query().Get("copy"); c != "" {
		vid, err := strconv.ParseInt(c, 10, 64)
		if err != nil {
			http.Error(w, "bad copy", http.StatusBadRequest)
			return
		}
		v, err := s.store.Voucher(r.Context(), p.Company.ID, vid)
		if err != nil {
			s.fail(w, err)
			return
		}
		f.Series, f.Text = v.Series, v.Text
		for _, l := range v.Lines {
			row := formRow{Account: l.Account, Text: l.Text}
			if l.Amount > 0 {
				row.Debit = l.Amount.String()
			} else {
				row.Credit = (-l.Amount).String()
			}
			f.Rows = append(f.Rows, row)
		}
	}
	s.showForm(w, r, f, http.StatusOK)
}

func (s *Server) showForm(w http.ResponseWriter, r *http.Request, f voucherForm, status int) {
	accts, err := s.store.Accounts(r.Context(), f.P.Company.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	f.Accounts = accts
	f.Rows = padRows(f.Rows)
	w.WriteHeader(status)
	s.render(w, "voucher_form.html", f)
}

// parseAmount reads a debit or credit cell: empty is zero, negative is an error.
func parseAmount(s string) (money.Ore, error) {
	s = strings.TrimSpace(strings.ReplaceAll(s, " ", ""))
	s = strings.ReplaceAll(s, " ", "")
	if s == "" {
		return 0, nil
	}
	v, err := money.Parse(s)
	if err != nil {
		return 0, err
	}
	if v < 0 {
		return 0, errors.New("negative amount")
	}
	return v, nil
}

func (s *Server) createVoucher(w http.ResponseWriter, r *http.Request) {
	p, ok := s.load(w, r, false)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	yearID, err := strconv.ParseInt(r.PostForm.Get("year"), 10, 64)
	if err != nil {
		http.Error(w, "bad year", http.StatusBadRequest)
		return
	}
	for _, y := range p.Years {
		if y.ID == yearID {
			p.Year = y
		}
	}
	f := voucherForm{P: p, YearID: yearID,
		Series: r.PostForm.Get("series"), Date: r.PostForm.Get("date"), Text: r.PostForm.Get("text")}
	nv := ledger.NewVoucher{Series: f.Series, Date: f.Date, Text: f.Text}

	acct, debit, credit, ltext := r.PostForm["acct"], r.PostForm["debit"], r.PostForm["credit"], r.PostForm["ltext"]
	at := func(a []string, i int) string {
		if i < len(a) {
			return a[i]
		}
		return ""
	}
	var formErr string
	for i := range acct {
		row := formRow{Account: at(acct, i), Debit: at(debit, i), Credit: at(credit, i), Text: at(ltext, i)}
		if strings.TrimSpace(row.Account+row.Debit+row.Credit+row.Text) == "" {
			continue
		}
		f.Rows = append(f.Rows, row)
		if formErr != "" {
			continue
		}
		d, derr := parseAmount(row.Debit)
		c, cerr := parseAmount(row.Credit)
		switch {
		case derr != nil || cerr != nil:
			formErr = fmt.Sprintf("Ogiltigt belopp på rad %d", len(f.Rows))
		case d != 0 && c != 0:
			formErr = fmt.Sprintf("Rad %d har både debet och kredit", len(f.Rows))
		default:
			nv.Lines = append(nv.Lines, ledger.Line{Account: row.Account, Amount: d - c, Text: row.Text})
		}
	}
	if formErr == "" {
		var v ledger.Voucher
		if v, err = s.store.AddVoucher(r.Context(), p.Company.ID, yearID, nv); err == nil {
			http.Redirect(w, r, fmt.Sprintf("/c/%d/vouchers?year=%d#v%d", p.Company.ID, yearID, v.ID), http.StatusSeeOther)
			return
		}
		var ve *ledger.ValidationError
		if !errors.As(err, &ve) {
			s.fail(w, err)
			return
		}
		formErr = ve.Msg
	}
	f.Error = formErr
	s.showForm(w, r, f, http.StatusUnprocessableEntity)
}

func (s *Server) correctVoucher(w http.ResponseWriter, r *http.Request) {
	p, ok := s.load(w, r, false)
	if !ok {
		return
	}
	vid, err := strconv.ParseInt(r.PathValue("vid"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	v, err := s.store.CorrectVoucher(r.Context(), p.Company.ID, vid, r.PostForm.Get("date"), r.PostForm.Get("reason"))
	if err != nil {
		var ve *ledger.ValidationError
		if errors.As(err, &ve) {
			http.Error(w, ve.Msg, http.StatusUnprocessableEntity)
			return
		}
		s.fail(w, err)
		return
	}
	// Continue with a new voucher prefilled with the postings that were
	// reversed, to be edited into the right ones.
	q := url.Values{"copy": {strconv.FormatInt(vid, 10)}, "notice": {
		fmt.Sprintf("Rättelseverifikation %s skapad. Bokför nu den korrekta verifikationen nedan.", v.Label())}}
	http.Redirect(w, r, fmt.Sprintf("/c/%d/vouchers/new?%s", p.Company.ID, q.Encode()), http.StatusSeeOther)
}

func (s *Server) lockYear(w http.ResponseWriter, r *http.Request) {
	p, ok := s.load(w, r, false)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	yearID, err := strconv.ParseInt(r.PostForm.Get("year"), 10, 64)
	if err != nil {
		http.Error(w, "bad year", http.StatusBadRequest)
		return
	}
	if err := s.store.LockYear(r.Context(), p.Company.ID, yearID, r.PostForm.Get("until")); err != nil {
		var ve *ledger.ValidationError
		if errors.As(err, &ve) {
			http.Error(w, ve.Msg, http.StatusUnprocessableEntity)
			return
		}
		s.fail(w, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/c/%d/", p.Company.ID), http.StatusSeeOther)
}

// sameOrigin rejects cross-site POSTs. The app has no login, so without this
// any web page the user visits could post forms to localhost.
func sameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
				http.Error(w, "cross-site request refused", http.StatusForbidden)
				return
			}
			if origin := r.Header.Get("Origin"); origin != "" {
				u, err := url.Parse(origin)
				if err != nil || u.Host != r.Host {
					http.Error(w, "cross-site request refused", http.StatusForbidden)
					return
				}
			}
			r.Body = http.MaxBytesReader(w, r.Body, maxUpload)
		}
		next.ServeHTTP(w, r)
	})
}

type closePage struct {
	P       page
	YearID  int64
	Plan    ledger.ClosePlan
	Result  string // account inputs, as typed
	Carry   string
	Error   string
	Planned bool // a plan could be computed
}

// closeYear serves the preview (GET) and the execution (POST) of year-end
// closing. The preview runs the same code as the execution, then rolls back.
func (s *Server) closeYear(w http.ResponseWriter, r *http.Request) {
	p, ok := s.load(w, r, false)
	if !ok {
		return
	}
	var get func(string) string
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		get = r.PostForm.Get
	} else {
		get = r.URL.Query().Get
	}
	yearID, err := strconv.ParseInt(get("year"), 10, 64)
	if err != nil {
		http.Error(w, "bad year", http.StatusBadRequest)
		return
	}
	cp := closePage{P: p, YearID: yearID, Result: strings.TrimSpace(get("result")), Carry: strings.TrimSpace(get("carry"))}
	if cp.Result == "" || cp.Carry == "" {
		dr, dc, err := s.store.DefaultCloseAccounts(r.Context(), p.Company.ID)
		if err != nil {
			s.fail(w, err)
			return
		}
		if cp.Result == "" {
			cp.Result = dr
		}
		if cp.Carry == "" {
			cp.Carry = dc
		}
	}

	if r.Method == http.MethodPost && get("confirm") == "yes" {
		_, err := s.store.CloseYear(r.Context(), p.Company.ID, yearID, cp.Result, cp.Carry)
		if err == nil {
			http.Redirect(w, r, fmt.Sprintf("/c/%d/", p.Company.ID), http.StatusSeeOther)
			return
		}
		var ve *ledger.ValidationError
		if !errors.As(err, &ve) {
			s.fail(w, err)
			return
		}
		cp.Error = ve.Msg
	}
	plan, err := s.store.PlanClose(r.Context(), p.Company.ID, yearID, cp.Result, cp.Carry)
	cp.Plan = plan
	status := http.StatusOK
	if err != nil {
		var ve *ledger.ValidationError
		if !errors.As(err, &ve) {
			s.fail(w, err)
			return
		}
		if cp.Error == "" {
			cp.Error = ve.Msg
		}
		status = http.StatusUnprocessableEntity
	} else {
		cp.Planned = true
	}
	if r.Method == http.MethodPost && cp.Error == "" {
		cp.Error = "Bekräfta avslutet med kryssrutan"
		status = http.StatusUnprocessableEntity
	}
	w.WriteHeader(status)
	s.render(w, "close.html", cp)
}
