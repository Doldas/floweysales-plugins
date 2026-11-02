package main

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

const epsilon = 1e-9

// Method is how the cost of a sale was determined.
type Method string

const (
	MethodAverage  Method = "genomsnitt"
	MethodFlatRate Method = "schablon"
)

// Sale is one disposal with its cost basis.
type Sale struct {
	Date     time.Time
	Account  string
	Name     string
	ISIN     string
	Key      string
	Class    Class
	Qty      float64
	Proceeds float64 // price × quantity minus the fee
	Fee      float64
	Average  float64 // cost by the average method
	FlatRate float64 // 20% of Proceeds
	Cost     float64 // the cost used
	Method   Method
	Planned  bool
	Notes    []string // problems that make this sale uncertain
}

// Gain is positive for a profit and negative for a loss, before rounding.
func (s Sale) Gain() float64 { return s.Proceeds - s.Cost }

// Holding is what remains of an instrument.
type Holding struct {
	Key, Name, ISIN string
	Class           Class
	Qty             float64
	Cost            float64
	CostUnknown     bool // shares arrived by transfer without a cost
}

// AvgCost is the average cost per unit (genomsnittligt omkostnadsbelopp).
func (h Holding) AvgCost() float64 {
	if h.Qty <= epsilon {
		return 0
	}
	return h.Cost / h.Qty
}

// CostMethod picks how a sale's cost is chosen.
type CostMethod string

const (
	// BestMethod takes the larger of the average cost and, where allowed, 20%.
	BestMethod CostMethod = "best"
	// AverageOnly never uses the flat rate.
	AverageOnly CostMethod = "average"
)

// Options configure a calculation.
type Options struct {
	Method    CostMethod
	Overrides map[string]Class // folded name or ISIN → class
}

// Result is the outcome of replaying all transactions.
type Result struct {
	Sales    []Sale
	Holdings []Holding
	Warnings []string
	Classes  map[string]Class // key → class used
	Guessed  map[string]bool  // key → class came from the name heuristic
}

type pool struct {
	h Holding
}

