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
	"sync"
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

	pendingMu      sync.Mutex
	pendingImports map[string]pendingImport
}

func New(store *ledger.Store) *Server {
	funcs := template.FuncMap{
		"kr":  func(o money.Ore) string { return o.Format() },
		"neg": func(o money.Ore) money.Ore { return -o },
		// Replaced per request in render; this one only lets the templates parse.
		"active": func(string) bool { return false },
	}
	return &Server{
		store:          store,
		tmpl:           template.Must(template.New("").Funcs(funcs).ParseFS(templateFS, "templates/*.html")),
		pendingImports: make(map[string]pendingImport),
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.index)
	mux.HandleFunc("POST /import", s.importSIE)
	mux.HandleFunc("POST /import/confirm", s.importConfirm)
	mux.HandleFunc("GET /c/{id}/{$}", s.company)
	mux.HandleFunc("GET /c/{id}/delete", s.confirmDeleteCompany)
	mux.HandleFunc("POST /c/{id}/delete", s.deleteCompany)
	mux.HandleFunc("POST /c/{id}/import", s.mergeSIE)
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
	mux.HandleFunc("GET /c/{id}/income.pdf", s.statementPDF("income", report.IncomeStatement))
	mux.HandleFunc("GET /c/{id}/balance.pdf", s.statementPDF("balance", report.BalanceSheet))
	mux.HandleFunc("GET /c/{id}/trial.pdf", s.trialPDF)
	mux.HandleFunc("GET /c/{id}/ledger.pdf", s.ledgerPDF)
	mux.HandleFunc("GET /c/{id}/export.se", s.exportSIE)
	mux.HandleFunc("GET /c/{id}/vouchers/new", s.newVoucherForm)
	mux.HandleFunc("POST /c/{id}/vouchers", s.createVoucher)
	mux.HandleFunc("POST /c/{id}/vouchers/{vid}/correct", s.correctVoucher)
	mux.HandleFunc("POST /c/{id}/vouchers/{vid}/delete", s.deleteVoucher)
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

type importNamePage struct {
	Token, Name, Original, Error string
	Years, Accounts, Vouchers    int
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
		doc.CompanyName = strings.TrimSpace(doc.CompanyName)
		if doc.CompanyName != "" {
			taken, terr := s.store.CompanyNameTaken(r.Context(), doc.CompanyName)
			if terr != nil {
				s.fail(w, terr)
				return
			}
			if taken {
				// Let the user pick another name instead of throwing the
				// parsed file away and asking for it again.
				name, terr := s.store.UniqueCompanyName(r.Context(), doc.CompanyName)
				if terr != nil {
					s.fail(w, terr)
					return
				}
				token, terr := s.rememberImport(doc)
				if terr != nil {
					s.fail(w, terr)
					return
				}
				s.showImportName(w, r, importNamePage{
					Token: token, Name: name, Original: doc.CompanyName,
					Years: len(doc.Years), Accounts: len(doc.Accounts), Vouchers: len(doc.Vouchers),
				}, http.StatusOK)
				return
			}
		}
		var id int64
		if id, err = s.store.ImportSIE(r.Context(), doc); err == nil {
			http.Redirect(w, r, fmt.Sprintf("/c/%d/", id), http.StatusSeeOther)
			return
		}
	}
	http.Redirect(w, r, "/?error="+template.URLQueryEscaper("import failed: "+err.Error()), http.StatusSeeOther)
}

func (s *Server) showImportName(w http.ResponseWriter, r *http.Request, p importNamePage, status int) {
	w.WriteHeader(status)
	s.render(w, "import_name.html", p)
}

func (s *Server) importConfirm(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	token := r.PostForm.Get("token")
	name := strings.TrimSpace(r.PostForm.Get("name"))
	doc, ok := s.pendingImport(token)
	if !ok {
		http.Redirect(w, r, "/?error="+template.URLQueryEscaper("Importen har gått ut. Ladda upp filen igen."),
			http.StatusSeeOther)
		return
	}
	page := importNamePage{
		Token: token, Name: name, Original: doc.CompanyName,
		Years: len(doc.Years), Accounts: len(doc.Accounts), Vouchers: len(doc.Vouchers),
	}
	if name == "" {
		page.Error = "Företagsnamn saknas"
		s.showImportName(w, r, page, http.StatusUnprocessableEntity)
		return
	}
	copy := *doc
	copy.CompanyName = name
	id, err := s.store.ImportSIE(r.Context(), &copy)
	if err != nil {
		if msg, ok := validationMsg(err); ok {
			page.Error = msg
			s.showImportName(w, r, page, http.StatusUnprocessableEntity)
			return
		}
		s.fail(w, err)
		return
	}
	s.forgetImport(token)
	http.Redirect(w, r, fmt.Sprintf("/c/%d/", id), http.StatusSeeOther)
}

