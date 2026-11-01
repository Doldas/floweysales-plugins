package main

import (
	"fmt"
	"strings"
)

// k4Workbook lays the result out as an Excel workbook: a summary, one sheet
// per K4 section with the rows exactly as the form has them (and SUM formulas
// for the Summa lines), every sale, and the things to check.
func k4Workbook(year int, pages []Page, rows []Row, totals Totals, est Estimate, sales []Sale, warnings []string) ([]byte, error) {
	sheets := []xSheet{summarySheet(year, totals, est, len(pages))}
	for _, section := range []string{"A", "D"} {
		if s, ok := sectionSheet(year, section, pages); ok {
			sheets = append(sheets, s)
		}
	}
	sheets = append(sheets, salesSheet(year, sales))
	if len(warnings) > 0 {
		w := xSheet{Name: "Att kontrollera", Widths: []float64{120}}
		w.add(xCell{Value: "Things to look at before you declare", Style: styleTitle})
		for _, m := range warnings {
			w.add(txt(m))
		}
		sheets = append(sheets, w)
	}
	return buildXLSX(sheets)
}

func summarySheet(year int, t Totals, e Estimate, pages int) xSheet {
	s := xSheet{Name: "Sammanfattning", Widths: []float64{12, 52, 16}}
	s.add(xCell{Value: fmt.Sprintf("K4 – försäljning av värdepapper, inkomstår %d", year), Style: styleTitle})
	s.add(note(fmt.Sprintf("%d K4-sida/sidor. Beräknat från dina egna filer – kontrollera mot avräkningsnotor och bankens årsbesked.", pages)))
	s.blank()
	s.add(head("Ruta"), head("Belopp att föra över till inkomstdeklarationen (INK1)"), head("Kronor"))
	s.add(txt("7.4"), txt("Vinst, fondandelar m.m. (K4 avsnitt A)"), integer(t.GainA))
	s.add(txt("8.3"), txt("Förlust, fondandelar m.m. (K4 avsnitt A)"), integer(t.LossA))
	s.add(txt("7.5"), txt("Vinst, ej marknadsnoterade m.m. (K4 avsnitt D)"), integer(t.GainD))
	s.add(txt("8.4"), txt("Förlust, ej marknadsnoterade m.m. (K4 avsnitt D)"), integer(t.LossD))
	s.add(note("Skatteverket kan ha förifyllt 7.4/8.3 från bankens kontrolluppgifter – lägg då till dina K4-summor. Kvittning och kvotering gör Skatteverket."))
	s.blank()
	s.add(head(""), head("Uppskattad skatteeffekt (förenklad)"), head("Kronor"))
	line := func(label string, v float64) { s.add(txt(""), txt(label), integer(int64(roundHalf(v)))) }
	line("Vinster", e.GainSetOff+e.GainOther)
	line("Förluster som kvittas fullt mot vinster", -e.LossFullOffset)
	line("Resterande förluster, 70 %", -e.LossQuotedTotal)
	line("Resultat från K4", e.NetK4)
	if e.OtherIncome != 0 {
		line("Övriga kapitalinkomster (din inmatning)", e.OtherIncome)
	}
	line("Kapitalinkomst", e.CapitalIncome)
	if e.Tax > 0 {
		line("Skatt 30 %", e.Tax)
	}
	if e.Deficit > 0 {
		line("Underskott av kapital", e.Deficit)
		line("Skattereduktion", e.TaxReduction)
	}
	s.add(note("Endast en uppskattning – Skatteverkets beslut gäller."))
	return s
}

func roundHalf(v float64) float64 {
	if v < 0 {
		return -float64(int64(-v + 0.5))
	}
	return float64(int64(v + 0.5))
}

