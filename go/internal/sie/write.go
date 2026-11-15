package sie

import (
	"bytes"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"golang.org/x/text/encoding/charmap"
)

// Write writes doc as a SIE 4 file in CP437 (#FORMAT PC8). Characters that
// CP437 cannot represent are written as '?'. gen is used for #GEN.
func Write(w io.Writer, doc *Document, gen time.Time) error {
	var b bytes.Buffer
	p := func(format string, a ...any) { fmt.Fprintf(&b, format+"\r\n", a...) }

	p("#FLAGGA 0")
	p("#FORMAT PC8")
	p("#SIETYP 4")
	p("#PROGRAM %s %s", q("Fribok"), q("0.1"))
	p("#GEN %s", gen.Format("20060102"))
	p("#FNAMN %s", q(doc.CompanyName))
	if doc.OrgNr != "" {
		p("#ORGNR %s", doc.OrgNr)
	}
	if doc.Currency != "" {
		p("#VALUTA %s", doc.Currency)
	}
	years := append([]Year(nil), doc.Years...)
	sort.Slice(years, func(i, j int) bool { return years[i].Index > years[j].Index })
	for _, y := range years {
		p("#RAR %d %s %s", y.Index, compact(y.Start), compact(y.End))
	}
	if doc.KPType != "" {
		p("#KPTYP %s", doc.KPType)
	}
	p("")
	for _, a := range doc.Accounts {
		p("#KONTO %s %s", a.Number, q(a.Name))
	}
	for _, a := range doc.Accounts {
		if a.Type != "" {
			p("#KTYP %s %s", a.Number, a.Type)
		}
	}
	p("")
	for _, set := range []struct {
		label string
		bals  []Balance
	}{{"#IB", doc.IB}, {"#UB", doc.UB}, {"#RES", doc.Res}} {
		bals := append([]Balance(nil), set.bals...)
		sort.SliceStable(bals, func(i, j int) bool {
			if bals[i].YearIndex != bals[j].YearIndex {
				return bals[i].YearIndex > bals[j].YearIndex
			}
			return bals[i].Account < bals[j].Account
		})
		for _, bal := range bals {
			p("%s %d %s %s", set.label, bal.YearIndex, bal.Account, bal.Amount)
		}
	}
	p("")
	for _, v := range doc.Vouchers {
		p("#VER %s %s %s %s", q(v.Series), q(v.Number), compact(v.Date), q(v.Text))
		p("{")
		for _, l := range v.Lines {
			if l.Text != "" {
				d := l.Date
				if d == "" {
					d = v.Date
				}
				p("#TRANS %s {} %s %s %s", l.Account, l.Amount, compact(d), q(l.Text))
			} else {
				p("#TRANS %s {} %s", l.Account, l.Amount)
			}
		}
		p("}")
	}

	out, err := toCP437(b.String())
	if err != nil {
		return err
	}
	_, err = w.Write(out)
	return err
}

func compact(isoDate string) string { return strings.ReplaceAll(isoDate, "-", "") }

// q quotes a string for SIE. Line breaks are not allowed inside a record.
func q(s string) string {
	s = strings.NewReplacer("\r", " ", "\n", " ").Replace(s)
	s = strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s)
	return `"` + s + `"`
}

func toCP437(s string) ([]byte, error) {
	out := make([]byte, 0, len(s))
	for _, r := range s {
		if r < 0x80 {
			out = append(out, byte(r))
			continue
		}
		c, ok := charmap.CodePage437.EncodeRune(r)
		if !ok {
			c = '?'
		}
		out = append(out, c)
	}
	return out, nil
}
