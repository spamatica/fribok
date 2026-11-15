# Fribok (Go rewrite, work in progress)

A small web UI for bookkeeping, built on SQLite. The Java application in the
parent directory is the reference implementation.

    go test ./...
    go run ./cmd/fribok            # http://127.0.0.1:8080, database in ./fribok.db

There is no authentication yet: keep it listening on localhost.

## Layout
- `internal/money`  amounts as integer öre (debit positive, credit negative)
- `internal/sie`    SIE 4 parser (CP437 or UTF-8)
- `internal/sie`    also writes SIE 4 (CP437)
- `internal/ledger` SQLite schema and migrations, SIE import/export, trial
  balance, posting, correcting and locking vouchers
- `internal/report` income statement and balance sheet (BAS number ranges)
- `internal/web`    server-rendered pages (html/template)

## Bookkeeping rules enforced
- Vouchers must balance, have at least two lines, use known accounts and a
  date inside the chosen fiscal year.
- Numbers are gap-free per fiscal year and series.
- A posted voucher is never edited or deleted. A mistake is fixed with a
  correction voucher that reverses it (linked to the original, voucher can be
  corrected once), followed by a new voucher.
- Closing a year (`/c/{id}/close`, with a preview that runs the same code and
  rolls back): books any result not yet booked as voucher BS1 (8999 against
  2099 or 2069), creates the next fiscal year if missing, writes its opening
  balances (the result account's balance moves to 2098 or 2091) and locks the
  closed year completely.
- A period can be locked up to a date; the lock can move forward, never back.
- Schema changes go in `migrations` in `internal/ledger/ledger.go`
  (`schema.sql` is version 0 and is not edited).

## Tests
The ledger and web tests import any `*.se` file placed in `../test_files/`
(git-ignored, since it is real accounting data) and check the computed
closing balances against the file's own `#UB`/`#RES` records.

## Not done yet
General ledger and journal reports, comparison with the previous year in the
reports, adding accounts
and companies without a SIE file, VAT report, SIE dimensions/objects,
authentication. Imported companies are always new: a second SIE file for the
same company is not merged.
