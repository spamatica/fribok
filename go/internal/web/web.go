// Package web is the server-rendered HTML interface.
package web

import (
	"bytes"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"fribok/internal/ledger"
	"fribok/internal/money"
	"fribok/internal/report"
	"fribok/internal/sie"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

const maxUpload = 32 << 20

type Server struct {
	store *ledger.Store
	tmpl  *template.Template
}

func New(store *ledger.Store) *Server {
	funcs := template.FuncMap{
		"kr":  func(o money.Ore) string { return o.Format() },
		"neg": func(o money.Ore) money.Ore { return -o },
		// Replaced per request in render; this one only lets the templates parse.
		"active": func(string) bool { return false },
	}
	return &Server{
		store: store,
		tmpl:  template.Must(template.New("").Funcs(funcs).ParseFS(templateFS, "templates/*.html")),
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.index)
	mux.HandleFunc("POST /import", s.importSIE)
	mux.HandleFunc("GET /c/{id}/{$}", s.company)
	mux.HandleFunc("POST /companies", s.createCompany)
	mux.HandleFunc("GET /templates", s.templates)
	mux.HandleFunc("POST /templates", s.importTemplate)
	mux.HandleFunc("POST /templates/{id}/delete", s.deleteTemplate)
	mux.HandleFunc("POST /c/{id}/chart", s.saveChart)
	mux.HandleFunc("GET /c/{id}/accounts", s.accounts)
	mux.HandleFunc("POST /c/{id}/accounts", s.createAccount)
	mux.HandleFunc("GET /c/{id}/accounts/{number}", s.editAccount)
	mux.HandleFunc("POST /c/{id}/accounts/{number}", s.updateAccount)
	mux.HandleFunc("POST /c/{id}/accounts/{number}/delete", s.deleteAccount)
	mux.HandleFunc("GET /c/{id}/vouchers", s.vouchers)
	mux.HandleFunc("GET /c/{id}/trial", s.trial)
	mux.HandleFunc("GET /c/{id}/ledger", s.generalLedger)
	mux.HandleFunc("GET /c/{id}/income", s.statement("income", report.IncomeStatement))
	mux.HandleFunc("GET /c/{id}/balance", s.statement("balance", report.BalanceSheet))
	mux.HandleFunc("GET /c/{id}/export.se", s.exportSIE)
	mux.HandleFunc("GET /c/{id}/vouchers/new", s.newVoucherForm)
	mux.HandleFunc("POST /c/{id}/vouchers", s.createVoucher)
	mux.HandleFunc("POST /c/{id}/vouchers/{vid}/correct", s.correctVoucher)
	mux.HandleFunc("POST /c/{id}/vouchers/{vid}/delete", s.deleteVoucher)
	mux.HandleFunc("GET /c/{id}/deleted", s.deletedVouchers)
	mux.HandleFunc("POST /c/{id}/lock", s.lockYear)
	mux.HandleFunc("GET /c/{id}/close", s.closeYear)
	mux.HandleFunc("POST /c/{id}/close", s.closeYear)
	mux.Handle("GET /static/", http.FileServerFS(staticFS))
	return withPath(sameOrigin(mux))
}

// pathWriter carries the request path to render, so that the top bar can
// mark the current page without every handler passing it on.
type pathWriter struct {
	http.ResponseWriter
	path string
}

func (w *pathWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func withPath(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(&pathWriter{ResponseWriter: w, path: r.URL.Path}, r)
	})
}

// sectionOf names the part of the app a request path belongs to, as used by
// the top bar: "accounts", "vouchers", "ledger", "trial", "income" and
// "balance" for those pages of a company, "" for the company's overview (and
// the pages reached from it), and "home" or "templates" outside companies.
func sectionOf(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if parts[0] == "c" {
		if len(parts) < 3 {
			return ""
		}
		switch parts[2] {
		case "accounts", "vouchers", "ledger", "trial", "income", "balance":
			return parts[2]
		case "deleted": // the log of deleted vouchers
			return "vouchers"
		}
		return ""
	}
	if parts[0] == "templates" {
		return "templates"
	}
	return "home"
}

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	t, err := s.tmpl.Clone() // the base set is never executed, so it stays cloneable
	if err != nil {
		log.Printf("render %s: %v", name, err)
		return
	}
	section := ""
	if pw, ok := w.(*pathWriter); ok {
		section = sectionOf(pw.path)
	}
	t.Funcs(template.FuncMap{"active": func(sec string) bool { return sec == section }})
	if err := t.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("render %s: %v", name, err)
	}
}

func (s *Server) fail(w http.ResponseWriter, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, nil)
		return
	}
	log.Printf("error: %v", err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	s.showIndex(w, r, defaultCompanyForm(), r.URL.Query().Get("error"), http.StatusOK)
}

