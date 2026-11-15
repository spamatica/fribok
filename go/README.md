# Fribok (Go rewrite, work in progress)

A small web UI for bookkeeping, built on SQLite. The Java application in the
parent directory is the reference implementation.

    go test ./...
    go run ./cmd/fribok            # http://127.0.0.1:8080, database in ./fribok.db

There is no authentication yet: keep it listening on localhost.

## Layout
- `internal/money`  amounts as integer öre (debit positive, credit negative)
- `internal/sie`    SIE 4 parser (CP437 or UTF-8)
- `internal/ledger` SQLite schema and queries, SIE import, trial balance
- `internal/web`    server-rendered pages (html/template)

## Tests
The ledger and web tests import any `*.se` file placed in `../test_files/`
(git-ignored, since it is real accounting data) and check the computed
closing balances against the file's own `#UB`/`#RES` records.

## Not done yet
SIE export, entering/editing vouchers, locked periods, income statement and
balance sheet reports, general ledger, previous-year handling beyond opening
balances, SIE dimensions/objects, authentication.
