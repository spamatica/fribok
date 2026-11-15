package sie

import (
	"strings"
	"testing"
)

const sample = `#FLAGGA 0
#FORMAT PC8
#SIETYP 4
#PROGRAM "Test" 1.0
#FNAMN "Förening \"X\""
#ORGNR 802000-1234
#RAR 0 20250101 20251231
#KONTO 1930 "Bank"
#KONTO 3999 "Övriga intäkter"
#KTYP 1930 T
#IB 0 1930 100.50
#UB 0 1930 300.50
#RES 0 3999 -200.00
#VER "V" "1" 20250102 "medlemsavgift"
{
#TRANS 1930 {} 200.00
#TRANS 3999 {"1" "x"} -200.00 20250103 "rad"
}
`

func TestParseSample(t *testing.T) {
	d, err := Parse(strings.NewReader(sample))
	if err != nil {
		t.Fatal(err)
	}
	if d.CompanyName != `Förening "X"` || d.OrgNr != "802000-1234" || d.SieType != 4 {
		t.Errorf("header: %+v", d)
	}
	if len(d.Years) != 1 || d.Years[0].Start != "2025-01-01" {
		t.Errorf("years: %+v", d.Years)
	}
	if len(d.Accounts) != 2 || d.Accounts[0].Type != "T" {
		t.Errorf("accounts: %+v", d.Accounts)
	}
	if len(d.IB) != 1 || d.IB[0].Amount != 10050 || d.UB[0].Amount != 30050 || d.Res[0].Amount != -20000 {
		t.Errorf("balances: %+v %+v %+v", d.IB, d.UB, d.Res)
	}
	if len(d.Vouchers) != 1 || len(d.Vouchers[0].Lines) != 2 {
		t.Fatalf("vouchers: %+v", d.Vouchers)
	}
	l := d.Vouchers[0].Lines[1]
	if l.Date != "2025-01-03" || l.Text != "rad" || l.Amount != -20000 {
		t.Errorf("line: %+v", l)
	}
}

func TestParseCP437(t *testing.T) {
	// "Föreningen" in CP437: ö = 0x94
	in := "#FNAMN \"F\x94reningen\"\n"
	d, err := Parse(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if d.CompanyName != "Föreningen" {
		t.Errorf("got %q", d.CompanyName)
	}
}

func TestParseErrors(t *testing.T) {
	for name, in := range map[string]string{
		"unterminated": "#VER \"V\" \"1\" 20250102 \"x\"\n{\n#TRANS 1930 {} 1.00\n",
		"no brace":     "#VER \"V\" \"1\" 20250102 \"x\"\n#TRANS 1930 {} 1.00\n",
		"bad date":     "#RAR 0 2025 20251231\n",
		"bad amount":   "#VER \"V\" \"1\" 20250102 \"x\"\n{\n#TRANS 1930 {} abc\n}\n",
		"open quote":   "#FNAMN \"abc\n",
		"stray brace":  "}\n",
		"not a record": "hello\n",
	} {
		if _, err := Parse(strings.NewReader(in)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}
