package main

import (
	"archive/zip"
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

// A minimal .xlsx writer (Office Open XML) so the results open in Excel,
// LibreOffice and Google Sheets without a dependency. It supports text,
// numbers, SUM-style formulas with a cached value, a few styles and column
// widths, which is all the tax sheets need.

// Cell styles.
const (
	styleNormal  = 0
	styleBold    = 1
	styleInt     = 2 // #,##0
	styleDec     = 3 // #,##0.00
	styleBoldInt = 4
	styleHeader  = 5 // bold on grey
	styleTitle   = 6
	styleNote    = 7 // italic grey
)

// xCell is one cell: a string, an int64/int/float64, or a formula with its
// cached result (Excel recalculates formulas on open; viewers that do not
// show the cached value).
type xCell struct {
	Value   any
	Style   int
	Formula string
}

type xSheet struct {
	Name   string
	Widths []float64
	Rows   [][]xCell
	// FreezeRows keeps the first n rows visible when scrolling.
	FreezeRows int
}

func (s *xSheet) add(cells ...xCell) { s.Rows = append(s.Rows, cells) }
func (s *xSheet) blank()             { s.Rows = append(s.Rows, nil) }

func txt(v string) xCell      { return xCell{Value: v} }
func bold(v string) xCell     { return xCell{Value: v, Style: styleBold} }
func head(v string) xCell     { return xCell{Value: v, Style: styleHeader} }
func note(v string) xCell     { return xCell{Value: v, Style: styleNote} }
func integer(v int64) xCell   { return xCell{Value: v, Style: styleInt} }
func decimal(v float64) xCell { return xCell{Value: v, Style: styleDec} }
func boldInt(v int64) xCell   { return xCell{Value: v, Style: styleBoldInt} }
func formulaInt(f string, cached int64) xCell {
	return xCell{Value: cached, Style: styleBoldInt, Formula: f}
}

func colName(i int) string {
	s := ""
	for i++; i > 0; i = (i - 1) / 26 {
		s = string(rune('A'+(i-1)%26)) + s
	}
	return s
}

func cellRef(col, row int) string { return colName(col) + strconv.Itoa(row+1) }

func xmlEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '&':
			b.WriteString("&amp;")
		case r == '<':
			b.WriteString("&lt;")
		case r == '>':
			b.WriteString("&gt;")
		case r == '"':
			b.WriteString("&quot;")
		case r == '\t' || r == '\n' || r == '\r' || r >= 0x20 && r != 0xFFFE && r != 0xFFFF && !(r >= 0xD800 && r <= 0xDFFF):
			b.WriteRune(r)
		}
	}
	return b.String()
}

// sheetName makes a valid sheet name: at most 31 characters, none of []:*?/\.
func sheetName(s string) string {
	s = strings.NewReplacer("[", "(", "]", ")", ":", "-", "*", "", "?", "", "/", "-", "\\", "-").Replace(s)
	if r := []rune(s); len(r) > 31 {
		s = string(r[:31])
	}
	return s
}

func (s xSheet) xml() []byte {
	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n")
	b.WriteString(`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">`)
	if s.FreezeRows > 0 {
		fmt.Fprintf(&b, `<sheetViews><sheetView workbookViewId="0"><pane ySplit="%d" topLeftCell="A%d" activePane="bottomLeft" state="frozen"/></sheetView></sheetViews>`, s.FreezeRows, s.FreezeRows+1)
	}
	if len(s.Widths) > 0 {
		b.WriteString("<cols>")
		for i, w := range s.Widths {
			fmt.Fprintf(&b, `<col min="%d" max="%d" width="%.1f" customWidth="1"/>`, i+1, i+1, w)
		}
		b.WriteString("</cols>")
	}
	b.WriteString("<sheetData>")
	for r, row := range s.Rows {
		if len(row) == 0 {
			continue
		}
		fmt.Fprintf(&b, `<row r="%d">`, r+1)
		for c, cell := range row {
			if v, isText := cell.Value.(string); (cell.Value == nil || isText && v == "") && cell.Formula == "" {
				continue
			}
			ref := cellRef(c, r)
			style := ""
			if cell.Style != 0 {
				style = fmt.Sprintf(` s="%d"`, cell.Style)
			}
			switch v := cell.Value.(type) {
			case string:
				fmt.Fprintf(&b, `<c r="%s"%s t="inlineStr"><is><t xml:space="preserve">%s</t></is></c>`, ref, style, xmlEscape(v))
			case int64:
				b.WriteString(numCell(ref, style, cell.Formula, strconv.FormatInt(v, 10)))
			case int:
				b.WriteString(numCell(ref, style, cell.Formula, strconv.Itoa(v)))
			case float64:
				b.WriteString(numCell(ref, style, cell.Formula, strconv.FormatFloat(v, 'f', -1, 64)))
			}
		}
		b.WriteString("</row>")
	}
	b.WriteString("</sheetData></worksheet>")
	return b.Bytes()
}

