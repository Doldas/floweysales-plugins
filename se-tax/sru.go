package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// The files follow Skatteverket's SRU specification ("Teknisk information om
// filöverföring", field names in K4_SKV2104 for 2025P4 and 2026P4, which are
// identical): INFO.SRU describes the sender, BLANKETTER.SRU holds one
// #BLANKETT block per K4 page. Both are ISO-8859-1 with CR+LF line ends.

// Filer is who the file is about and when it was made.
type Filer struct {
	PersonalNumber string // 12 digits
	Name           string
	PostalCode     string // 5 digits
	City           string
	Email          string
	Phone          string
}

// ValidatePersonalNumber normalises a personnummer or samordningsnummer to 12
// digits and checks its Luhn check digit.
func ValidatePersonalNumber(raw string, now time.Time) (string, error) {
	s := strings.TrimSpace(raw)
	plus := strings.Contains(s, "+")
	s = strings.NewReplacer("-", "", "+", "", " ", "").Replace(s)
	for _, r := range s {
		if r < '0' || r > '9' {
			return "", fmt.Errorf("the personal number may only contain digits and one dash")
		}
	}
	switch len(s) {
	case 10:
		yy, _ := strconv.Atoi(s[:2])
		century := now.Year() / 100 * 100
		year := century + yy
		if year > now.Year() {
			year -= 100
		}
		if plus {
			year -= 100
		}
		s = fmt.Sprintf("%04d", year) + s[2:]
	case 12:
	default:
		return "", fmt.Errorf("the personal number needs 12 digits (ÅÅÅÅMMDDNNNN)")
	}
	if !luhn(s[2:]) {
		return "", fmt.Errorf("the personal number's check digit is wrong; check for a typo")
	}
	month, _ := strconv.Atoi(s[4:6])
	day, _ := strconv.Atoi(s[6:8])
	if month < 1 || month > 12 || day < 1 || day > 91 { // samordningsnummer add 60 to the day
		return "", fmt.Errorf("the personal number does not contain a valid date")
	}
	return s, nil
}

func luhn(digits string) bool {
	sum := 0
	for i, r := range digits {
		d := int(r - '0')
		if i%2 == 0 {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
	}
	return sum%10 == 0
}

// MaskPersonalNumber hides all but the birth year and last digit.
func MaskPersonalNumber(pn string) string {
	if len(pn) != 12 {
		return "—"
	}
	return pn[:4] + "••••••" + pn[10:]
}

// latin1 converts to ISO-8859-1 bytes; characters outside it become '?'.
// Values are cleaned with clip first, because string fields must not contain
// '#' (the record marker) or line breaks.
func latin1(s string) []byte {
	out := make([]byte, 0, len(s))
	for _, r := range s {
		switch {
		case r == '\r' || r == '\n' || r == '\t':
			out = append(out, ' ')
		case r < 256:
			out = append(out, byte(r))
		case r == '–' || r == '—' || r == '−':
			out = append(out, '-')
		case r == '’' || r == '‘':
			out = append(out, '\'')
		default:
			out = append(out, '?')
		}
	}
	return out
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(strings.ReplaceAll(s, "#", " ")), " ")
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}

type sruBuilder struct{ b []byte }

func (w *sruBuilder) line(format string, args ...any) {
	w.b = append(w.b, latin1(fmt.Sprintf(format, args...))...)
	w.b = append(w.b, '\r', '\n')
}

// InfoSRU is the INFO.SRU file.
func InfoSRU(f Filer, now time.Time) []byte {
	w := &sruBuilder{}
	w.line("#DATABESKRIVNING_START")
	w.line("#PRODUKT SRU")
	w.line("#SKAPAD %s", now.Format("20060102 150405"))
	w.line("#PROGRAM FloweySales se-tax")
	w.line("#FILNAMN BLANKETTER.SRU")
	w.line("#DATABESKRIVNING_SLUT")
	w.line("#MEDIELEV_START")
	w.line("#ORGNR %s", f.PersonalNumber)
	w.line("#NAMN %s", clip(f.Name, 250))
	w.line("#POSTNR %s", f.PostalCode)
	w.line("#POSTORT %s", clip(f.City, 250))
	if f.Email != "" {
		w.line("#EMAIL %s", clip(f.Email, 250))
	}
	if f.Phone != "" {
		w.line("#TELEFON %s", clip(f.Phone, 15))
	}
	w.line("#MEDIELEV_SLUT")
	return w.b
}

// BlanketterSRU is the BLANKETTER.SRU file with one K4 block per page.
func BlanketterSRU(f Filer, year int, pages []Page, now time.Time) []byte {
	w := &sruBuilder{}
	for _, p := range pages {
		w.line("#BLANKETT K4-%dP4", year)
		w.line("#IDENTITET %s %s", f.PersonalNumber, now.Format("20060102 150405"))
		w.line("#NAMN %s", clip(f.Name, 250))
		w.line("#SYSTEMINFO FloweySales se-tax")
		field := func(code int, value string) { w.line("#UPPGIFT %d %s", code, value) }
		money := func(code int, v int64) {
			if v != 0 {
				field(code, strconv.FormatInt(v, 10))
			}
		}
		field(7014, strconv.Itoa(p.Number))
		for i, r := range p.A {
			base := 3100 + 10*i
			field(base, strconv.FormatInt(r.Qty, 10))
			field(base+1, clip(rowLabel(r), 80))
			field(base+2, strconv.FormatInt(r.Proceeds, 10))
			field(base+3, strconv.FormatInt(r.Cost, 10))
			money(base+4, r.Gain)
			money(base+5, r.Loss)
		}
		if len(p.A) > 0 {
			field(3300, strconv.FormatInt(p.SumA.Proceeds, 10))
			field(3301, strconv.FormatInt(p.SumA.Cost, 10))
			money(3304, p.SumA.Gain)
			money(3305, p.SumA.Loss)
		}
		for i, r := range p.D {
			base := 3410 + 10*i
			field(base, formatQty(r.QtyExact))
			field(base+1, clip(rowLabel(r), 80))
			field(base+2, strconv.FormatInt(r.Proceeds, 10))
			field(base+3, strconv.FormatInt(r.Cost, 10))
			money(base+4, r.Gain)
			money(base+5, r.Loss)
		}
		if len(p.D) > 0 {
			field(3500, strconv.FormatInt(p.SumD.Proceeds, 10))
			field(3501, strconv.FormatInt(p.SumD.Cost, 10))
			money(3503, p.SumD.Gain)
			money(3504, p.SumD.Loss)
		}
		w.line("#BLANKETTSLUT")
	}
	w.line("#FIL_SLUT")
	return w.b
}

// rowLabel is the "Beteckning": the name as the bank prints it.
func rowLabel(r Row) string {
	if r.Name != "" {
		return r.Name
	}
	return r.ISIN
}
