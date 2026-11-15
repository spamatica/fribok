// Command fribok serves the bookkeeping web UI.
package main

import (
	"flag"
	"log"
	"net/http"
	"time"

	"fribok/internal/ledger"
	"fribok/internal/web"
)

func main() {
	dbPath := flag.String("db", "fribok.db", "SQLite database file")
	addr := flag.String("addr", "127.0.0.1:8080", "listen address (there is no authentication yet: keep it on localhost)")
	flag.Parse()

	store, err := ledger.Open(*dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()

	srv := &http.Server{
		Addr:              *addr,
		Handler:           web.New(store).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("listening on http://%s (db %s)", *addr, *dbPath)
	log.Fatal(srv.ListenAndServe())
}