// confirmDeleteCompany asks before the destructive POST.
func (s *Server) confirmDeleteCompany(w http.ResponseWriter, r *http.Request) {
	p, ok := s.load(w, r, false)
	if !ok {
		return
	}
	s.render(w, "company_delete.html", p)
}

// deleteCompany removes a company and all of its bookkeeping.
func (s *Server) deleteCompany(w http.ResponseWriter, r *http.Request) {
	p, ok := s.load(w, r, false)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if r.PostForm.Get("confirm") != "yes" {
		http.Error(w, "Bekräfta borttagningen", http.StatusUnprocessableEntity)
		return
	}
	if err := s.store.DeleteCompany(r.Context(), p.Company.ID); err != nil {
		if msg, ok := validationMsg(err); ok {
			http.Error(w, msg, http.StatusUnprocessableEntity)
			return
		}
		s.fail(w, err)
		return
	}
	http.Redirect(w, r, "/?notice="+template.URLQueryEscaper("Företaget togs bort"), http.StatusSeeOther)
}

// mergeSIE adds the years of an uploaded SIE file to an existing company.
func (s *Server) mergeSIE(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	f, _, err := r.FormFile("file")
	if err != nil {
		http.Redirect(w, r, fmt.Sprintf("/c/%d/?error=%s", id,
			template.URLQueryEscaper("ingen fil togs emot")), http.StatusSeeOther)
		return
	}
	defer f.Close()
	doc, err := sie.Parse(f)
	if err == nil {
		var res ledger.MergeResult
		if res, err = s.store.MergeSIE(r.Context(), id, doc); err == nil {
			http.Redirect(w, r, fmt.Sprintf("/c/%d/?notice=%s", id,
				template.URLQueryEscaper(mergeNotice(res))), http.StatusSeeOther)
			return
		}
	}
	msg, ok := validationMsg(err)
	if !ok {
		s.fail(w, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/c/%d/?error=%s", id,
		template.URLQueryEscaper(msg)), http.StatusSeeOther)
}

func mergeNotice(res ledger.MergeResult) string {
	list := func(years []ledger.MergeYear) string {
		parts := make([]string, len(years))
		for i, y := range years {
			parts[i] = y.Start + " – " + y.End
		}
		return strings.Join(parts, ", ")
	}
	var parts []string
	if len(res.Added) > 0 {
		parts = append(parts, "Tillagda räkenskapsår: "+list(res.Added))
	}
	if len(res.Updated) > 0 {
		parts = append(parts, "Uppdaterade räkenskapsår: "+list(res.Updated))
	}
	if len(res.Skipped) > 0 {
		parts = append(parts, "Fanns redan: "+list(res.Skipped))
	}
	parts = append(parts, fmt.Sprintf("Importerade verifikationer: %d. Nya konton: %d.",
		res.Vouchers, res.Accounts))
	return strings.Join(parts, ". ") + "."
}

// companyCtx loads the company from the path and, when withYear is set, the
// selected fiscal year (?year=<idx>, default the newest).
type page struct {
	Company ledger.Company
	Years   []ledger.FiscalYear
	Year    ledger.FiscalYear
	Notice  string
	Error   string
}

func (s *Server) load(w http.ResponseWriter, r *http.Request, withYear bool) (page, bool) {
	var p page
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return p, false
	}
	ctx := r.Context()
	p.Notice = r.URL.Query().Get("notice")
	p.Error = r.URL.Query().Get("error")
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
	// The store returns vouchers in series/number order; the page shows the
	// latest one first.
	for i, j := 0, len(vs)-1; i < j; i, j = i+1, j-1 {
		vs[i], vs[j] = vs[j], vs[i]
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
	rows, tot, err := s.trialData(r.Context(), p)
	if err != nil {
		s.fail(w, err)
		return
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
		st, prev, err := s.statementData(r.Context(), p, build)
		if err != nil {
			s.fail(w, err)
			return
		}
		s.render(w, "statement.html", map[string]any{"P": p, "S": st, "Prev": prev, "Path": path})
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
