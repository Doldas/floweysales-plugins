package main

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"io"
	"strings"
	"testing"
)

func unzip(t *testing.T, data []byte) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		out[f.Name] = string(b)
	}
	return out
}

func TestWorkbookIsWellFormedXML(t *testing.T) {
	data, err := buildXLSX([]xSheet{
		{Name: "A/B:[x]?*", Widths: []float64{10}, Rows: [][]xCell{{txt("a < b & \"c\""), integer(5), decimal(1.5), formulaInt("SUM(B1:B1)", 5)}, nil, {note("n")}}},
		{Name: "a/b:[x]?*", Rows: [][]xCell{{txt("dup name")}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	parts := unzip(t, data)
	for _, name := range []string{"[Content_Types].xml", "_rels/.rels", "xl/workbook.xml", "xl/_rels/workbook.xml.rels", "xl/styles.xml", "xl/worksheets/sheet1.xml", "xl/worksheets/sheet2.xml"} {
		body, ok := parts[name]
		if !ok {
			t.Errorf("missing part %s", name)
			continue
		}
		var v any
		if err := xml.Unmarshal([]byte(body), &v); err != nil {
			t.Errorf("%s is not well-formed: %v", name, err)
		}
	}
	wb := strings.SplitN(parts["xl/workbook.xml"], "\n", 2)[1] // skip the XML declaration
	if strings.ContainsAny(wb, "[?*") || strings.Count(wb, "<sheet ") != 2 {
		t.Errorf("sheet names must be valid and unique: %s", wb)
	}
	if !strings.Contains(parts["xl/worksheets/sheet1.xml"], `a &lt; b &amp; &quot;c&quot;`) || !strings.Contains(parts["xl/worksheets/sheet1.xml"], `<f>SUM(B1:B1)</f><v>5</v>`) {
		t.Errorf("%s", parts["xl/worksheets/sheet1.xml"])
	}
}

func TestColumnNames(t *testing.T) {
	for i, want := range map[int]string{0: "A", 25: "Z", 26: "AA", 27: "AB", 701: "ZZ", 702: "AAA"} {
		if got := colName(i); got != want {
			t.Errorf("%d → %s, want %s", i, got, want)
		}
	}
}

func TestK4WorkbookRowsAndSumFormulas(t *testing.T) {
	var rows []Row
	for i := 0; i < 10; i++ { // ten section A rows: two pages
		rows = append(rows, Row{Name: "S", Class: ClassStock, Qty: 1, Proceeds: 100, Cost: 60, Gain: 40})
	}
	rows = append(rows, Row{Name: "Coin", Class: ClassCrypto, Qty: 1, QtyExact: 0.25, Proceeds: 50, Cost: 80, Loss: 30})
	pages := Pages(rows)
	tot := SumRows(rows)
	data, err := k4Workbook(2026, pages, rows, tot, tot.Estimate, nil, []string{"check this"})
	if err != nil {
		t.Fatal(err)
	}
	parts := unzip(t, data)
	a := parts["xl/worksheets/sheet2.xml"]
	// Page 1: rows 6-14 are data (after title, note, blank, page and header rows), row 15 is Summa; page 2: its row, then the total over both pages.
	if !strings.Contains(a, "<f>SUM(C6:C14)</f><v>900</v>") || !strings.Contains(a, "<f>SUM(E6:E14)</f><v>360</v>") {
		t.Errorf("page 1 sums:\n%s", a)
	}
	if !strings.Contains(a, "<f>C15+C") || !strings.Contains(a, "Totalt alla sidor") {
		t.Errorf("total over pages missing:\n%s", a)
	}
	wb := parts["xl/workbook.xml"]
	for _, name := range []string{"Sammanfattning", "K4 avsnitt A", "K4 avsnitt D", "Försäljningar", "Att kontrollera"} {
		if !strings.Contains(wb, name) {
			t.Errorf("sheet %q missing: %s", name, wb)
		}
	}
	d := parts["xl/worksheets/sheet3.xml"]
	if !strings.Contains(d, "<v>0.25</v>") {
		t.Errorf("section D keeps decimals:\n%s", d)
	}
}

func TestK4ToolReturnsAnOpenableWorkbook(t *testing.T) {
	res, err := callTool(t, "k4", map[string]any{"year": "2026", "files": files(), "exclude_accounts": "9999999"})
	if err != nil {
		t.Fatal(err)
	}
	f := fileNamed(res, "k4-2026.xlsx")
	if f == nil || f.MIME != xlsxMIME || f.Encoding != "base64" {
		t.Fatalf("%+v", f)
	}
	raw, err := base64.StdEncoding.DecodeString(f.Content)
	if err != nil {
		t.Fatal(err)
	}
	parts := unzip(t, raw)
	if !strings.Contains(parts["xl/worksheets/sheet2.xml"], "Volvo B") || !strings.Contains(parts["xl/worksheets/sheet2.xml"], "<v>19601</v>") {
		t.Errorf("%s", parts["xl/worksheets/sheet2.xml"])
	}
	h, err := callTool(t, "holdings", map[string]any{"files": files(), "exclude_accounts": "9999999", "as_of": "2026-12-31"})
	if err != nil || fileNamed(h, "holdings-2026-12-31.xlsx") == nil {
		t.Fatalf("holdings workbook: %v", err)
	}
}
