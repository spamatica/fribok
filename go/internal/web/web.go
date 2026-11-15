// Package web is the server-rendered HTML interface.
package web

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strconv"

	"fribok/internal/ledger"
	"fribok/internal/money"
	"fribok/internal/sie"
)

//go:embed templates/*.html
var templateFS embed.FS

const maxUpload = 32 << 20

type Server struct {
	store *ledger.Store
	tmpl  *template.Template
}

func New(store *ledger.Store) *Server {
	funcs := template.FuncMap{
		"kr":  func(o money.Ore) string { return o.Format() },
		"neg": func(o money.Ore) money.Ore { return -o },
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
	mux.HandleFunc("GET /c/{id}/accounts", s.accounts)
	mux.HandleFunc("GET /c/{id}/vouchers", s.vouchers)
	mux.HandleFunc("GET /c/{id}/trial", s.trial)
	return mux
}

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, name, data); err != nil {
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
	cs, err := s.store.Companies(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	s.render(w, "index.html", map[string]any{"Companies": cs, "Error": r.URL.Query().Get("error")})
}

func (s *Server) importSIE(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUpload)
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

func (s *Server) accounts(w http.ResponseWriter, r *http.Request) {
	p, ok := s.load(w, r, false)
	if !ok {
		return
	}
	as, err := s.store.Accounts(r.Context(), p.Company.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.render(w, "accounts.html", map[string]any{"P": p, "Accounts": as})
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
	s.render(w, "vouchers.html", map[string]any{"P": p, "Vouchers": vs, "Path": "vouchers"})
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
