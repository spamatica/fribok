// Package money holds amounts as integer öre so that no floating point
// arithmetic is ever used for bookkeeping.
package money

import (
	"fmt"
	"strconv"
	"strings"
)

// Ore is an amount in öre (1/100 SEK). Debit is positive, credit negative.
type Ore int64

// Parse reads a decimal amount such as "200.00", "-4", "1234,5".
// More than two decimals is an error unless the extra digits are zeros.
func Parse(s string) (Ore, error) {
	orig := s
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty amount")
	}
	s = strings.Replace(s, ",", ".", 1)
	neg := false
	switch s[0] {
	case '-':
		neg, s = true, s[1:]
	case '+':
		s = s[1:]
	}
	whole, frac, _ := strings.Cut(s, ".")
	if whole == "" {
		whole = "0"
	}
	if !digits(whole) || !digits(frac) {
		return 0, fmt.Errorf("invalid amount %q", orig)
	}
	if len(frac) > 2 {
		if strings.Trim(frac[2:], "0") != "" {
			return 0, fmt.Errorf("amount %q has more than two decimals", orig)
		}
		frac = frac[:2]
	}
	for len(frac) < 2 {
		frac += "0"
	}
	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid amount %q: %w", orig, err)
	}
	f, _ := strconv.ParseInt(frac, 10, 64)
	v := w*100 + f
	if neg {
		v = -v
	}
	return Ore(v), nil
}

func digits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// String formats as plain decimal with a dot, e.g. "-4.00" (the SIE format).
func (o Ore) String() string {
	sign := ""
	v := int64(o)
	if v < 0 {
		sign, v = "-", -v
	}
	return fmt.Sprintf("%s%d.%02d", sign, v/100, v%100)
}

// Format formats for Swedish display, e.g. "1 234,56".
func (o Ore) Format() string {
	sign := ""
	v := int64(o)
	if v < 0 {
		sign, v = "-", -v
	}
	whole := strconv.FormatInt(v/100, 10)
	var b strings.Builder
	for i, r := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteString(" ")
		}
		b.WriteRune(r)
	}
	return fmt.Sprintf("%s%s,%02d", sign, b.String(), v%100)
}
