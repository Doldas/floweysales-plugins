package main

import (
	"strings"
	"time"
)

// Class is the tax class of an instrument. It decides the K4 section, whether
// the 20% flat-rate method is allowed and how a loss is treated.
type Class string

const (
	ClassStock       Class = "aktie"     // marknadsnoterad aktie
	ClassEquityFund  Class = "aktiefond" // aktiefond or equity ETF
	ClassMixedFund   Class = "blandfond"
	ClassInterestFnd Class = "ranfond" // räntefond: losses are only 70% deductible
	ClassCrypto      Class = "krypto"  // K4 section D
	ClassUnlisted    Class = "onoterad"
)

var allClasses = []Class{ClassStock, ClassEquityFund, ClassMixedFund, ClassInterestFnd, ClassCrypto, ClassUnlisted}

func parseClass(s string) (Class, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "aktie", "aktier", "stock", "share":
		return ClassStock, true
	case "aktiefond", "fond", "etf", "equity_fund":
		return ClassEquityFund, true
	case "blandfond", "mixed_fund":
		return ClassMixedFund, true
	case "ranfond", "räntefond", "rantefond", "bond_fund":
		return ClassInterestFnd, true
	case "krypto", "crypto":
		return ClassCrypto, true
	case "onoterad", "onoterade", "unlisted":
		return ClassUnlisted, true
	}
	return "", false
}

// Section is the K4 section the class is reported in: "A" or "D".
func (c Class) Section() string {
	if c == ClassCrypto || c == ClassUnlisted {
		return "D"
	}
	return "A"
}

// FullLossDeduction reports whether a loss on the class can be set off in full
// against gains: marknadsnoterade delägarrätter other than räntefonder.
func (c Class) FullLossDeduction() bool {
	return c == ClassStock || c == ClassEquityFund || c == ClassMixedFund
}

// FlatRateAllowed reports whether the 20% method (schablonmetoden) may be
// used. Skatteverket allows it for marknadsnoterade aktier; this plugin does
// not apply it to funds.
func (c Class) FlatRateAllowed() bool { return c == ClassStock }

// Label is the Swedish name shown in tables.
func (c Class) Label() string {
	switch c {
	case ClassStock:
		return "Aktie"
	case ClassEquityFund:
		return "Aktiefond/ETF"
	case ClassMixedFund:
		return "Blandfond"
	case ClassInterestFnd:
		return "Räntefond"
	case ClassCrypto:
		return "Krypto"
	case ClassUnlisted:
		return "Onoterad"
	}
	return string(c)
}

// Kind is what a transaction does to a holding.
type Kind int

const (
	KindBuy         Kind = iota // adds shares and cost
	KindOpening                 // adds shares at a stated total cost (carried over from earlier years)
	KindTransferIn              // shares arrive without a known cost
	KindAdjust                  // split, reverse split, bonus issue: shares change, total cost does not
	KindSell                    // a taxable disposal
	KindTransferOut             // shares leave to another custodian: not a sale
)

func (k Kind) incoming() bool {
	return k == KindBuy || k == KindOpening || k == KindTransferIn || k == KindAdjust
}

// Tx is one normalised transaction. Amounts are in SEK.
type Tx struct {
	Date    time.Time
	Account string
	Kind    Kind
	Name    string
	ISIN    string
	Qty     float64 // positive; for KindAdjust the signed change in shares
	Gross   float64 // price × quantity, before fees (KindOpening: total cost)
	Fee     float64
	Class   Class
	Source  string // "file.csv:12" for messages
	Planned bool   // a hypothetical sale from the planner
}

// Key identifies the instrument: the ISIN when known, else the folded name.
func (t Tx) Key() string {
	if t.ISIN != "" {
		return t.ISIN
	}
	return "name:" + foldName(t.Name)
}

func foldName(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }
