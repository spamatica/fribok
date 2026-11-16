// Package pdf writes small, dependency-free PDF documents with the standard
// Helvetica fonts. It is deliberately limited to what the report pages need:
// text, horizontal lines, filled rectangles and page breaks.
package pdf

import (
	"bytes"
	"fmt"
	"strings"

	"golang.org/x/text/encoding/charmap"
)

// Doc is a PDF document under construction. Coordinates are in points from the
// top-left corner of the page, because the report layout code is written top
// down; the writer flips them when emitting PDF operators.
type Doc struct {
	width, height float64
	margin        float64
	pages         []string
	footer        func(page, total int) string
}

// NewA4 creates an A4 document and adds its first page.
func NewA4(landscape bool) *Doc {
	d := &Doc{width: 595.28, height: 841.89, margin: 40}
	if landscape {
		d.width, d.height = d.height, d.width
	}
	d.AddPage()
	return d
}

func (d *Doc) Width() float64  { return d.width }
func (d *Doc) Height() float64 { return d.height }
func (d *Doc) Margin() float64 { return d.margin }

// AddPage starts a new page.
func (d *Doc) AddPage() { d.pages = append(d.pages, "") }

// PageCount returns the number of pages added so far.
func (d *Doc) PageCount() int { return len(d.pages) }

// SetFooter installs a footer callback. It runs at finalization so that the
// callback knows the total number of pages.
func (d *Doc) SetFooter(f func(page, total int) string) { d.footer = f }

func (d *Doc) appendContent(s string) {
	d.pages[len(d.pages)-1] += s + "\n"
}

// Text draws one line of text with its baseline at top (measured from the top
// of the page) and its left edge at x.
func (d *Doc) Text(x, top, size float64, bold bool, text string) {
	font := "F1"
	if bold {
		font = "F2"
	}
	d.appendContent(fmt.Sprintf("BT /%s %.2f Tf 1 0 0 1 %.2f %.2f Tm (%s) Tj ET",
		font, size, x, d.height-top, escape(text)))
}

// TextRight draws text right-aligned at right.
func (d *Doc) TextRight(right, top, size float64, bold bool, text string) {
	d.Text(right-d.StringWidth(size, bold, text), top, size, bold, text)
}

// TextCenter draws text centred on x.
func (d *Doc) TextCenter(x, top, size float64, bold bool, text string) {
	d.Text(x-d.StringWidth(size, bold, text)/2, top, size, bold, text)
}

// Line draws a horizontal line between two x coordinates at top.
func (d *Doc) Line(x1, top, x2 float64, width float64) {
	d.appendContent(fmt.Sprintf("%.2f w %.2f %.2f m %.2f %.2f l S",
		width, x1, d.height-top, x2, d.height-top))
}

// FillRect fills a rectangle with a gray level from 0 (black) to 1 (white).
func (d *Doc) FillRect(x, top, width, height, gray float64) {
	d.appendContent(fmt.Sprintf("%.3f g %.2f %.2f %.2f %.2f re f 0 g",
		gray, x, d.height-top-height, width, height))
}

// StringWidth returns the approximate width of text in points.
func (d *Doc) StringWidth(size float64, bold bool, text string) float64 {
	var units int
	for _, r := range text {
		units += runeWidth(r)
	}
	return float64(units) * size / 1000
}

// Fit returns text truncated with an ellipsis so that it fits in width.
func Fit(d *Doc, size float64, bold bool, width float64, text string) string {
	if d.StringWidth(size, bold, text) <= width {
		return text
	}
	const ellipsis = "…"
	ellipsisWidth := d.StringWidth(size, bold, ellipsis)
	if ellipsisWidth >= width {
		return ""
	}
	runes := []rune(text)
	for len(runes) > 0 {
		runes = runes[:len(runes)-1]
		candidate := string(runes) + ellipsis
		if d.StringWidth(size, bold, candidate) <= width {
			return candidate
		}
	}
	return ""
}

// Bytes finalizes the document and returns a complete PDF file.
func (d *Doc) Bytes() []byte {
	totalPages := len(d.pages)
	if d.footer != nil {
		for i := range d.pages {
			if s := d.footer(i+1, totalPages); s != "" {
				x := d.width / 2
				d.pages[i] += fmt.Sprintf("BT /F1 8 Tf 1 0 0 1 %.2f %.2f Tm (%s) Tj ET\n",
					x-d.StringWidth(8, false, s)/2, d.height-24, escape(s))
			}
		}
	}

	pageFirst := 3
	contentFirst := pageFirst + totalPages
	fontRegular := contentFirst + totalPages
	fontBold := fontRegular + 1
	objectCount := fontBold

	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	offsets := make([]int, objectCount+1)
	writeObject := func(id int, body string) {
		offsets[id] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", id, body)
	}

	writeObject(1, "<< /Type /Catalog /Pages 2 0 R >>")
	kids := make([]string, totalPages)
	for i := range kids {
		kids[i] = fmt.Sprintf("%d 0 R", pageFirst+i)
	}
	writeObject(2, fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>",
		strings.Join(kids, " "), totalPages))

	for i := 0; i < totalPages; i++ {
		pageID := pageFirst + i
		contentID := contentFirst + i
		writeObject(pageID, fmt.Sprintf(
			"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %.2f %.2f] /Resources << /Font << /F1 %d 0 R /F2 %d 0 R >> >> /Contents %d 0 R >>",
			d.width, d.height, fontRegular, fontBold, contentID))
		stream := d.pages[i]
		writeObject(contentID, fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream))
	}
	writeObject(fontRegular, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>")
	writeObject(fontBold, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica-Bold /Encoding /WinAnsiEncoding >>")

	xrefAt := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n", objectCount+1)
	buf.WriteString("0000000000 65535 f \n")
	for id := 1; id <= objectCount; id++ {
		fmt.Fprintf(&buf, "%010d 00000 n \n", offsets[id])
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n",
		objectCount+1, xrefAt)
	return buf.Bytes()
}

func escape(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	encoded, err := charmap.Windows1252.NewEncoder().String(s)
	if err != nil {
		var b strings.Builder
		for _, r := range s {
			switch {
			case r < 0x80:
				b.WriteByte(byte(r))
			case r <= 0xff:
				b.WriteByte(byte(r))
			default:
				b.WriteByte('?')
			}
		}
		encoded = b.String()
	}
	var b strings.Builder
	for i := 0; i < len(encoded); i++ {
		switch c := encoded[i]; c {
		case '\\', '(', ')':
			b.WriteByte('\\')
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// runeWidth returns a Helvetica character width in 1/1000 em. It covers ASCII
// exactly and approximates the accented letters used by the Swedish reports.
func runeWidth(r rune) int {
	switch r {
	case ' ':
		return 278
	case '!':
		return 278
	case '"':
		return 355
	case '#':
		return 556
	case '$':
		return 556
	case '%':
		return 889
	case '&':
		return 667
	case '\'':
		return 191
	case '(':
		return 333
	case ')':
		return 333
	case '*':
		return 389
	case '+':
		return 584
	case ',':
		return 278
	case '-':
		return 333
	case '.':
		return 278
	case '/':
		return 278
	case ':':
		return 278
	case ';':
		return 278
	case '<':
		return 584
	case '=':
		return 584
	case '>':
		return 584
	case '?':
		return 556
	case '@':
		return 1015
	case '[':
		return 278
	case '\\':
		return 278
	case ']':
		return 278
	case '^':
		return 469
	case '_':
		return 556
	case '`':
		return 333
	case '{':
		return 334
	case '|':
		return 260
	case '}':
		return 334
	case '~':
		return 584
	case '–', '—':
		return 556
	case '…':
		return 1000
	}
	if r >= '0' && r <= '9' {
		return 556
	}
	if r >= 'A' && r <= 'Z' {
		switch r {
		case 'A':
			return 667
		case 'B':
			return 667
		case 'C':
			return 722
		case 'D':
			return 722
		case 'E':
			return 667
		case 'F':
			return 611
		case 'G':
			return 778
		case 'H':
			return 722
		case 'I':
			return 278
		case 'J':
			return 500
		case 'K':
			return 667
		case 'L':
			return 556
		case 'M':
			return 833
		case 'N':
			return 722
		case 'O':
			return 778
		case 'P':
			return 667
		case 'Q':
			return 778
		case 'R':
			return 722
		case 'S':
			return 667
		case 'T':
			return 611
		case 'U':
			return 722
		case 'V':
			return 667
		case 'W':
			return 944
		case 'X':
			return 667
		case 'Y':
			return 667
		case 'Z':
			return 611
		}
	}
	if r >= 'a' && r <= 'z' {
		switch r {
		case 'a', 'b', 'd', 'e', 'g', 'h', 'n', 'o', 'p', 'q', 'u':
			return 556
		case 'c':
			return 500
		case 'f', 't':
			return 278
		case 'i', 'j', 'l':
			return 222
		case 'k':
			return 500
		case 'm':
			return 833
		case 'r':
			return 333
		case 's':
			return 500
		case 'v', 'x', 'y':
			return 500
		case 'w':
			return 722
		case 'z':
			return 500
		}
	}
	// Swedish and other Latin-1 letters are close enough to their base
	// letters. These values keep truncation and right alignment sane.
	switch r {
	case 'Å', 'Ä':
		return 667
	case 'Ö':
		return 778
	case 'å', 'ä':
		return 556
	case 'ö':
		return 556
	case 'é', 'è', 'ê', 'ë':
		return 556
	case 'É', 'È', 'Ê', 'Ë':
		return 667
	}
	return 556
}
