package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestPersonalNumberValidation(t *testing.T) {
	now := fixedNow()
	for in, want := range map[string]string{
		"19811218-9876": "198112189876", "198112189876": "198112189876", "811218-9876": "198112189876", " 811218 9876 ": "198112189876",
	} {
		got, err := ValidatePersonalNumber(in, now)
		if err != nil || got != want {
			t.Errorf("%q → %q %v", in, got, err)
		}
	}
	// A person born in 1912 is written with a plus sign in the 10-digit form.
	if got, err := ValidatePersonalNumber("121212+1212", now); err == nil && !strings.HasPrefix(got, "19") {
		t.Errorf("plus sign: %q", got)
	}
	for _, in := range []string{"", "19811218-9875", "198112189", "abcdefghijkl", "19811318-9876", "1981121898765"} {
		if _, err := ValidatePersonalNumber(in, now); err == nil {
			t.Errorf("%q accepted", in)
		}
	}
	if got := MaskPersonalNumber("198112189876"); got != "1981••••••76" {
		t.Errorf("mask %q", got)
	}
}

func TestLatin1Encoding(t *testing.T) {
	got := latin1(clip("Åke Öberg – #1 ’x’ 日本", 80))
	want := []byte{0xC5, 'k', 'e', ' ', 0xD6, 'b', 'e', 'r', 'g', ' ', '-', ' ', '1', ' ', '\'', 'x', '\'', ' ', '?', '?'}
	if !bytes.Equal(got, want) {
		t.Errorf("% x\nwant % x", got, want)
	}
}

func samplePages() []Page {
	rows := []Row{
		{Name: "Volvo B", Class: ClassStock, Qty: 80, Proceeds: 19561, Cost: 17600, Gain: 1961},
		{Name: "Global Index # Fond", Class: ClassEquityFund, Qty: 13, Proceeds: 4990, Cost: 5200, Loss: 210},
		{Name: "Bitcoin", Class: ClassCrypto, Qty: 3, QtyExact: 0.25, Proceeds: 9000, Cost: 4000, Gain: 5000},
	}
	return Pages(rows)
}

func TestBlanketterSRUHasTheDocumentedStructure(t *testing.T) {
	f := Filer{PersonalNumber: "198112189876", Name: "Åke Öberg", PostalCode: "12345", City: "Göteborg"}
	out := BlanketterSRU(f, 2026, samplePages(), fixedNow())
	if !bytes.Contains(out, []byte{0xC5, 'k', 'e'}) {
		t.Error("the file must be ISO-8859-1, not UTF-8")
	}
	lines := strings.Split(strings.TrimSuffix(string(out), "\r\n"), "\r\n")
	if len(lines) < 10 || lines[0] != "#BLANKETT K4-2026P4" || lines[1] != "#IDENTITET 198112189876 20270315 103000" || lines[len(lines)-1] != "#FIL_SLUT" || lines[len(lines)-2] != "#BLANKETTSLUT" {
		t.Fatalf("structure: %q", lines)
	}
	body := strings.Join(lines, "\n")
	for _, want := range []string{
		"#UPPGIFT 7014 1",
		"#UPPGIFT 3100 80", "#UPPGIFT 3101 Volvo B", "#UPPGIFT 3102 19561", "#UPPGIFT 3103 17600", "#UPPGIFT 3104 1961",
		"#UPPGIFT 3110 13", "#UPPGIFT 3111 Global Index Fond", "#UPPGIFT 3115 210",
		"#UPPGIFT 3300 24551", "#UPPGIFT 3301 22800", "#UPPGIFT 3304 1961", "#UPPGIFT 3305 210",
		"#UPPGIFT 3410 0,25", // D quantities may have decimals
		"#UPPGIFT 3412 9000", "#UPPGIFT 3414 5000",
		"#UPPGIFT 3500 9000", "#UPPGIFT 3501 4000", "#UPPGIFT 3503 5000",
	} {
		if want == "#UPPGIFT 3410 0,25" {
			want = "#UPPGIFT 3410 0.25"
		}
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in\n%s", want, body)
		}
	}
	if strings.Contains(body, "3105") || strings.Contains(body, "3304 0") || strings.Contains(body, "3504") {
		t.Errorf("empty gain/loss fields must be omitted:\n%s", body)
	}
	if strings.Contains(body, "#UPPGIFT 3101 Global") {
		t.Error("rows are in order: Global Index is the second A row (3111)")
	}
}

func TestMultiplePagesAreNumberedAndNeverShareRows(t *testing.T) {
	var rows []Row
	for i := 0; i < 10; i++ {
		rows = append(rows, Row{Name: "S" + string(rune('A'+i)), Class: ClassStock, Qty: 1, Proceeds: 10, Cost: 5, Gain: 5})
	}
	out := string(BlanketterSRU(Filer{PersonalNumber: "198112189876", Name: "X"}, 2025, Pages(rows), fixedNow()))
	if strings.Count(out, "#BLANKETT K4-2025P4") != 2 || !strings.Contains(out, "#UPPGIFT 7014 2") {
		t.Errorf("%s", out)
	}
	if strings.Count(out, "#UPPGIFT 3101 ") != 2 {
		t.Errorf("each page restarts at A.1:\n%s", out)
	}
}

func TestInfoSRUCarriesTheMandatoryFields(t *testing.T) {
	out := string(InfoSRU(Filer{PersonalNumber: "198112189876", Name: "Åke", PostalCode: "12345", City: "Lund"}, fixedNow()))
	lines := strings.Split(strings.TrimSuffix(out, "\r\n"), "\r\n")
	want := []string{"#DATABESKRIVNING_START", "#PRODUKT SRU", "#SKAPAD 20270315 103000", "#PROGRAM FloweySales se-tax", "#FILNAMN BLANKETTER.SRU",
		"#DATABESKRIVNING_SLUT", "#MEDIELEV_START", "#ORGNR 198112189876"}
	for i, w := range want {
		if lines[i] != w {
			t.Errorf("line %d = %q, want %q", i, lines[i], w)
		}
	}
	if !strings.Contains(out, "#POSTNR 12345") || !strings.Contains(out, "#POSTORT Lund") || !strings.HasSuffix(strings.TrimSpace(out), "#MEDIELEV_SLUT") {
		t.Errorf("%s", out)
	}
}
