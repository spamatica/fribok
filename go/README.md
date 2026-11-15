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
- `internal/ledger` SQLite schema and migrations, SIE import/export, chart
  templates, trial balance, posting, correcting and locking vouchers
- `internal/report` income statement and balance sheet (BAS number ranges)
- `internal/web`    server-rendered pages (html/template)

## Chart of accounts
A chart *template* (kontoplansmall, e.g. BAS or EUBAS97) is a shared reference
list of accounts. A company enables the accounts it actually uses; only
enabled accounts can be posted to and appear in reports and SIE export. An
enabled account that is not in the template is a *custom* account.

- A SIE import turns the file's `#KONTO` list into a template (named after
  `#KPTYP`, reused if it exists) and enables only the accounts the file uses
  (postings or `#IB`/`#UB`/`#RES`).
- Templates can also be imported on their own from a SIE file (`/templates`).
- On the chart page, tick accounts to enable them; "only enabled" and search
  filters narrow the list, and "Aktivera alla som visas" enables the filtered
  set. An account with postings or balances cannot be disabled.
- Closing a year enables the result accounts and 8999 from the template when
  it needs them, preferring the accounts the company already uses.
- Databases from before templates are migrated: each company gets a template
  made from its existing accounts, all enabled.

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

## Pages
Companies (create new from a template, or import SIE), chart templates,
chart of accounts (enable/disable from the template, custom accounts, rename),
vouchers (enter,
correct), general ledger, trial balance, income statement and balance sheet
(both with the previous year alongside), year-end closing, period locks and
SIE export.

Years that are known only from imported closing balances (`#UB`/`#RES` of a
year without vouchers in the file) are shown in the reports and re-exported,
but cannot be posted to or closed.

## Not done yet
Journal report, VAT report, a BAS template to start from (import one from a
SIE file for now), copying the enabled set from another company, merging a second SIE file into an existing
company, SIE dimensions/objects, authentication, PDF output (use the
browser's print function for now).
