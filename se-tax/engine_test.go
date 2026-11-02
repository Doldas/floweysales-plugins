package main

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
)

func d(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

func buy(date, name string, qty, gross, fee float64) Tx {
	return Tx{Date: d(date), Kind: KindBuy, Name: name, ISIN: "SE" + strings.ToUpper(strings.ReplaceAll(name, " ", "")), Qty: qty, Gross: gross, Fee: fee, Class: ClassStock}
}

func sell(date, name string, qty, gross, fee float64) Tx {
	tx := buy(date, name, qty, gross, fee)
	tx.Kind = KindSell
	return tx
}

func near(t *testing.T, what string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 0.005 {
		t.Errorf("%s = %.4f, want %.4f", what, got, want)
	}
}

// Skatteverket's brochure (SKV 332): 200 shares bought for 62 000 kr incl. 200 kr
// courtage have an average cost of 310 kr; selling 50 takes 15 500 kr and leaves 46 500 kr.
func TestAverageCostMatchesSkatteverketExample(t *testing.T) {
	res := Replay([]Tx{
		buy("2020-01-10", "ABC", 200, 61800, 200),
		sell("2021-05-10", "ABC", 50, 20000, 0),
	}, Options{Method: AverageOnly})
	if len(res.Sales) != 1 {
		t.Fatalf("%+v", res.Sales)
	}
	near(t, "cost", res.Sales[0].Cost, 15500)
	near(t, "gain", res.Sales[0].Gain(), 4500)
	if len(res.Holdings) != 1 {
		t.Fatalf("%+v", res.Holdings)
	}
	near(t, "remaining cost", res.Holdings[0].Cost, 46500)
	near(t, "remaining qty", res.Holdings[0].Qty, 150)
}

// 200 shares × 360 kr − 200 kr courtage = 71 800 kr; the flat rate is 14 360 kr.
func TestFlatRateAppliesOnlyWhenBetterAndAllowed(t *testing.T) {
	buys := buy("2010-01-10", "OLD", 200, 1000, 0) // a tiny cost, so 20% is better
	s := sell("2021-05-10", "OLD", 200, 72000, 200)
	res := Replay([]Tx{buys, s}, Options{Method: BestMethod})
	near(t, "proceeds", res.Sales[0].Proceeds, 71800)
	near(t, "cost", res.Sales[0].Cost, 14360)
	if res.Sales[0].Method != MethodFlatRate {
		t.Errorf("method %s", res.Sales[0].Method)
	}
	avg := Replay([]Tx{buys, s}, Options{Method: AverageOnly})
	near(t, "average only cost", avg.Sales[0].Cost, 1000)

	fund := buys
	fund.Class = ClassEquityFund
	fs := s
	fs.Class = ClassEquityFund
	r := Replay([]Tx{fund, fs}, Options{Method: BestMethod})
	near(t, "fund never uses the flat rate", r.Sales[0].Cost, 1000)

	// A higher real cost beats 20%.
	r = Replay([]Tx{buy("2020-01-10", "ABC", 200, 60000, 0), sell("2021-05-10", "ABC", 200, 72000, 200)}, Options{Method: BestMethod})
	near(t, "real cost", r.Sales[0].Cost, 60000)
	if r.Sales[0].Method != MethodAverage {
		t.Errorf("method %s", r.Sales[0].Method)
	}
}

func TestFlatRateSaleStillReducesThePoolByAverageCost(t *testing.T) {
	res := Replay([]Tx{
		buy("2010-01-10", "OLD", 100, 1000, 0),
		sell("2021-05-10", "OLD", 50, 50000, 0), // flat rate: 10 000 > average 500
		sell("2021-06-10", "OLD", 50, 50000, 0),
	}, Options{Method: BestMethod})
	near(t, "second sale average", res.Sales[1].Average, 500)
}

func TestSplitChangesUnitsNotTotalCost(t *testing.T) {
	split := Tx{Date: d("2021-01-01"), Kind: KindAdjust, Name: "ABC", ISIN: "SEABC", Qty: 100, Class: ClassStock}
	res := Replay([]Tx{buy("2020-01-10", "ABC", 100, 10000, 0), split, sell("2021-02-01", "ABC", 100, 9000, 0)}, Options{Method: AverageOnly})
	near(t, "cost after split", res.Sales[0].Cost, 5000)
	if len(res.Holdings) != 1 || res.Holdings[0].Qty != 100 {
		t.Fatalf("%+v", res.Holdings)
	}
	reverse := Tx{Date: d("2021-01-01"), Kind: KindAdjust, Name: "ABC", ISIN: "SEABC", Qty: -90, Class: ClassStock}
	res = Replay([]Tx{buy("2020-01-10", "ABC", 100, 10000, 0), reverse}, Options{})
	near(t, "reverse split keeps cost", res.Holdings[0].Cost, 10000)
	near(t, "reverse split units", res.Holdings[0].Qty, 10)
}

func TestSellingMoreThanHeldWarnsAndNeverInventsCost(t *testing.T) {
	res := Replay([]Tx{buy("2020-01-10", "ABC", 10, 1000, 0), sell("2021-05-10", "ABC", 30, 6000, 0)}, Options{Method: AverageOnly})
	near(t, "cost covers only held units", res.Sales[0].Cost, 1000)
	if len(res.Warnings) == 0 || !strings.Contains(res.Warnings[0], "purchase history") {
		t.Errorf("warnings %v", res.Warnings)
	}
	if len(res.Sales[0].Notes) == 0 {
		t.Error("the sale must carry a note")
	}
}

func TestTransferInWarnsAboutUnknownCost(t *testing.T) {
	in := Tx{Date: d("2020-01-10"), Kind: KindTransferIn, Name: "ABC", ISIN: "SEABC", Qty: 10, Class: ClassStock}
	res := Replay([]Tx{in, sell("2021-05-10", "ABC", 10, 6000, 0)}, Options{Method: BestMethod})
	if len(res.Warnings) == 0 || !strings.Contains(res.Warnings[0], "opening balance") {
		t.Errorf("warnings %v", res.Warnings)
	}
	if res.Sales[0].Method != MethodFlatRate {
		t.Errorf("with no known cost the flat rate is the sound basis for a share, got %s", res.Sales[0].Method)
	}
}

func TestSameDayBuysApplyBeforeSells(t *testing.T) {
	res := Replay([]Tx{sell("2021-05-10", "ABC", 10, 1100, 0), buy("2021-05-10", "ABC", 10, 1000, 0)}, Options{Method: AverageOnly})
	if len(res.Warnings) != 0 {
		t.Errorf("%v", res.Warnings)
	}
	near(t, "cost", res.Sales[0].Cost, 1000)
}

func TestRowsRoundAndSplitGainFromLoss(t *testing.T) {
	sales := []Sale{
		{Date: d("2025-03-01"), Name: "B Fund", Key: "b", Class: ClassEquityFund, Qty: 10.4, Proceeds: 1000.4, Cost: 1200.4},
		{Date: d("2025-04-01"), Name: "A Stock", Key: "a", Class: ClassStock, Qty: 5, Proceeds: 700.6, Cost: 500, Method: MethodFlatRate},
		{Date: d("2025-05-01"), Name: "A Stock", Key: "a", Class: ClassStock, Qty: 5, Proceeds: 100, Cost: 100},
		{Date: d("2024-12-31"), Name: "Old", Key: "o", Class: ClassStock, Qty: 1, Proceeds: 1, Cost: 1},
		{Date: d("2025-06-01"), Name: "Coin", Key: "c", Class: ClassCrypto, Qty: 0.5, Proceeds: 300, Cost: 100},
	}
	rows := Rows(sales, 2025, false)
	if len(rows) != 3 {
		t.Fatalf("%+v", rows)
	}
	if rows[0].Name != "A Stock" || rows[0].Qty != 10 || rows[0].Proceeds != 801 || rows[0].Cost != 600 || rows[0].Gain != 201 || rows[0].Loss != 0 {
		t.Errorf("%+v", rows[0])
	}
	if rows[1].Name != "B Fund" || rows[1].Qty != 10 || rows[1].Gain != 0 || rows[1].Loss != 200 || len(rows[1].Notes) == 0 {
		t.Errorf("fund row: %+v", rows[1])
	}
	if rows[2].Class != ClassCrypto || rows[2].Gain != 200 {
		t.Errorf("crypto row: %+v", rows[2])
	}
	if got := Rows(sales, 2025, false); len(got) != 3 {
		t.Error("rows must be repeatable")
	}
}

func TestPagesFollowTheFormCapacity(t *testing.T) {
	var rows []Row
	for i := 0; i < 20; i++ {
		rows = append(rows, Row{Name: fmt.Sprint("S", i), Class: ClassStock, Proceeds: 100, Cost: 60, Gain: 40})
	}
	for i := 0; i < 8; i++ {
		rows = append(rows, Row{Name: fmt.Sprint("C", i), Class: ClassCrypto, Proceeds: 10, Cost: 20, Loss: 10})
	}
	pages := Pages(rows)
	if len(pages) != 3 {
		t.Fatalf("20 A rows need 3 pages: %d", len(pages))
	}
	if len(pages[0].A) != 9 || len(pages[2].A) != 2 || len(pages[0].D) != 7 || len(pages[1].D) != 1 || len(pages[2].D) != 0 {
		t.Errorf("layout %d/%d %d/%d/%d", len(pages[0].A), len(pages[2].A), len(pages[0].D), len(pages[1].D), len(pages[2].D))
	}
	if pages[0].SumA.Gain != 360 || pages[0].SumD.Loss != 70 || pages[2].SumD != (Sums{}) {
		t.Errorf("sums %+v %+v", pages[0].SumA, pages[0].SumD)
	}
	if Pages(nil) != nil && len(Pages(nil)) != 0 {
		t.Error("no rows, no pages")
	}
}

func TestEstimateOffsetsQuotesAndReducesTax(t *testing.T) {
	// 10 000 gain on shares, 4 000 loss on shares: offset in full → 6 000 → tax 1 800.
	e := Estimate{GainSetOff: 10000, LossFull: 4000}.Compute(0)
	near(t, "net", e.NetK4, 6000)
	near(t, "tax", e.Tax, 1800)
	// 1 960 kr loss only: 70% = 1 372, reduction 30% = 411.6 (Skatteverket's example gives 411).
	e = Estimate{LossFull: 1960}.Compute(0)
	near(t, "deductible", e.LossDeductible, 1372)
	near(t, "reduction", e.TaxReduction, 411.6)
	// Losses beyond the gains count at 70%.
	e = Estimate{GainSetOff: 1000, LossFull: 3000}.Compute(0)
	near(t, "deductible", e.LossDeductible, 1000+0.7*2000)
	// Bond-fund and crypto losses are never set off in full.
	e = Estimate{GainSetOff: 1000, LossQuoted: 1000}.Compute(0)
	near(t, "quoted", e.LossDeductible, 700)
	// Full losses cannot offset crypto gains in full.
	e = Estimate{GainOther: 1000, LossFull: 1000}.Compute(0)
	near(t, "crypto gain", e.NetK4, 300)
	// Deficit above 100 000 kr: 30 000 + 21% of the excess.
	e = Estimate{LossFull: 200000}.Compute(10000)
	near(t, "deficit", e.Deficit, 130000)
	near(t, "reduction", e.TaxReduction, 30000+0.21*30000)
	e = Estimate{LossFull: 100000 / 0.7}.Compute(0)
	near(t, "reduction at the limit", e.TaxReduction, 30000)
	// Other income is added before tax.
	e = Estimate{GainSetOff: 1000}.Compute(500)
	near(t, "tax", e.Tax, 450)
}

func TestSumRowsSeparatesSectionsAndClasses(t *testing.T) {
	tot := SumRows([]Row{
		{Class: ClassStock, Gain: 1000}, {Class: ClassEquityFund, Loss: 400},
		{Class: ClassInterestFnd, Loss: 100}, {Class: ClassCrypto, Gain: 50}, {Class: ClassUnlisted, Loss: 20},
	})
	if tot.GainA != 1000 || tot.LossA != 500 || tot.GainD != 50 || tot.LossD != 20 {
		t.Errorf("%+v", tot)
	}
	near(t, "estimate net", tot.Estimate.NetK4, 1050-400-0.7*120)
}

func TestGuessClass(t *testing.T) {
	for name, want := range map[string]Class{
		"Volvo B": ClassStock, "Handelsbanken Global Index": ClassEquityFund, "iShares Core MSCI World ETF": ClassEquityFund,
		"SEB Korträntefond": ClassInterestFnd, "Swedbank Robur Ränta": ClassInterestFnd, "Spiltan Aktiefond Investmentbolag": ClassEquityFund,
		"Balanced allocation fund": ClassMixedFund, "Investor B": ClassStock,
	} {
		if got := GuessClass(name, ""); got != want {
			t.Errorf("%q → %s, want %s", name, got, want)
		}
	}
}

func TestOverridesWinOverGuesses(t *testing.T) {
	over, problems := ParseOverrides("SEVOLVO=aktiefond\nInvestor B = ranfond\n# comment\nbroken\nX=bogus")
	if len(problems) != 2 {
		t.Errorf("problems %v", problems)
	}
	res := Replay([]Tx{buy("2020-01-01", "Investor B", 1, 100, 0), buy("2020-01-01", "VOLVO", 1, 100, 0)}, Options{Overrides: over})
	got := map[string]Class{}
	for _, h := range res.Holdings {
		got[h.Name] = h.Class
	}
	if got["Investor B"] != ClassInterestFnd || got["VOLVO"] != ClassEquityFund {
		t.Errorf("%v", got)
	}
}

func TestSectionDKeepsDecimalQuantities(t *testing.T) {
	rows := Rows([]Sale{{Date: d("2025-06-01"), Name: "Coin", Key: "c", Class: ClassCrypto, Qty: 0.25, Proceeds: 300, Cost: 100}}, 2025, false)
	if len(rows[0].Notes) != 0 || rows[0].QtyExact != 0.25 {
		t.Errorf("%+v", rows[0])
	}
	if qtyText(rows[0]) != "0.25" {
		t.Errorf("%q", qtyText(rows[0]))
	}
}
