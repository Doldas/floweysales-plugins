package main

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// ImportReport describes what was read from the files, for the review tool and
// for warnings next to a result.
type ImportReport struct {
	Format   string
	Rows     int
	Files    []string
	Types    map[string]int // transaction type text → rows
	Accounts map[string]int // account → trades read
	Ignored  map[string]int // type text → rows that carry no tax event
	Warnings []string
	First    time.Time
	Last     time.Time
}

func newReport() *ImportReport {
	return &ImportReport{Types: map[string]int{}, Accounts: map[string]int{}, Ignored: map[string]int{}}
}

func (r *ImportReport) warn(format string, args ...any) {
	if len(r.Warnings) < 40 {
		r.Warnings = append(r.Warnings, fmt.Sprintf(format, args...))
	}
}

// InputFile is one uploaded file.
type InputFile struct{ Name, Content string }

// decodeText returns the file as UTF-8: a BOM is dropped and bytes that are
// not valid UTF-8 are read as ISO-8859-1 (some bank exports still are).
func decodeText(s string) string {
	s = strings.TrimPrefix(s, "\ufeff")
	if utf8.ValidString(s) {
		return s
	}
	runes := make([]rune, 0, len(s))
	for i := 0; i < len(s); i++ {
		runes = append(runes, rune(s[i]))
	}
	return string(runes)
}

// parseNumber reads Swedish and English number formats: "1 234,56", "-1234.5",
// non-breaking spaces and the Unicode minus. Empty text is (0, false).
func parseNumber(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	s = strings.NewReplacer(" ", "", " ", "", " ", "", "−", "-", "–", "-").Replace(s)
	s = strings.TrimPrefix(s, "+")
	switch {
	case strings.Contains(s, ",") && strings.Contains(s, "."):
		if strings.LastIndex(s, ",") > strings.LastIndex(s, ".") {
			s = strings.ReplaceAll(strings.ReplaceAll(s, ".", ""), ",", ".")
		} else {
			s = strings.ReplaceAll(s, ",", "")
		}
	case strings.Contains(s, ","):
		s = strings.ReplaceAll(s, ",", ".")
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return v, true
}

func parseDate(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	for _, layout := range []string{"2006-01-02", "20060102", "2006/01/02", "02.01.2006", "2/1/2006"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	if len(s) > 10 { // a date with a time
		return parseDate(s[:10])
	}
	return time.Time{}, false
}

// table is a CSV with a header row; columns are found by (case-folded) name.
type table struct {
	header []string
	rows   [][]string
	line   []int
}

func readTable(content string) (*table, error) {
	content = decodeText(content)
	first := content
	if i := strings.IndexByte(content, '\n'); i >= 0 {
		first = content[:i]
	}
	delim := ';'
	switch {
	case strings.Count(first, ";") >= 2:
	case strings.Count(first, "\t") >= 2:
		delim = '\t'
	case strings.Count(first, ",") >= 2:
		delim = ','
	}
	r := csv.NewReader(bytes.NewReader([]byte(content)))
	r.Comma = delim
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	r.TrimLeadingSpace = true
	t := &table{}
	row := 0
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", row+1, err)
		}
		row++
		blank := true
		for _, c := range rec {
			if strings.TrimSpace(c) != "" {
				blank = false
			}
		}
		if blank {
			continue
		}
		if t.header == nil {
			for i := range rec {
				rec[i] = strings.ToLower(strings.TrimSpace(rec[i]))
			}
			t.header = rec
			continue
		}
		t.rows = append(t.rows, rec)
		t.line = append(t.line, row)
	}
	if t.header == nil {
		return nil, fmt.Errorf("the file is empty")
	}
	return t, nil
}

// col returns the index of the first header that matches, or -1.
func (t *table) col(names ...string) int {
	for _, n := range names {
		for i, h := range t.header {
			if h == n {
				return i
			}
		}
	}
	return -1
}

func cell(rec []string, i int) string {
	if i < 0 || i >= len(rec) {
		return ""
	}
	return strings.TrimSpace(rec[i])
}