// sectionSheet writes the K4 pages of one section as the form has them.
func sectionSheet(year int, section string, pages []Page) (xSheet, bool) {
	s := xSheet{Name: "K4 avsnitt " + section, Widths: []float64{9, 40, 18, 18, 14, 14, 16, 16, 16}}
	title := "Avsnitt A – marknadsnoterade aktier, aktiefonder och andra fondandelar"
	if section == "D" {
		title = "Avsnitt D – övriga värdepapper (krypto, ej marknadsnoterade)"
	}
	s.add(xCell{Value: fmt.Sprintf("K4 %d – %s", year, title), Style: styleTitle})
	s.add(note("Skriv av raderna i samma ordning i K4. Summa-raderna är formler."))
	s.blank()
	var sumRows []int
	found := false
	for _, pg := range pages {
		rows, sums := pg.A, pg.SumA
		if section == "D" {
			rows, sums = pg.D, pg.SumD
		}
		if len(rows) == 0 {
			continue
		}
		found = true
		s.add(bold(fmt.Sprintf("Sida %d av %d", pg.Number, len(pages))))
		s.add(head("Antal"), head("Beteckning"), head("Försäljningspris"), head("Omkostnadsbelopp"), head("Vinst"), head("Förlust"), head("Typ"), head("Kostnadsmetod"), head("ISIN"))
		first := len(s.Rows) + 1 // Excel row number of the first data row
		for _, r := range rows {
			qty := xCell{Value: r.Qty, Style: styleInt}
			if section == "D" {
				qty = xCell{Value: r.QtyExact, Style: styleDec}
			}
			s.add(qty, txt(rowLabel(r)), integer(r.Proceeds), integer(r.Cost), optInt(r.Gain), optInt(r.Loss), txt(r.Class.Label()), txt(methodLabel(r)), txt(r.ISIN))
		}
		last := len(s.Rows)
		sum := func(col string, cached int64) xCell {
			return formulaInt(fmt.Sprintf("SUM(%s%d:%s%d)", col, first, col, last), cached)
		}
		s.add(xCell{}, bold("Summa"), sum("C", sums.Proceeds), sum("D", sums.Cost), sum("E", sums.Gain), sum("F", sums.Loss))
		sumRows = append(sumRows, len(s.Rows))
		s.blank()
	}
	if !found {
		return xSheet{}, false
	}
	if len(sumRows) > 1 {
		total := func(col string) xCell {
			refs := make([]string, len(sumRows))
			var cached int64
			for i, r := range sumRows {
				refs[i] = fmt.Sprintf("%s%d", col, r)
			}
			for _, pg := range pages {
				sums := pg.SumA
				if section == "D" {
					sums = pg.SumD
				}
				switch col {
				case "C":
					cached += sums.Proceeds
				case "D":
					cached += sums.Cost
				case "E":
					cached += sums.Gain
				case "F":
					cached += sums.Loss
				}
			}
			return formulaInt(strings.Join(refs, "+"), cached)
		}
		s.add(xCell{}, bold("Totalt alla sidor"), total("C"), total("D"), total("E"), total("F"))
	}
	s.FreezeRows = 0
	return s, true
}

// optInt leaves the cell empty for zero, as the form does for the unused
// Vinst/Förlust column.
func optInt(v int64) xCell {
	if v == 0 {
		return xCell{}
	}
	return integer(v)
}

func salesSheet(year int, sales []Sale) xSheet {
	s := xSheet{Name: "Försäljningar", Widths: []float64{12, 14, 40, 16, 16, 10, 16, 12, 16, 14, 18, 16, 16, 50}, FreezeRows: 1}
	s.add(head("Datum"), head("Konto"), head("Värdepapper"), head("ISIN"), head("Typ"), head("Antal"), head("Försäljningspris"), head("Avgift"),
		head("Omkostnadsbelopp"), head("Metod"), head("Genomsnittsmetod"), head("Schablon 20 %"), head("Vinst/förlust"), head("Anmärkning"))
	for _, x := range sales {
		if x.Date.Year() != year || x.Planned {
			continue
		}
		s.add(txt(x.Date.Format("2006-01-02")), txt(x.Account), txt(x.Name), txt(x.ISIN), txt(x.Class.Label()), xCell{Value: x.Qty, Style: styleDec},
			decimal(x.Proceeds), decimal(x.Fee), decimal(x.Cost), txt(string(x.Method)), decimal(x.Average), decimal(x.FlatRate), decimal(x.Gain()), txt(strings.Join(x.Notes, "; ")))
	}
	return s
}

func holdingsWorkbook(asOf string, holdings []Holding) ([]byte, error) {
	s := xSheet{Name: "Innehav", Widths: []float64{40, 16, 16, 12, 18, 18, 36}, FreezeRows: 1}
	s.add(head("Värdepapper"), head("ISIN"), head("Typ"), head("Antal"), head("Omkostnadsbelopp"), head("Genomsnitt per styck"), head("Anmärkning"))
	for _, h := range holdings {
		n := ""
		if h.CostUnknown {
			n = "kostnad okänd för överförda aktier"
		}
		s.add(txt(h.Name), txt(h.ISIN), txt(h.Class.Label()), xCell{Value: h.Qty, Style: styleDec}, decimal(h.Cost), decimal(h.AvgCost()), txt(n))
	}
	return buildXLSX([]xSheet{s})
}
