// Package sie parses SIE files (the Swedish standard accounting exchange
// format). Only the records needed to import a company's books are kept:
// company data, fiscal years, accounts, balances (#IB/#UB/#RES) and
// vouchers. Dimensions and objects are parsed but ignored for now.
package sie

import (
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"

	"fribok/internal/money"
)

// Document is the parsed content of a SIE file.
type Document struct {
	Program     string
	SieType     int
	CompanyName string
	OrgNr       string
	Currency    string
	KPType      string // chart of accounts type, e.g. EUBAS97
	Years       []Year
	Accounts    []Account
	IB          []Balance // opening balances
	UB          []Balance // closing balances
	Res         []Balance // results (for P&L accounts)
	Vouchers    []Voucher
}

// Year is a fiscal year. Index 0 is the current year, -1 the previous, etc.
type Year struct {
	Index      int
	Start, End string // ISO dates, YYYY-MM-DD
}

type Account struct {
	Number string
	Name   string
	Type   string // T, S, K, I from #KTYP, or empty
}

type Balance struct {
	YearIndex int
	Account   string
	Amount    money.Ore
}

type Voucher struct {
	Series string
	Number string
	Date   string // ISO
	Text   string
	Lines  []Line
}

type Line struct {
	Account string
	Amount  money.Ore
	Date    string // optional ISO transaction date
	Text    string
}

// Parse reads a SIE file. The encoding is CP437 as required by the standard,
// except that input which is valid UTF-8 containing non-ASCII is taken as UTF-8
// (some programs write that regardless of the standard).
func Parse(r io.Reader) (*Document, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	text, err := decode(raw)
	if err != nil {
		return nil, err
	}
	doc := &Document{Currency: "SEK"}
	var cur *Voucher
	inBlock := false

	for n, line := range strings.Split(text, "\n") {
		lineNo := n + 1
		line = strings.TrimSpace(strings.TrimPrefix(line, "\xef\xbb\xbf"))
		if line == "" {
			continue
		}
		fail := func(format string, a ...any) error {
			return fmt.Errorf("line %d: %s", lineNo, fmt.Sprintf(format, a...))
		}
		switch line {
		case "{":
			if cur == nil || inBlock {
				return nil, fail("unexpected {")
			}
			inBlock = true
			continue
		case "}":
			if !inBlock {
				return nil, fail("unexpected }")
			}
			doc.Vouchers = append(doc.Vouchers, *cur)
			cur, inBlock = nil, false
			continue
		}
		toks, err := tokenize(line)
		if err != nil {
			return nil, fail("%v", err)
		}
		label := strings.ToUpper(toks[0])
		if !strings.HasPrefix(label, "#") {
			return nil, fail("expected a #record, got %q", toks[0])
		}
		args := toks[1:]
		need := func(k int) error {
			if len(args) < k {
				return fail("%s needs at least %d fields, got %d", label, k, len(args))
			}
			return nil
		}

		if cur != nil && !inBlock {
			return nil, fail("#VER must be followed by {")
		}
		if inBlock {
			switch label {
			case "#TRANS":
				// #TRANS konto {objekt} belopp [transdat] [transtext] [kvantitet] [sign]
				if err := need(3); err != nil {
					return nil, err
				}
				amt, err := money.Parse(args[2])
				if err != nil {
					return nil, fail("%v", err)
				}
				l := Line{Account: args[0], Amount: amt}
				if len(args) > 3 && args[3] != "" {
					if l.Date, err = isoDate(args[3]); err != nil {
						return nil, fail("%v", err)
					}
				}
				if len(args) > 4 {
					l.Text = args[4]
				}
				cur.Lines = append(cur.Lines, l)
			case "#RTRANS", "#BTRANS":
				// Informational records of changed transactions; the #TRANS
				// lines already hold the final state.
			default:
				return nil, fail("unexpected %s inside voucher", label)
			}
			continue
		}

		switch label {
		case "#PROGRAM":
			doc.Program = strings.Join(args, " ")
		case "#SIETYP":
			if err := need(1); err != nil {
				return nil, err
			}
			if doc.SieType, err = strconv.Atoi(args[0]); err != nil {
				return nil, fail("bad #SIETYP %q", args[0])
			}
		case "#FNAMN":
			if err := need(1); err != nil {
				return nil, err
			}
			doc.CompanyName = args[0]
		case "#ORGNR":
			if err := need(1); err != nil {
				return nil, err
			}
			doc.OrgNr = args[0]
		case "#VALUTA":
			if err := need(1); err != nil {
				return nil, err
			}
			doc.Currency = args[0]
		case "#KPTYP":
			if err := need(1); err != nil {
				return nil, err
			}
			doc.KPType = args[0]
		case "#RAR":
			if err := need(3); err != nil {
				return nil, err
			}
			idx, err := strconv.Atoi(args[0])
			if err != nil {
				return nil, fail("bad #RAR index %q", args[0])
			}
			y := Year{Index: idx}
			if y.Start, err = isoDate(args[1]); err != nil {
				return nil, fail("%v", err)
			}
			if y.End, err = isoDate(args[2]); err != nil {
				return nil, fail("%v", err)
			}
			doc.Years = append(doc.Years, y)
		case "#KONTO":
			if err := need(2); err != nil {
				return nil, err
			}
			doc.Accounts = append(doc.Accounts, Account{Number: args[0], Name: args[1]})
		case "#KTYP":
			if err := need(2); err != nil {
				return nil, err
			}
			for i := range doc.Accounts {
				if doc.Accounts[i].Number == args[0] {
					doc.Accounts[i].Type = strings.ToUpper(args[1])
				}
			}
		case "#IB", "#UB", "#RES":
			if err := need(3); err != nil {
				return nil, err
			}
			idx, err := strconv.Atoi(args[0])
			if err != nil {
				return nil, fail("bad year index %q", args[0])
			}
			amt, err := money.Parse(args[2])
			if err != nil {
				return nil, fail("%v", err)
			}
			b := Balance{YearIndex: idx, Account: args[1], Amount: amt}
			switch label {
			case "#IB":
				doc.IB = append(doc.IB, b)
			case "#UB":
				doc.UB = append(doc.UB, b)
			default:
				doc.Res = append(doc.Res, b)
			}
		case "#VER":
			// #VER serie vernr verdatum vertext [regdatum] [sign]
			if err := need(3); err != nil {
				return nil, err
			}
			d, err := isoDate(args[2])
			if err != nil {
				return nil, fail("%v", err)
			}
			cur = &Voucher{Series: args[0], Number: args[1], Date: d}
			if len(args) > 3 {
				cur.Text = args[3]
			}
		default:
			// #FLAGGA, #FORMAT, #GEN, #ADRESS, #DIM, #OBJEKT, #SRU, #KSUMMA ...
		}
	}
	if cur != nil || inBlock {
		return nil, fmt.Errorf("unterminated voucher at end of file")
	}
	return doc, nil
}