// Import reads every file, detects its format and returns the transactions.
// Format is "auto", "avanza" or "generic".
func Import(files []InputFile, format string, excluded map[string]bool) ([]Tx, *ImportReport, error) {
	report := newReport()
	var all []Tx
	for _, f := range files {
		name := f.Name
		if name == "" {
			name = "input"
		}
		t, err := readTable(f.Content)
		if err != nil {
			return nil, report, fmt.Errorf("%s: %w", name, err)
		}
		kind := format
		if kind == "" || kind == "auto" {
			kind = detectFormat(t)
		}
		var txs []Tx
		switch kind {
		case "avanza":
			txs, err = parseAvanza(t, name, report, excluded)
		case "generic":
			txs, err = parseGeneric(t, name, report, excluded)
		default:
			return nil, report, fmt.Errorf("%s: unrecognised file. Expected an Avanza transaction export (Datum;Konto;Typ av transaktion;…) or the generic CSV (date,type,name,isin,quantity,amount_sek,fee_sek,…); see the Guide tool", name)
		}
		if err != nil {
			return nil, report, fmt.Errorf("%s: %w", name, err)
		}
		if report.Format == "" {
			report.Format = kind
		} else if report.Format != kind {
			report.Format = "mixed"
		}
		report.Files = append(report.Files, name)
		all = append(all, txs...)
	}
	for _, tx := range all {
		if report.First.IsZero() || tx.Date.Before(report.First) {
			report.First = tx.Date
		}
		if tx.Date.After(report.Last) {
			report.Last = tx.Date
		}
	}
	return all, report, nil
}

func detectFormat(t *table) string {
	if t.col("typ av transaktion") >= 0 && t.col("datum") >= 0 {
		return "avanza"
	}
	if t.col("type", "typ") >= 0 && t.col("date", "datum") >= 0 && t.col("quantity", "antal") >= 0 {
		return "generic"
	}
	return ""
}

// parseAvanza reads Avanza's "Transaktioner" export.
func parseAvanza(t *table, file string, report *ImportReport, excluded map[string]bool) ([]Tx, error) {
	cDate, cAcc, cType := t.col("datum"), t.col("konto"), t.col("typ av transaktion")
	cName, cQty, cPrice := t.col("värdepapper/beskrivning", "värdepapper", "beskrivning"), t.col("antal"), t.col("kurs")
	cAmount, cCur := t.col("belopp"), t.col("transaktionsvaluta", "valuta")
	cFee, cRate, cISIN := t.col("courtage (sek)", "courtage"), t.col("valutakurs"), t.col("isin")
	if cDate < 0 || cType < 0 || cAmount < 0 || cQty < 0 {
		return nil, fmt.Errorf("the Avanza export must have the columns Datum, Typ av transaktion, Antal and Belopp")
	}
	var out []Tx
	for i, rec := range t.rows {
		src := fmt.Sprintf("%s:%d", file, t.line[i])
		typ := cell(rec, cType)
		report.Types[typ]++
		report.Rows++
		account := cell(rec, cAcc)
		if excluded[strings.ToLower(account)] {
			report.Ignored["(excluded account)"]++
			continue
		}
		var kind Kind
		switch strings.ToLower(typ) {
		case "köp":
			kind = KindBuy
		case "sälj":
			kind = KindSell
		case "värdepappersöverföring", "överföring", "insättning värdepapper", "uttag värdepapper":
			qty, _ := parseNumber(cell(rec, cQty))
			if qty >= 0 {
				kind = KindTransferIn
			} else {
				kind = KindTransferOut
			}
		case "split", "omvänd split", "fondemission", "nyemission", "uppdelning", "sammanläggning":
			kind = KindAdjust
		default:
			report.Ignored[typ]++
			continue
		}
		date, ok := parseDate(cell(rec, cDate))
		if !ok {
			report.warn("%s: unreadable date %q; row skipped", src, cell(rec, cDate))
			continue
		}
		qty, ok := parseNumber(cell(rec, cQty))
		if !ok || qty == 0 {
			report.warn("%s: %s without a quantity; row skipped", src, typ)
			continue
		}
		name, isin := cell(rec, cName), strings.ToUpper(cell(rec, cISIN))
		if name == "" && isin == "" {
			report.warn("%s: %s without a security name; row skipped", src, typ)
			continue
		}
		tx := Tx{Date: date, Account: account, Kind: kind, Name: name, ISIN: isin, Source: src}
		if kind != KindAdjust {
			tx.Qty = math.Abs(qty)
		} else {
			tx.Qty = qty
		}
		fee, _ := parseNumber(cell(rec, cFee))
		tx.Fee = math.Abs(fee)
		if kind == KindBuy || kind == KindSell {
			amount, ok := parseNumber(cell(rec, cAmount))
			if !ok {
				report.warn("%s: %s without an amount; row skipped", src, typ)
				continue
			}
			amount = math.Abs(amount)
			cur := strings.ToUpper(cell(rec, cCur))
			if cur != "" && cur != "SEK" {
				// The cash amount is in another currency: price × quantity × rate is the SEK value.
				rate, _ := parseNumber(cell(rec, cRate))
				price, _ := parseNumber(cell(rec, cPrice))
				if rate > 0 && price > 0 {
					tx.Gross = price * tx.Qty * rate
					report.warn("%s: %s settled in %s; converted with the rate in the file (%.4f). Check it against the contract note.", src, name, cur, rate)
				} else {
					report.warn("%s: %s settled in %s without a usable exchange rate; row skipped", src, name, cur)
					continue
				}
			} else if kind == KindBuy {
				tx.Gross = math.Max(amount-tx.Fee, 0) // the cash paid includes the fee
			} else {
				tx.Gross = amount + tx.Fee // the cash received is after the fee
			}
		}
		report.Accounts[account]++
		out = append(out, tx)
	}
	return out, nil
}

