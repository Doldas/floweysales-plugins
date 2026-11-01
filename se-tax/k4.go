package main

import "math"

// K4 form capacity: A.1–A.9 and D.1–D.7 per page.
const (
	rowsPerPageA = 9
	rowsPerPageD = 7
)

// Page is one K4 form: up to nine rows in section A and seven in section D,
// each section with its own sums.
type Page struct {
	Number int
	A, D   []Row
	SumA   Sums
	SumD   Sums
}

// Sums are the "Summa" fields of a section.
type Sums struct{ Proceeds, Cost, Gain, Loss int64 }

func sum(rows []Row) Sums {
	var s Sums
	for _, r := range rows {
		s.Proceeds += r.Proceeds
		s.Cost += r.Cost
		s.Gain += r.Gain
		s.Loss += r.Loss
	}
	return s
}

// Pages lays the rows out on as many K4 forms as needed.
func Pages(rows []Row) []Page {
	var a, d []Row
	for _, r := range rows {
		if r.Class.Section() == "D" {
			d = append(d, r)
		} else {
			a = append(a, r)
		}
	}
	n := int(math.Max(math.Ceil(float64(len(a))/rowsPerPageA), math.Ceil(float64(len(d))/rowsPerPageD)))
	pages := make([]Page, 0, n)
	for i := 0; i < n; i++ {
		p := Page{Number: i + 1, A: slice(a, i*rowsPerPageA, rowsPerPageA), D: slice(d, i*rowsPerPageD, rowsPerPageD)}
		p.SumA, p.SumD = sum(p.A), sum(p.D)
		pages = append(pages, p)
	}
	return pages
}

func slice(rows []Row, from, n int) []Row {
	if from >= len(rows) {
		return nil
	}
	to := from + n
	if to > len(rows) {
		to = len(rows)
	}
	return rows[from:to]
}

// Totals are the figures carried to the income tax return (INK1) and the
// estimate of what Skatteverket makes of them.
type Totals struct {
	GainA, LossA int64 // K4 section A → INK1 rutorna 7.4 and 8.3
	GainD, LossD int64 // K4 section D → INK1 rutorna 7.5 and 8.4
	Estimate     Estimate
}

// SumRows adds the rows up.
func SumRows(rows []Row) Totals {
	var t Totals
	var in Estimate
	for _, r := range rows {
		full := r.Class.FullLossDeduction()
		switch {
		case r.Class.Section() == "A":
			t.GainA += r.Gain
			t.LossA += r.Loss
		default:
			t.GainD += r.Gain
			t.LossD += r.Loss
		}
		switch {
		case full:
			in.GainSetOff += float64(r.Gain)
			in.LossFull += float64(r.Loss)
		case r.Class == ClassCrypto:
			in.GainOther += float64(r.Gain)
			in.LossQuoted += float64(r.Loss)
		default:
			in.GainSetOff += float64(r.Gain)
			in.LossQuoted += float64(r.Loss)
		}
	}
	t.Estimate = in.Compute(0)
	return t
}

// Estimate is a simplified model of the kvittning and kvotering that
// Skatteverket performs on the K4 totals. It is an estimate, not a decision.
type Estimate struct {
	GainSetOff float64 // gains that fully deductible losses can offset
	GainOther  float64 // gains they cannot (crypto)
	LossFull   float64 // losses on shares, equity and mixed funds
	LossQuoted float64 // losses that only count at 70%

	// Results of Compute.
	LossDeductible  float64 // losses that actually reduce capital income
	LossFullOffset  float64
	LossQuotedTotal float64
	NetK4           float64 // gains minus deductible losses
	OtherIncome     float64
	CapitalIncome   float64 // NetK4 + OtherIncome
	Tax             float64 // 30% of a positive capital income
	Deficit         float64 // a negative capital income, as a positive number
	TaxReduction    float64 // skattereduktion på underskott av kapital
}

// Compute applies the rules to the inputs. otherIncome is capital income
// outside K4 (interest, dividends, net of interest expenses).
func (e Estimate) Compute(otherIncome float64) Estimate {
	gains := e.GainSetOff + e.GainOther
	e.LossFullOffset = math.Min(e.LossFull, e.GainSetOff)
	remainingFull := e.LossFull - e.LossFullOffset
	e.LossQuotedTotal = (remainingFull + e.LossQuoted) * 0.7
	e.LossDeductible = e.LossFullOffset + e.LossQuotedTotal
	e.NetK4 = gains - e.LossDeductible
	e.OtherIncome = otherIncome
	e.CapitalIncome = e.NetK4 + otherIncome
	e.Tax, e.Deficit, e.TaxReduction = 0, 0, 0
	if e.CapitalIncome > 0 {
		e.Tax = 0.3 * e.CapitalIncome
	} else if e.CapitalIncome < 0 {
		e.Deficit = -e.CapitalIncome
		if e.Deficit <= 100000 {
			e.TaxReduction = 0.3 * e.Deficit
		} else {
			e.TaxReduction = 30000 + 0.21*(e.Deficit-100000)
		}
	}
	return e
}

// NetEffect is the tax paid (positive) or saved through the reduction
// (negative); useful for comparing two scenarios.
func (e Estimate) NetEffect() float64 { return e.Tax - e.TaxReduction }
