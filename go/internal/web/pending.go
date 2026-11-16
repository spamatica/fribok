package web

import (
	"crypto/rand"
	"encoding/hex"
	"time"

	"fribok/internal/sie"
)

// Imports whose company name clashes are parked here while the user picks
// another name. The token is a one-time handle for the parsed file, so the
// upload does not have to be repeated.
const pendingImportTTL = 30 * time.Minute

type pendingImport struct {
	doc     *sie.Document
	expires time.Time
}

func (s *Server) rememberImport(doc *sie.Document) (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	token := hex.EncodeToString(b[:])
	now := time.Now()
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	for k, v := range s.pendingImports {
		if now.After(v.expires) {
			delete(s.pendingImports, k)
		}
	}
	s.pendingImports[token] = pendingImport{doc: doc, expires: now.Add(pendingImportTTL)}
	return token, nil
}

func (s *Server) pendingImport(token string) (*sie.Document, bool) {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	p, ok := s.pendingImports[token]
	if !ok {
		return nil, false
	}
	if time.Now().After(p.expires) {
		delete(s.pendingImports, token)
		return nil, false
	}
	return p.doc, true
}

func (s *Server) forgetImport(token string) {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	delete(s.pendingImports, token)
}
