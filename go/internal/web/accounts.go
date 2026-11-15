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
)

// validationMsg returns the user-facing text of a validation error.
func validationMsg(err error) (string, bool) {
	var ve *ledger.ValidationError
	if errors.As(err, &ve) {
		return ve.Msg, true
	}
	return "", false
}

type companyForm struct {
	Name, OrgNr, Start, End string
	TemplateID              int64
	EnableAll               bool
}

func (s *Server) showIndex(w http.ResponseWriter, r *http.Request, f companyForm, errMsg string, status int) {
	cs, err := s.store.Companies(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	ts, err := s.store.Templates(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(status)
	s.render(w, "index.html", map[string]any{"Companies": cs, "Templates": ts, "Error": errMsg, "Form": f})
}

func defaultCompanyForm() companyForm {
	y := time.Now().Year()
	return companyForm{Start: fmt.Sprintf("%d-01-01", y), End: fmt.Sprintf("%d-12-31", y)}
}

func (s *Server) createCompany(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	f := companyForm{Name: r.PostForm.Get("name"), OrgNr: r.PostForm.Get("orgnr"),
		Start: r.PostForm.Get("start"), End: r.PostForm.Get("end"), EnableAll: r.PostForm.Get("enable") == "all"}
	if c := r.PostForm.Get("template"); c != "" {
		id, err := strconv.ParseInt(c, 10, 64)
		if err != nil {
			http.Error(w, "bad template", http.StatusBadRequest)
			return
		}
		f.TemplateID = id
	}
	id, err := s.store.CreateCompany(r.Context(), f.Name, f.OrgNr, f.Start, f.End, f.TemplateID, f.EnableAll)
	if err != nil {
		msg, ok := validationMsg(err)
		if !ok {
			s.fail(w, err)
			return
		}
		s.showIndex(w, r, f, msg, http.StatusUnprocessableEntity)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/c/%d/", id), http.StatusSeeOther)
}

type accountsPage struct {
	P         page
	Rows      []ledger.ChartRow
	Q         string
	OnlyOn    bool // show only enabled accounts
	Total     int  // accounts in the chart (template and custom)
	EnabledN  int
	Error     string
	Notice    string
	Form      ledger.Account // custom account form values
	HasTplate bool
}

// chartFilter reads the search box and the "only enabled" checkbox.
func chartFilter(get func(string) string) (q string, only bool) {
	return strings.ToLower(strings.TrimSpace(get("q"))), get("enabled") == "1"
}

func (s *Server) showAccounts(w http.ResponseWriter, r *http.Request, p page, form ledger.Account, errMsg string, status int) {
	all, err := s.store.Chart(r.Context(), p.Company.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	q, only := chartFilter(r.FormValue)
	pg := accountsPage{P: p, Q: q, OnlyOn: only, Total: len(all), Error: errMsg, Form: form,
		HasTplate: p.Company.TemplateID != 0, Notice: r.URL.Query().Get("notice")}
	for _, row := range all {
		if row.Enabled {
			pg.EnabledN++
		}
		if only && !row.Enabled {
			continue
		}
		if q == "" || strings.HasPrefix(row.Number, q) || strings.Contains(strings.ToLower(row.Name), q) {
			pg.Rows = append(pg.Rows, row)
		}
	}
	w.WriteHeader(status)
	s.render(w, "accounts.html", pg)
}

func (s *Server) accounts(w http.ResponseWriter, r *http.Request) {
	p, ok := s.load(w, r, false)
	if !ok {
		return
	}
	s.showAccounts(w, r, p, ledger.Account{}, "", http.StatusOK)
}

// saveChart applies the checkboxes of the chart page to the accounts shown.
func (s *Server) saveChart(w http.ResponseWriter, r *http.Request) {
	p, ok := s.load(w, r, false)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	shown, on := r.PostForm["shown"], r.PostForm["on"]
	if r.PostForm.Get("action") == "enable_shown" {
		on = shown
	}
	q, only := chartFilter(r.PostForm.Get)
	if err := s.store.SetEnabled(r.Context(), p.Company.ID, shown, on); err != nil {
		msg, ok := validationMsg(err)
		if !ok {
			s.fail(w, err)
			return
		}
		s.showAccounts(w, r, p, ledger.Account{}, msg, http.StatusUnprocessableEntity)
		return
	}
	v := url.Values{"notice": {"Kontoplanen är sparad."}}
	if q != "" {
		v.Set("q", q)
	}
	if only {
		v.Set("enabled", "1")
	}
	http.Redirect(w, r, fmt.Sprintf("/c/%d/accounts?%s", p.Company.ID, v.Encode()), http.StatusSeeOther)
}

func (s *Server) createAccount(w http.ResponseWriter, r *http.Request) {
	p, ok := s.load(w, r, false)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	form := ledger.Account{Number: r.PostForm.Get("number"), Name: r.PostForm.Get("name"), Type: r.PostForm.Get("type")}
	if err := s.store.AddAccount(r.Context(), p.Company.ID, form.Number, form.Name, form.Type); err != nil {
		msg, ok := validationMsg(err)
		if !ok {
			s.fail(w, err)
			return
		}
		s.showAccounts(w, r, p, form, msg, http.StatusUnprocessableEntity)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/c/%d/accounts?q=%s", p.Company.ID, strings.TrimSpace(form.Number)), http.StatusSeeOther)
}

type accountEditPage struct {
	P          page
	A          ledger.Account
	InUse      bool
	InTemplate bool
	Error      string
}

func (s *Server) showAccountEdit(w http.ResponseWriter, r *http.Request, p page, a ledger.Account, errMsg string, status int) {
	chart, err := s.store.Chart(r.Context(), p.Company.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	pg := accountEditPage{P: p, A: a, Error: errMsg}
	for _, row := range chart {
		if row.Number == a.Number {
			pg.InUse, pg.InTemplate = row.InUse, row.InTemplate
		}
	}
	w.WriteHeader(status)
	s.render(w, "account_edit.html", pg)
}

func (s *Server) editAccount(w http.ResponseWriter, r *http.Request) {
	p, ok := s.load(w, r, false)
	if !ok {
		return
	}
	a, err := s.store.Account(r.Context(), p.Company.ID, r.PathValue("number"))
	if err != nil {
		s.fail(w, err)
		return
	}
	s.showAccountEdit(w, r, p, a, "", http.StatusOK)
}

func (s *Server) updateAccount(w http.ResponseWriter, r *http.Request) {
	p, ok := s.load(w, r, false)
	if !ok {
		return
	}
	a, err := s.store.Account(r.Context(), p.Company.ID, r.PathValue("number"))
	if err != nil {
		s.fail(w, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	typed := ledger.Account{Number: a.Number, Name: r.PostForm.Get("name"), Type: r.PostForm.Get("type")}
	if err := s.store.UpdateAccount(r.Context(), p.Company.ID, a.Number, typed.Name, typed.Type); err != nil {
		msg, ok := validationMsg(err)
		if !ok {
			s.fail(w, err)
			return
		}
		s.showAccountEdit(w, r, p, typed, msg, http.StatusUnprocessableEntity)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/c/%d/accounts?q=%s", p.Company.ID, a.Number), http.StatusSeeOther)
}

func (s *Server) deleteAccount(w http.ResponseWriter, r *http.Request) {
	p, ok := s.load(w, r, false)
	if !ok {
		return
	}
	a, err := s.store.Account(r.Context(), p.Company.ID, r.PathValue("number"))
	if err != nil {
		s.fail(w, err)
		return
	}
	if err := s.store.DeleteAccount(r.Context(), p.Company.ID, a.Number); err != nil {
		msg, ok := validationMsg(err)
		if !ok {
			s.fail(w, err)
			return
		}
		s.showAccountEdit(w, r, p, a, msg, http.StatusUnprocessableEntity)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/c/%d/accounts", p.Company.ID), http.StatusSeeOther)
}
