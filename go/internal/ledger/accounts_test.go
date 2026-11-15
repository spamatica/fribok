package ledger

import (
	"context"
	"testing"
)

func TestCreateCompanyWithTemplate(t *testing.T) {
	ctx := context.Background()
	s, src, _, _ := setup(t)
	c0, _ := s.Company(ctx, src)
	if c0.TemplateID == 0 {
		t.Fatal("the imported company has no template")
	}

	id, err := s.CreateCompany(ctx, " Ny förening ", "802000-0000", "2026-07-01", "2027-12-31", c0.TemplateID, false)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.Company(ctx, id)
	if err != nil || c.Name != "Ny förening" || c.Currency != "SEK" || c.TemplateID != c0.TemplateID {
		t.Fatalf("company = %+v, %v", c, err)
	}
	ys, _ := s.Years(ctx, id)
	if len(ys) != 1 || ys[0].Index != 0 || ys[0].Start != "2026-07-01" || ys[0].End != "2027-12-31" {
		t.Errorf("years = %+v", ys)
	}
	// Nothing is enabled yet, but the whole template is on offer.
	if as, _ := s.Accounts(ctx, id); len(as) != 0 {
		t.Errorf("%d accounts enabled in a new company", len(as))
	}
	chart, _ := s.Chart(ctx, id)
	if len(chart) != 3 { // 1930, 3999, 6570
		t.Errorf("chart has %d rows", len(chart))
	}
	_, err = s.AddVoucher(ctx, id, ys[0].ID, NewVoucher{Series: "V", Date: "2026-07-02", Text: "t", Lines: twoLines(100)})
	wantInvalid(t, err, "finns inte i kontoplanen")
	if err := s.EnableAccounts(ctx, id, []string{"1930", "6570"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddVoucher(ctx, id, ys[0].ID, NewVoucher{Series: "V", Date: "2026-07-02", Text: "t", Lines: twoLines(100)}); err != nil {
		t.Error(err)
	}

	// Enable everything from the start.
	all, err := s.CreateCompany(ctx, "Alla", "", "2026-01-01", "2026-12-31", c0.TemplateID, true)
	if err != nil {
		t.Fatal(err)
	}
	if as, _ := s.Accounts(ctx, all); len(as) != 3 {
		t.Errorf("enableAll gave %d accounts", len(as))
	}
	// Without a template only custom accounts are possible.
	none, err := s.CreateCompany(ctx, "Utan mall", "", "2026-01-01", "2026-12-31", 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddAccount(ctx, none, "1930", "Bank", ""); err != nil {
		t.Error(err)
	}
	_, err = s.CreateCompany(ctx, "X", "", "2026-01-01", "2026-12-31", 999, false)
	wantInvalid(t, err, "Kontoplansmallen finns inte")
}

func TestCreateCompanyValidation(t *testing.T) {
	ctx := context.Background()
	s := mustOpen(t)
	for name, c := range map[string]struct {
		name, start, end string
		template         int64
		want             string
	}{
		"no name":     {"", "2026-01-01", "2026-12-31", 0, "namn saknas"},
		"bad start":   {"X", "2026-1-1", "2026-12-31", 0, "startdatum"},
		"bad end":     {"X", "2026-01-01", "x", 0, "slutdatum"},
		"reversed":    {"X", "2026-12-31", "2026-01-01", 0, "före"},
		"too long":    {"X", "2026-01-01", "2027-07-01", 0, "18 månader"},
		"no template": {"X", "2026-01-01", "2026-12-31", 99, "finns inte"},
	} {
		_, err := s.CreateCompany(ctx, c.name, "", c.start, c.end, c.template, false)
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		wantInvalid(t, err, c.want)
	}
	if cs, _ := s.Companies(ctx); len(cs) != 0 {
		t.Errorf("failed creations left %d companies", len(cs))
	}
	// Exactly 18 months is fine.
	if _, err := s.CreateCompany(ctx, "X", "", "2026-01-01", "2027-06-30", 0, false); err != nil {
		t.Errorf("18 months: %v", err)
	}
}

func TestAccountsAddUpdateDelete(t *testing.T) {
	ctx := context.Background()
	s, cid, cur, _ := setup(t)
	if err := s.SetEnabled(ctx, cid, []string{"6570"}, nil); err != nil { // in the template, not enabled
		t.Fatal(err)
	}

	if err := s.AddAccount(ctx, cid, "1940", "Bankgiro", ""); err != nil {
		t.Fatal(err)
	}
	a, _ := s.Account(ctx, cid, "1940")
	if a.Name != "Bankgiro" || a.Type != "T" {
		t.Errorf("account = %+v", a)
	}
	for _, c := range []struct{ num, name, typ, want string }{
		{"1940", "Dubblett", "", "finns redan"},
		{"6570", "Finns i mallen", "", "finns i kontoplansmallen"},
		{"19", "Kort", "", "4–6 siffror"},
		{"0123", "Noll", "", "4–6 siffror"},
		{"19a0", "Bokstav", "", "4–6 siffror"},
		{"1950", " ", "", "Kontonamn"},
		{"1950", "X", "Q", "Kontotyp"},
	} {
		wantInvalid(t, s.AddAccount(ctx, cid, c.num, c.name, c.typ), c.want)
	}

	// Defaults by class and an explicit type.
	for num, typ := range map[string]string{"2099": "S", "3010": "I", "5010": "K", "8999": "K"} {
		if err := s.AddAccount(ctx, cid, num, "k"+num, ""); err != nil {
			t.Fatal(err)
		}
		if a, _ := s.Account(ctx, cid, num); a.Type != typ {
			t.Errorf("%s type %s, want %s", num, a.Type, typ)
		}
	}

	// Rename always works; changing the type only while unused.
	if err := s.UpdateAccount(ctx, cid, "1940", "Bankgiro 123", "T"); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateAccount(ctx, cid, "1940", "Bankgiro 123", "S"); err != nil {
		t.Errorf("type change of an unused account: %v", err)
	}
	wantInvalid(t, s.UpdateAccount(ctx, cid, "1930", "Bank", "S"), "kan inte ändras") // 1930 has postings
	if err := s.UpdateAccount(ctx, cid, "1930", "Företagskonto", "T"); err != nil {
		t.Errorf("rename of a used account: %v", err)
	}
	wantInvalid(t, s.UpdateAccount(ctx, cid, "7777", "x", ""), "finns inte")

	// Delete only unused accounts.
	wantInvalid(t, s.DeleteAccount(ctx, cid, "1930"), "kan inte tas bort")
	if err := s.DeleteAccount(ctx, cid, "1940"); err != nil {
		t.Fatal(err)
	}
	wantInvalid(t, s.DeleteAccount(ctx, cid, "1940"), "finns inte")
	_ = cur
}