// parseGeneric reads the documented neutral CSV:
//
//	date,type,name,isin,class,quantity,amount_sek,fee_sek,account
//
// type is buy, sell, opening, split or transfer_in/transfer_out; amount_sek is
// price × quantity before the fee (for opening, the total cost).
func parseGeneric(t *table, file string, report *ImportReport, excluded map[string]bool) ([]Tx, error) {
	cDate, cType := t.col("date", "datum"), t.col("type", "typ")
	cName, cISIN, cClass := t.col("name", "namn"), t.col("isin"), t.col("class", "klass")
	cQty, cAmount, cFee, cAcc := t.col("quantity", "antal"), t.col("amount_sek", "belopp_sek", "amount"), t.col("fee_sek", "courtage_sek", "fee"), t.col("account", "konto")
	if cDate < 0 || cType < 0 || cQty < 0 {
		return nil, fmt.Errorf("the generic CSV needs the columns date, type and quantity")
	}
	var out []Tx
	for i, rec := range t.rows {
		src := fmt.Sprintf("%s:%d", file, t.line[i])
		typ := strings.ToLower(cell(rec, cType))
		report.Types[typ]++
		report.Rows++
		account := cell(rec, cAcc)
		if excluded[strings.ToLower(account)] {
			report.Ignored["(excluded account)"]++
			continue
		}
		var kind Kind
		switch typ {
		case "buy", "köp", "kop":
			kind = KindBuy
		case "sell", "sälj", "salj":
			kind = KindSell
		case "opening", "ingående", "ingaende":
			kind = KindOpening
		case "split", "adjust":
			kind = KindAdjust
		case "transfer_in", "överföring in":
			kind = KindTransferIn
		case "transfer_out", "överföring ut":
			kind = KindTransferOut
		default:
			report.Ignored[typ]++
			continue
		}
		date, ok := parseDate(cell(rec, cDate))
		if !ok {
			report.warn("%s: unreadable date %q; row skipped", src, cell(rec, cDate))
			continue
		}
		qty, ok := parseNumber(cell(rec, cQty))
		if !ok || qty == 0 {
			report.warn("%s: %s without a quantity; row skipped", src, typ)
			continue
		}
		tx := Tx{Date: date, Account: account, Kind: kind, Name: cell(rec, cName), ISIN: strings.ToUpper(cell(rec, cISIN)), Source: src}
		if tx.Name == "" && tx.ISIN == "" {
			report.warn("%s: %s without a name or ISIN; row skipped", src, typ)
			continue
		}
		if kind == KindAdjust {
			tx.Qty = qty
		} else {
			tx.Qty = math.Abs(qty)
		}
		if c, ok := parseClass(cell(rec, cClass)); ok {
			tx.Class = c
		} else if v := cell(rec, cClass); v != "" {
			report.warn("%s: unknown class %q (use %s); guessing from the name", src, v, classNames())
		}
		amount, hasAmount := parseNumber(cell(rec, cAmount))
		fee, _ := parseNumber(cell(rec, cFee))
		tx.Fee = math.Abs(fee)
		if kind == KindBuy || kind == KindSell || kind == KindOpening {
			if !hasAmount {
				report.warn("%s: %s without amount_sek; row skipped", src, typ)
				continue
			}
			tx.Gross = math.Abs(amount)
		}
		report.Accounts[account]++
		out = append(out, tx)
	}
	return out, nil
}

func classNames() string {
	names := make([]string, len(allClasses))
	for i, c := range allClasses {
		names[i] = string(c)
	}
	return strings.Join(names, ", ")
}