// Replay applies the transactions in date order and returns every sale with
// its cost basis (genomsnittsmetoden) and the holdings left at the end.
// Transactions of the same day apply additions before disposals.
func Replay(txs []Tx, opts Options) Result {
	res := Result{Classes: map[string]Class{}, Guessed: map[string]bool{}}
	ordered := append([]Tx(nil), txs...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if !ordered[i].Date.Equal(ordered[j].Date) {
			return ordered[i].Date.Before(ordered[j].Date)
		}
		return ordered[i].Kind.incoming() && !ordered[j].Kind.incoming()
	})
	// A class set on any row of an instrument applies to all its rows.
	for _, tx := range ordered {
		if tx.Class != "" {
			res.Classes[tx.Key()] = tx.Class
		}
	}
	pools := map[string]*pool{}
	var order []string
	warned := map[string]bool{}
	warn := func(key, format string, args ...any) {
		msg := fmt.Sprintf(format, args...)
		if !warned[key+msg] && len(res.Warnings) < 60 {
			warned[key+msg] = true
			res.Warnings = append(res.Warnings, msg)
		}
	}
	for _, tx := range ordered {
		key := tx.Key()
		class := classFor(key, tx, opts, &res)
		p := pools[key]
		if p == nil {
			p = &pool{h: Holding{Key: key, Name: tx.Name, ISIN: tx.ISIN, Class: class}}
			pools[key] = p
			order = append(order, key)
		}
		if p.h.Name == "" {
			p.h.Name = tx.Name
		}
		if p.h.ISIN == "" {
			p.h.ISIN = tx.ISIN
		}
		p.h.Class = class
		switch tx.Kind {
		case KindBuy:
			p.h.Qty += tx.Qty
			p.h.Cost += tx.Gross + tx.Fee
		case KindOpening:
			p.h.Qty += tx.Qty
			p.h.Cost += tx.Gross + tx.Fee
		case KindTransferIn:
			p.h.Qty += tx.Qty
			p.h.CostUnknown = true
			warn(key, "%s: %s shares arrived by transfer (%s) without a known cost. Add an opening balance (type 'opening' in the generic CSV) with their real cost, or the gain on later sales is overstated.", label(tx), formatQty(tx.Qty), tx.Date.Format("2006-01-02"))
		case KindAdjust:
			p.h.Qty += tx.Qty // split: more or fewer shares, the same total cost
			if p.h.Qty < epsilon {
				p.h.Qty, p.h.Cost = 0, 0
			}
		case KindTransferOut:
			take := math.Min(tx.Qty, p.h.Qty)
			if p.h.Qty > epsilon {
				p.h.Cost -= p.h.Cost * take / p.h.Qty
			}
			p.h.Qty -= take
			if tx.Qty > take+epsilon {
				warn(key, "%s: transferred out more shares than were held.", label(tx))
			}
		case KindSell:
			sale := Sale{Date: tx.Date, Account: tx.Account, Name: p.h.Name, ISIN: p.h.ISIN, Key: key, Class: class, Qty: tx.Qty,
				Proceeds: tx.Gross - tx.Fee, Fee: tx.Fee, Planned: tx.Planned}
			held := math.Min(tx.Qty, p.h.Qty)
			if p.h.Qty > epsilon {
				sale.Average = p.h.Cost * held / p.h.Qty
			}
			if tx.Qty > held+epsilon {
				sale.Notes = append(sale.Notes, fmt.Sprintf("sold %s but only %s were found in the history", formatQty(tx.Qty), formatQty(held)))
				warn(key, "%s: sold %s %s but the files only show %s held on %s. Include the whole purchase history, including earlier years.", label(tx), formatQty(tx.Qty), p.h.Name, formatQty(held), tx.Date.Format("2006-01-02"))
			}
			if p.h.CostUnknown {
				sale.Notes = append(sale.Notes, "part of the holding arrived by transfer without a known cost")
			}
			sale.FlatRate = 0.2 * sale.Proceeds
			sale.Cost, sale.Method = sale.Average, MethodAverage
			if opts.Method != AverageOnly && class.FlatRateAllowed() && sale.FlatRate > sale.Average+epsilon {
				sale.Cost, sale.Method = sale.FlatRate, MethodFlatRate
			}
			// The pool always falls by the average cost, whichever method the sale used.
			p.h.Cost -= sale.Average
			p.h.Qty -= held
			if p.h.Qty < epsilon {
				p.h.Qty, p.h.Cost = 0, 0
			}
			res.Sales = append(res.Sales, sale)
		}
	}
	for _, key := range order {
		h := pools[key].h
		if h.Qty > epsilon {
			res.Holdings = append(res.Holdings, h)
		}
	}
	sort.Slice(res.Holdings, func(i, j int) bool {
		return strings.ToLower(res.Holdings[i].Name) < strings.ToLower(res.Holdings[j].Name)
	})
	return res
}

func label(tx Tx) string {
	if tx.Source != "" {
		return tx.Source
	}
	return tx.Date.Format("2006-01-02")
}

func formatQty(q float64) string {
	if math.Abs(q-math.Round(q)) < 1e-6 {
		return fmt.Sprintf("%.0f", q)
	}
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.4f", q), "0"), ".")
}

// classFor resolves an instrument's class: an override from the user, a class
// stated in the file, else a guess from the name.
func classFor(key string, tx Tx, opts Options, res *Result) Class {
	for _, k := range []string{tx.ISIN, "name:" + foldName(tx.Name), foldName(tx.Name)} {
		if k == "" {
			continue
		}
		if c, ok := opts.Overrides[strings.ToLower(k)]; ok {
			res.Classes[key] = c
			return c
		}
	}
	if c, ok := res.Classes[key]; ok {
		return c
	}
	c := GuessClass(tx.Name, tx.ISIN)
	res.Classes[key] = c
	res.Guessed[key] = true
	return c
}