func (s *Server) importSIE(w http.ResponseWriter, r *http.Request) {
	f, _, err := r.FormFile("file")
	if err != nil {
		http.Redirect(w, r, "/?error="+template.URLQueryEscaper("no file received"), http.StatusSeeOther)
		return
	}
	defer f.Close()
	doc, err := sie.Parse(f)
	if err == nil {
		var id int64
		if id, err = s.store.ImportSIE(r.Context(), doc); err == nil {
			http.Redirect(w, r, fmt.Sprintf("/c/%d/", id), http.StatusSeeOther)
			return
		}
	}
	http.Redirect(w, r, "/?error="+template.URLQueryEscaper("import failed: "+err.Error()), http.StatusSeeOther)
}

// companyCtx loads the company from the path and, when withYear is set, the
// selected fiscal year (?year=<idx>, default the newest).
type page struct {
	Company ledger.Company
	Years   []ledger.FiscalYear
	Year    ledger.FiscalYear
}

func (s *Server) load(w http.ResponseWriter, r *http.Request, withYear bool) (page, bool) {
	var p page
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return p, false
	}
	ctx := r.Context()
	if p.Company, err = s.store.Company(ctx, id); err != nil {
		s.fail(w, err)
		return p, false
	}
	if p.Years, err = s.store.Years(ctx, id); err != nil {
		s.fail(w, err)
		return p, false
	}
	if withYear && len(p.Years) > 0 {
		p.Year = p.Years[0]
		if q := r.URL.Query().Get("year"); q != "" {
			want, err := strconv.ParseInt(q, 10, 64)
			if err != nil {
				http.Error(w, "bad year", http.StatusBadRequest)
				return p, false
			}
			found := false
			for _, y := range p.Years {
				if y.ID == want {
					p.Year, found = y, true
				}
			}
			if !found {
				http.NotFound(w, r)
				return p, false
			}
		}
	}
	return p, true
}

func (s *Server) company(w http.ResponseWriter, r *http.Request) {
	p, ok := s.load(w, r, false)
	if !ok {
		return
	}
	s.render(w, "company.html", p)
}

func (s *Server) vouchers(w http.ResponseWriter, r *http.Request) {
	p, ok := s.load(w, r, true)
	if !ok {
		return
	}
	vs, err := s.store.Vouchers(r.Context(), p.Year.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	deletable, err := s.store.LastDeletableVoucher(r.Context(), p.Company.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.render(w, "vouchers.html", map[string]any{"P": p, "Vouchers": vs, "Path": "vouchers",
		"DeletableID": deletable, "Notice": r.URL.Query().Get("notice")})
}

func (s *Server) trial(w http.ResponseWriter, r *http.Request) {
	p, ok := s.load(w, r, true)
	if !ok {
		return
	}
	rows, err := s.store.TrialBalance(r.Context(), p.Company.ID, p.Year.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	var tot struct{ Opening, Debit, Credit, Closing money.Ore }
	for _, x := range rows {
		tot.Opening += x.Opening
		tot.Debit += x.Debit
		tot.Credit += x.Credit
		tot.Closing += x.Closing
	}
	s.render(w, "trial.html", map[string]any{"P": p, "Rows": rows, "Total": tot, "Path": "trial"})
}

func (s *Server) exportSIE(w http.ResponseWriter, r *http.Request) {
	p, ok := s.load(w, r, false)
	if !ok {
		return
	}
	doc, err := s.store.Export(r.Context(), p.Company.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	var buf bytes.Buffer
	if err := sie.Write(&buf, doc, time.Now()); err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/x-sie; charset=ibm437")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="SIE4_%d.se"`, p.Company.ID))
	w.Write(buf.Bytes())
}

// statement serves a report built from the year's trial balance.
func (s *Server) statement(path string, build func(cur, prev []ledger.TrialRow) report.Statement) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := s.load(w, r, true)
		if !ok {
			return
		}
		rows, err := s.store.TrialBalance(r.Context(), p.Company.ID, p.Year.ID)
		if err != nil {
			s.fail(w, err)
			return
		}
		// The previous year is the next older one in the list (newest first).
		var prevRows []ledger.TrialRow
		var prev ledger.FiscalYear
		for i, y := range p.Years {
			if y.ID == p.Year.ID && i+1 < len(p.Years) {
				prev = p.Years[i+1]
				if prevRows, err = s.store.TrialBalance(r.Context(), p.Company.ID, prev.ID); err != nil {
					s.fail(w, err)
					return
				}
				if prevRows == nil {
					prevRows = []ledger.TrialRow{}
				}
			}
		}
		s.render(w, "statement.html", map[string]any{"P": p, "S": build(rows, prevRows), "Prev": prev, "Path": path})
	}
}

func (s *Server) generalLedger(w http.ResponseWriter, r *http.Request) {
	p, ok := s.load(w, r, true)
	if !ok {
		return
	}
	from, to := strings.TrimSpace(r.URL.Query().Get("from")), strings.TrimSpace(r.URL.Query().Get("to"))
	accts, err := s.store.GeneralLedger(r.Context(), p.Company.ID, p.Year.ID, from, to)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.render(w, "ledger.html", map[string]any{"P": p, "Accounts": accts, "From": from, "To": to, "Path": "ledger"})
}
