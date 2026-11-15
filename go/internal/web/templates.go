package web

import (
	"fmt"
	"net/http"
	"strconv"

	"fribok/internal/sie"
)

func (s *Server) showTemplates(w http.ResponseWriter, r *http.Request, errMsg string, status int) {
	ts, err := s.store.Templates(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(status)
	s.render(w, "templates.html", map[string]any{"Templates": ts, "Error": errMsg,
		"Notice": r.URL.Query().Get("notice")})
}

func (s *Server) templates(w http.ResponseWriter, r *http.Request) {
	s.showTemplates(w, r, "", http.StatusOK)
}

// importTemplate creates a template from the chart of accounts in a SIE file.
func (s *Server) importTemplate(w http.ResponseWriter, r *http.Request) {
	f, _, err := r.FormFile("file")
	if err != nil {
		s.showTemplates(w, r, "Ingen fil mottogs", http.StatusUnprocessableEntity)
		return
	}
	defer f.Close()
	doc, err := sie.Parse(f)
	if err != nil {
		s.showTemplates(w, r, "Filen kunde inte läsas: "+err.Error(), http.StatusUnprocessableEntity)
		return
	}
	if _, err := s.store.ImportTemplate(r.Context(), r.FormValue("name"), doc); err != nil {
		msg, ok := validationMsg(err)
		if !ok {
			s.fail(w, err)
			return
		}
		s.showTemplates(w, r, msg, http.StatusUnprocessableEntity)
		return
	}
	http.Redirect(w, r, "/templates?notice=Mallen+importerades.", http.StatusSeeOther)
}

func (s *Server) deleteTemplate(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := s.store.DeleteTemplate(r.Context(), id); err != nil {
		msg, ok := validationMsg(err)
		if !ok {
			s.fail(w, err)
			return
		}
		s.showTemplates(w, r, msg, http.StatusUnprocessableEntity)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/templates?notice=%s", "Mallen+togs+bort."), http.StatusSeeOther)
}
