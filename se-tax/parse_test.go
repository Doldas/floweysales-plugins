package main

import (
	"strings"
	"testing"
)

const avanzaHeader = "Datum;Konto;Typ av transaktion;Värdepapper/beskrivning;Antal;Kurs;Belopp;Transaktionsvaluta;Courtage (SEK);Valutakurs;Instrumentvaluta;ISIN;Resultat\n"

func importOne(t *testing.T, content string, excluded ...string) ([]Tx, *ImportReport) {
	t.Helper()
	ex := map[string]bool{}
	for _, e := range excluded {
		ex[strings.ToLower(e)] = true
	}
	txs, rep, err := Import([]InputFile{{Name: "t.csv", Content: content}}, "auto", ex)
	if err != nil {
		t.Fatal(err)
	}
	return txs, rep
}

func TestParseNumberFormats(t *testing.T) {
	for in, want := range map[string]float64{
		"1 234,56": 1234.56, "1 234,56": 1234.56, "-1234.5": -1234.5, "−1 000": -1000, "1,234.50": 1234.5, "1.234,50": 1234.5,
		"12": 12, "0,5": 0.5, "+3": 3,
	} {
		got, ok := parseNumber(in)
		if !ok || got != want {
			t.Errorf("%q → %v %v, want %v", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "abc", "NaN", "Inf"} {
		if _, ok := parseNumber(in); ok {
			t.Errorf("%q accepted", in)
		}
	}
}

func TestAvanzaExportBuysSellsAndIgnoredTypes(t *testing.T) {
	txs, rep := importOne(t, "\ufeff"+avanzaHeader+
		"2025-03-03;1111111;Sälj;Volvo B;-100;250,50;25 011,00;SEK;39;;SEK;SE0000115446;\n"+
		"2024-01-02;1111111;Köp;Volvo B;100;200,00;-20 039,00;SEK;39;;SEK;SE0000115446;\n"+
		"2025-01-05;1111111;Utdelning;Volvo B;100;5;500;SEK;;;SEK;SE0000115446;\n"+
		"2025-01-06;1111111;Insättning;;;;10000;SEK;;;;;\n")
	if len(txs) != 2 || rep.Format != "avanza" || rep.Rows != 4 {
		t.Fatalf("%d txs, %+v", len(txs), rep)
	}
	var b, s Tx
	for _, tx := range txs {
		if tx.Kind == KindBuy {
			b = tx
		} else {
			s = tx
		}
	}
	near(t, "buy gross", b.Gross, 20000)
	near(t, "buy fee", b.Fee, 39)
	near(t, "sell gross", s.Gross, 25050)
	near(t, "sell qty", s.Qty, 100)
	if rep.Ignored["Utdelning"] != 1 || rep.Ignored["Insättning"] != 1 || rep.Accounts["1111111"] != 2 {
		t.Errorf("%+v", rep)
	}
	res := Replay(txs, Options{Method: AverageOnly})
	near(t, "gain", res.Sales[0].Gain(), 25011-20039)
}

func TestExcludedAccountsAreLeftOut(t *testing.T) {
	txs, rep := importOne(t, avanzaHeader+
		"2025-02-02;2222222;Köp;ISK Fond;10;100;-1 000;SEK;0;;SEK;SE0001;\n"+
		"2025-02-03;1111111;Köp;Volvo B;1;100;-100;SEK;0;;SEK;SE0002;\n", "2222222")
	if len(txs) != 1 || txs[0].Account != "1111111" || rep.Ignored["(excluded account)"] != 1 {
		t.Fatalf("%+v %+v", txs, rep)
	}
}

func TestForeignCurrencyUsesTheRateInTheFileAndSaysSo(t *testing.T) {
	txs, rep := importOne(t, avanzaHeader+"2025-02-03;1;Köp;Apple;10;190;-19 600;USD;20;10,5;USD;US0378331005;\n")
	if len(txs) != 1 {
		t.Fatal(rep.Warnings)
	}
	near(t, "gross in SEK", txs[0].Gross, 10*190*10.5)
	if len(rep.Warnings) == 0 || !strings.Contains(rep.Warnings[0], "USD") {
		t.Errorf("warnings %v", rep.Warnings)
	}
	txs, rep = importOne(t, avanzaHeader+"2025-02-03;1;Köp;Apple;10;190;-1900;USD;0;;USD;US0378331005;\n")
	if len(txs) != 0 || len(rep.Warnings) == 0 {
		t.Errorf("a foreign trade without a rate must be skipped with a warning: %d %v", len(txs), rep.Warnings)
	}
}

func TestBrokenRowsAreSkippedWithAWarning(t *testing.T) {
	txs, rep := importOne(t, avanzaHeader+
		"not-a-date;1;Köp;Volvo;1;1;-1;SEK;0;;SEK;SE1;\n"+
		"2025-01-01;1;Köp;Volvo;;1;-1;SEK;0;;SEK;SE1;\n"+
		"2025-01-01;1;Köp;;5;1;-1;SEK;0;;SEK;;\n"+
		"2025-01-01;1;Köp;Volvo;5;1;-5;SEK;0;;SEK;SE1;\n")
	if len(txs) != 1 || len(rep.Warnings) != 3 {
		t.Errorf("%d txs, warnings %v", len(txs), rep.Warnings)
	}
}

func TestSplitAndTransferRows(t *testing.T) {
	txs, _ := importOne(t, avanzaHeader+
		"2025-01-01;1;Split;Volvo;100;;;SEK;;;SEK;SE1;\n"+
		"2025-01-02;1;Värdepappersöverföring;Volvo;20;;;SEK;;;SEK;SE1;\n"+
		"2025-01-03;1;Värdepappersöverföring;Volvo;-5;;;SEK;;;SEK;SE1;\n")
	if len(txs) != 3 || txs[0].Kind != KindAdjust || txs[0].Qty != 100 || txs[1].Kind != KindTransferIn || txs[2].Kind != KindTransferOut || txs[2].Qty != 5 {
		t.Errorf("%+v", txs)
	}
}

func TestGenericCSVWithOpeningBalanceAndComma(t *testing.T) {
	txs, rep := importOne(t, "date,type,name,isin,class,quantity,amount_sek,fee_sek,account\n"+
		"2025-01-01,opening,Global Index,SE9,aktiefond,\"12,5\",\"4 200,00\",0,A\n"+
		"2025-06-01,sell,Global Index,SE9,aktiefond,12.5,5000,10,A\n"+
		"2025-06-02,mystery,X,SE1,aktie,1,1,0,A\n")
	if rep.Format != "generic" || len(txs) != 2 || rep.Ignored["mystery"] != 1 {
		t.Fatalf("%+v %+v", txs, rep)
	}
	res := Replay(txs, Options{Method: BestMethod})
	near(t, "cost from the opening balance", res.Sales[0].Cost, 4200)
	near(t, "proceeds", res.Sales[0].Proceeds, 4990)
	if res.Sales[0].Class != ClassEquityFund {
		t.Errorf("class %s", res.Sales[0].Class)
	}
}

func TestUnknownFormatIsExplained(t *testing.T) {
	_, _, err := Import([]InputFile{{Name: "x.csv", Content: "a;b;c\n1;2;3\n"}}, "auto", nil)
	if err == nil || !strings.Contains(err.Error(), "Avanza") {
		t.Errorf("%v", err)
	}
	if _, _, err := Import([]InputFile{{Name: "x.csv", Content: ""}}, "auto", nil); err == nil {
		t.Error("an empty file must be an error")
	}
}

func TestLatin1FilesAreDecoded(t *testing.T) {
	raw := "Datum;Konto;Typ av transaktion;Värdepapper/beskrivning;Antal;Belopp\n2025-01-01;1;Köp;Åhlens;1;-100\n"
	latin := make([]byte, 0, len(raw))
	for _, r := range raw {
		latin = append(latin, byte(r))
	}
	txs, _ := importOne(t, string(latin))
	if len(txs) != 1 || txs[0].Name != "Åhlens" {
		t.Errorf("%+v", txs)
	}
}