func numCell(ref, style, formula, value string) string {
	if formula != "" {
		return fmt.Sprintf(`<c r="%s"%s><f>%s</f><v>%s</v></c>`, ref, style, xmlEscape(formula), value)
	}
	return fmt.Sprintf(`<c r="%s"%s><v>%s</v></c>`, ref, style, value)
}

const stylesXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">
<fonts count="4">
<font><sz val="11"/><name val="Calibri"/></font>
<font><b/><sz val="11"/><name val="Calibri"/></font>
<font><b/><sz val="14"/><name val="Calibri"/></font>
<font><i/><sz val="10"/><color rgb="FF666666"/><name val="Calibri"/></font>
</fonts>
<fills count="3"><fill><patternFill patternType="none"/></fill><fill><patternFill patternType="gray125"/></fill>
<fill><patternFill patternType="solid"><fgColor rgb="FFE7E6E6"/><bgColor indexed="64"/></patternFill></fill></fills>
<borders count="1"><border><left/><right/><top/><bottom/><diagonal/></border></borders>
<cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs>
<cellXfs count="8">
<xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0"/>
<xf numFmtId="0" fontId="1" fillId="0" borderId="0" xfId="0" applyFont="1"/>
<xf numFmtId="3" fontId="0" fillId="0" borderId="0" xfId="0" applyNumberFormat="1"/>
<xf numFmtId="4" fontId="0" fillId="0" borderId="0" xfId="0" applyNumberFormat="1"/>
<xf numFmtId="3" fontId="1" fillId="0" borderId="0" xfId="0" applyNumberFormat="1" applyFont="1"/>
<xf numFmtId="0" fontId="1" fillId="2" borderId="0" xfId="0" applyFont="1" applyFill="1"/>
<xf numFmtId="0" fontId="2" fillId="0" borderId="0" xfId="0" applyFont="1"/>
<xf numFmtId="0" fontId="3" fillId="0" borderId="0" xfId="0" applyFont="1"/>
</cellXfs>
<cellStyles count="1"><cellStyle name="Normal" xfId="0" builtinId="0"/></cellStyles>
</styleSheet>`

// buildXLSX returns the workbook as a .xlsx file.
func buildXLSX(sheets []xSheet) ([]byte, error) {
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	write := func(name, body string) error {
		w, err := zw.Create(name)
		if err != nil {
			return err
		}
		_, err = w.Write([]byte(body))
		return err
	}
	var types, wbSheets, rels strings.Builder
	types.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n" +
		`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/>` +
		`<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>` +
		`<Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/>`)
	rels.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n" + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`)
	seen := map[string]bool{}
	for i, s := range sheets {
		n := i + 1
		name := sheetName(s.Name)
		for seen[strings.ToLower(name)] {
			name = sheetName(name + "_")
		}
		seen[strings.ToLower(name)] = true
		fmt.Fprintf(&types, `<Override PartName="/xl/worksheets/sheet%d.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>`, n)
		fmt.Fprintf(&wbSheets, `<sheet name="%s" sheetId="%d" r:id="rId%d"/>`, xmlEscape(name), n, n)
		fmt.Fprintf(&rels, `<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet%d.xml"/>`, n, n)
		if err := write(fmt.Sprintf("xl/worksheets/sheet%d.xml", n), string(s.xml())); err != nil {
			return nil, err
		}
	}
	types.WriteString("</Types>")
	fmt.Fprintf(&rels, `<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/></Relationships>`, len(sheets)+1)
	parts := []struct{ name, body string }{
		{"[Content_Types].xml", types.String()},
		{"_rels/.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n" + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>`},
		{"xl/workbook.xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n" + `<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets>` + wbSheets.String() + `</sheets></workbook>`},
		{"xl/_rels/workbook.xml.rels", rels.String()},
		{"xl/styles.xml", stylesXML},
	}
	for _, p := range parts {
		if err := write(p.name, p.body); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

const xlsxMIME = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