func decode(raw []byte) (string, error) {
	if utf8.Valid(raw) && hasHighBytes(raw) {
		return string(raw), nil
	}
	out, err := charmap.CodePage437.NewDecoder().Bytes(raw)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func hasHighBytes(b []byte) bool {
	return bytes.IndexFunc(b, func(r rune) bool { return r > 127 }) >= 0
}

func isoDate(s string) (string, error) {
	t, err := time.Parse("20060102", s)
	if err != nil {
		return "", fmt.Errorf("bad date %q", s)
	}
	return t.Format("2006-01-02"), nil
}

// tokenize splits a record line into fields. Quoted strings may contain
// spaces and \" escapes. An object list {...} is returned as one token with
// its raw content, which is currently ignored.
func tokenize(line string) ([]string, error) {
	var toks []string
	i := 0
	for i < len(line) {
		c := line[i]
		switch {
		case c == ' ' || c == '\t':
			i++
		case c == '"':
			var b strings.Builder
			i++
			closed := false
			for i < len(line) {
				if line[i] == '\\' && i+1 < len(line) && (line[i+1] == '"' || line[i+1] == '\\') {
					b.WriteByte(line[i+1])
					i += 2
					continue
				}
				if line[i] == '"' {
					closed = true
					i++
					break
				}
				b.WriteByte(line[i])
				i++
			}
			if !closed {
				return nil, fmt.Errorf("unterminated quoted string")
			}
			toks = append(toks, b.String())
		case c == '{':
			// Find the matching close brace, respecting quotes.
			start := i + 1
			inQ := false
			i++
			for i < len(line) && (inQ || line[i] != '}') {
				if line[i] == '"' {
					inQ = !inQ
				}
				i++
			}
			if i >= len(line) {
				return nil, fmt.Errorf("unterminated {")
			}
			toks = append(toks, line[start:i])
			i++
		default:
			start := i
			for i < len(line) && line[i] != ' ' && line[i] != '\t' {
				i++
			}
			toks = append(toks, line[start:i])
		}
	}
	if len(toks) == 0 {
		return nil, fmt.Errorf("empty record")
	}
	return toks, nil
}