// GuessClass makes a first guess from the instrument's name. It is only a
// default: the review tool lists every guess and overrides replace it.
func GuessClass(name, isin string) Class {
	n := strings.ToLower(name)
	has := func(words ...string) bool {
		for _, w := range words {
			if strings.Contains(n, w) {
				return true
			}
		}
		return false
	}
	switch {
	case has("ränt", "rante", "ranta", "obligation", "bond", "likviditet", "kreditfond", "high yield", "korträntefond"):
		return ClassInterestFnd
	case has("blandfond", "balanced", "allocation", "multi asset", "multi-asset"):
		return ClassMixedFund
	case has(" fond", "fond ", "etf", "xact", "ishares", "vanguard", "spdr", "index", "ucits", "robur", "lannebo", "spiltan", "fund", "amundi", "xtrackers", "wisdomtree", "invesco", "global select"):
		return ClassEquityFund
	}
	return ClassStock
}

// ParseOverrides reads lines such as "SE0000108656=aktiefond" or
// "Handelsbanken Global Index=aktiefond"; keys are matched case-insensitively
// by ISIN or by exact name.
func ParseOverrides(text string) (map[string]Class, []string) {
	out := map[string]Class{}
	var problems []string
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		cut := strings.LastIndexAny(line, "=;:")
		if cut <= 0 {
			problems = append(problems, fmt.Sprintf("override line %d needs 'ISIN-or-name=class'", i+1))
			continue
		}
		key, val := strings.TrimSpace(line[:cut]), strings.TrimSpace(line[cut+1:])
		c, ok := parseClass(val)
		if !ok || key == "" {
			problems = append(problems, fmt.Sprintf("override line %d: %q is not a class (use %s)", i+1, val, classNames()))
			continue
		}
		out[strings.ToLower(key)] = c
		out["name:"+foldName(key)] = c
		out[foldName(key)] = c
	}
	return out, problems
}

// Row is one K4 row: all sales of one security in the tax year.
type Row struct {
	Name, ISIN, Key string
	Class           Class
	Qty             int64   // rounded, as the form wants whole units
	QtyExact        float64 // before rounding
	Proceeds, Cost  int64   // whole kronor
	Gain, Loss      int64   // whole kronor; at most one is non-zero
	FlatRateSales   int
	AverageSales    int
	Notes           []string
}

// Rows groups the sales of the tax year per security, in whole kronor.
// Planned sales are included only when includePlanned is set.
func Rows(sales []Sale, year int, includePlanned bool) []Row {
	byKey := map[string]*Row{}
	exact := map[string][3]float64{}
	var order []string
	for _, s := range sales {
		if s.Date.Year() != year || (s.Planned && !includePlanned) {
			continue
		}
		r := byKey[s.Key]
		if r == nil {
			r = &Row{Name: s.Name, ISIN: s.ISIN, Key: s.Key, Class: s.Class}
			byKey[s.Key] = r
			order = append(order, s.Key)
		}
		e := exact[s.Key]
		e[0] += s.Qty
		e[1] += s.Proceeds
		e[2] += s.Cost
		exact[s.Key] = e
		if s.Method == MethodFlatRate {
			r.FlatRateSales++
		} else {
			r.AverageSales++
		}
		for _, n := range s.Notes {
			if !contains(r.Notes, n) {
				r.Notes = append(r.Notes, n)
			}
		}
	}
	rows := make([]Row, 0, len(order))
	for _, key := range order {
		r := byKey[key]
		e := exact[key]
		r.QtyExact = e[0]
		r.Qty = int64(math.Round(e[0]))
		r.Proceeds, r.Cost = int64(math.Round(e[1])), int64(math.Round(e[2]))
		if d := r.Proceeds - r.Cost; d > 0 {
			r.Gain = d
		} else {
			r.Loss = -d
		}
		if r.Class.Section() == "A" && math.Abs(e[0]-float64(r.Qty)) > 1e-6 { // section D takes decimals
			r.Notes = append(r.Notes, fmt.Sprintf("quantity %s was rounded to %d because the K4 form takes whole units", formatQty(e[0]), r.Qty))
		}
		rows = append(rows, *r)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Class.Section() != rows[j].Class.Section() {
			return rows[i].Class.Section() < rows[j].Class.Section()
		}
		return strings.ToLower(rows[i].Name) < strings.ToLower(rows[j].Name)
	})
	return rows
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
