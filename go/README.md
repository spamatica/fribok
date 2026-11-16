# Fribok (Go rewrite, work in progress)

A small web UI for bookkeeping, built on SQLite. The Java application in the
parent directory is the reference implementation.

    go test ./...
    go run ./cmd/fribok            # http://127.0.0.1:8080, database in ./fribok.db
    ./start.sh                     # start and open the browser (desktop use)

There is no authentication yet: keep it listening on localhost.

## Starting
`./start.sh` builds `fribok` if the binary is missing, starts it on
`127.0.0.1:8080`, waits until it responds and opens the default browser. Logs
go to `fribok.log`; Ctrl+C stops the server. Environment variables:

- `FRIBOK_PORT` (default 8080) and `FRIBOK_HOST` (default 127.0.0.1)
- `FRIBOK_DB` (default `fribok.db` next to the script) and `FRIBOK_LOG`
  (default `fribok.log` next to the script)
- `FRIBOK_NO_BROWSER=1` to skip opening a browser
- `FRIBOK_BROWSER` to choose the browser command; it receives the URL

The script works from any directory and only ever starts one server per port.
When companies already exist, the start page shows them as prominent cards;
the create/import forms are collapsed under "Lägg till företag".

## Layout
- `internal/money`  amounts as integer öre (debit positive, credit negative)
- `internal/sie`    SIE 4 parser (CP437 or UTF-8)
- `internal/sie`    also writes SIE 4 (CP437)
- `internal/ledger` SQLite schema and migrations, SIE import/export and merge,
  chart templates, trial balance, posting, correcting and locking vouchers
- `internal/report` income statement and balance sheet (BAS number ranges)
- `internal/pdf`    small A4 PDF writer (Helvetica text, tables and page breaks)
- `internal/web`    server-rendered pages (html/template), `static/` holds the
  stylesheet (light, dark and print) and the form scripts

## Importing SIE files
`/` imports a SIE file as a new company. Later years are added to an existing
company with "Lägg till räkenskapsår" on its page (`POST /c/{id}/import`):

- Fiscal years the company does not have yet are added.
- A year that already has vouchers is left alone; the file's `#UB`/`#RES` for
  that year (a newer export normally repeats the previous year's closing
  balances) are ignored.
- A year that only has imported closing balances is replaced by the file's
  actual vouchers, so files can be imported in any order.
- Accounts the file uses are enabled (existing names are kept).
- Fiscal-year indexes are renumbered by date: the newest year is 0, the one
  before it -1, and so on.

An import that would duplicate vouchers in a year, or whose year boundaries
overlap an existing year, is refused as a whole. The first file must therefore
be imported from `/` (which creates the company); the rest can be added from the
company page.

Imported years are not locked automatically. To make a historical year
read-only without recalculating the next year's opening balances, use "Lås
period" instead of "Avsluta året". Closing is for the year you are actually
finishing; its preview explains that it writes the next year's opening balances
and cannot be undone.

Company names are unique, ignoring case and extra whitespace. Creating a
company with an existing name reopens the form with an error. Importing a SIE
file whose `#FNAMN` is already used opens a rename step with a suggestion such
as "Name (2)", so the parsed file does not have to be uploaded again.

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

## Keyboard shortcuts
The menu uses single-letter shortcuts: Kontoplan K, Verifikationer V, Huvudbok
H, Saldobalans S, Resultaträkning R and Balansräkning B. On the start page F
opens Företag and K opens Kontoplansmallar, and on Verifikationer N opens "Ny
verifikation". Alt+Shift+letter also works as a fallback (plain Alt+letter is
reserved by browser menus). The shortcuts are shown as small hints next to the
links and are ignored while typing in a field or editor, or while focus is
inside a form, so they cannot interrupt a half-entered voucher.

## Voucher form keyboard
Focus starts in the date field. Enter moves to the next field (Ctrl+Enter
books). In an account field, Enter also fills in the amount that balances the
voucher in the opposite column (after a debit of 200 the next row gets a
credit of 200). Enter in a debit field skips the credit column. Enter or Tab
in the last field of the last row adds a row; the form starts with four.
Account fields suggest accounts, best match first (number, then name start,
then a word start, then anywhere), with the first suggestion preselected for
Tab or Enter. The scripts are in `internal/web/static/`; their logic is
tested through a JS engine in `picker_test.go`.

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
- Deleting a voucher is a narrow exception, for undoing a slip: only the most
  recently posted voucher of the company, never in a locked period. Its number
  is reused by the next voucher so numbering stays gap-free.
- A period can be locked up to a date; the lock can move forward, never back.
- Schema changes go in `migrations` in `internal/ledger/ledger.go`
  (`schema.sql` is version 0 and is not edited).

## Tests
The ledger and web tests import any `*.se` file placed in `../test_files/`
(git-ignored, since it is real accounting data) and check the computed
closing balances against the file's own `#UB`/`#RES` records.

## Pages
Companies (create new from a template, or import SIE; add later years from the
company page), chart templates, chart of accounts (enable/disable from the
template, custom accounts, rename), vouchers (enter, correct), general ledger,
trial balance, income statement and balance sheet (both with the previous year
alongside), year-end closing, period locks, SIE export and removing a company
(after a confirmation page; it deletes its fiscal years, accounts and
vouchers).

The four report pages have a "Ladda ner PDF" link (`income.pdf`, `balance.pdf`,
`trial.pdf` and `ledger.pdf`). The PDFs are A4 with Helvetica and WinAnsi text,
and a ledger download keeps the account range filter.

Years that are known only from imported closing balances (`#UB`/`#RES` of a
year without vouchers in the file) are shown in the reports and re-exported,
but cannot be posted to or closed.

## Not done yet
Journal report, VAT report, a BAS template to start from (import one from a
SIE file for now), copying the enabled set from another company, SIE
dimensions/objects, authentication.
