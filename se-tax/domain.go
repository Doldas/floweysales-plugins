package main

import (
	"context"
	"encoding/json"
	"math"
	"strings"

	sdk "github.com/Doldas/floweysales/pluginsdk"
)

// The domain operations give the same calculations as the Tax tab, as plain
// JSON for the app and the AI agent. Transactions arrive as CSV text (the
// Avanza export or the generic CSV); nothing is stored.

const (
	disclaimer = "A calculation from the transactions supplied, not tax advice. Skatteverket decides. Check account types (ISK, kapitalförsäkring, IPS and pension accounts are not declared on K4) and the detected instrument types."
	// maxReportRows keeps a result well under the 1 MB limit.
	maxReportRows = 1500
)

func (t *taxPlugin) domain(_ context.Context, p sdk.DomainParams) (any, error) {
	switch p.Operation {
	case sdk.CapTaxReport:
		return t.taxReport(p.Input)
	case sdk.CapTaxHoldings:
		return t.taxHoldings(p.Input)
	case sdk.CapTaxEstimate:
		return t.taxEstimate(p.Input)
	}
	return nil, sdk.Errorf(sdk.CodeInvalidArgument, "unknown operation %q", p.Operation)
}

type transactionInput struct {
	CSV             string `json:"csv"`
	Format          string `json:"format"`
	ExcludeAccounts string `json:"exclude_accounts"`
	Classes         string `json:"classes"`
	Method          string `json:"method"`
}

func (in transactionInput) load() (*loaded, error) {
	files := splitFiles(in.CSV)
	if len(files) == 0 {
		return nil, invalid("no transactions given: save your broker export under Tax → Saved transactions, or pass it as csv")
	}
	return loadFiles(files, in.Format, in.ExcludeAccounts, in.Classes, in.Method)
}

// splitFiles reads a csv input that may hold several files, each introduced by
// a "=== file: NAME ===" line (how the backend joins saved files). Text
// without such a line is one file.
func splitFiles(csv string) []InputFile {
	if strings.TrimSpace(csv) == "" {
		return nil
	}
	var files []InputFile
	var cur *InputFile
	var body strings.Builder
	flush := func() {
		if cur != nil {
			cur.Content = body.String()
			files = append(files, *cur)
			body.Reset()
		}
	}
	for _, line := range strings.SplitAfter(csv, "\n") {
		if name, ok := strings.CutPrefix(strings.TrimRight(line, "\r\n"), sdk.DomainFileMarker); ok && strings.HasSuffix(name, " ===") {
			flush()
			cur = &InputFile{Name: strings.TrimSuffix(name, " ===")}
			continue
		}
		if cur == nil {
			cur = &InputFile{Name: "transactions.csv"}
		}
		body.WriteString(line)
	}
	flush()
	return files
}

func decodeInput[T any](raw json.RawMessage) (T, error) {
	var v T
	if len(raw) == 0 {
		return v, nil
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return v, invalid("invalid input: %v", err)
	}
	return v, nil
}

func sek(v float64) float64 { return math.Round(v*100) / 100 }

type reportRow struct {
	Name          string   `json:"name"`
	ISIN          string   `json:"isin,omitempty"`
	Type          string   `json:"type"`
	Section       string   `json:"k4_section"`
	Quantity      int64    `json:"quantity"`
	ProceedsSEK   int64    `json:"proceeds_sek"`
	CostSEK       int64    `json:"cost_sek"`
	GainSEK       int64    `json:"gain_sek"`
	LossSEK       int64    `json:"loss_sek"`
	FlatRateSales int      `json:"flat_rate_sales"`
	AverageSales  int      `json:"average_sales"`
	Notes         []string `json:"notes,omitempty"`
}

func estimateJSON(e Estimate) map[string]any {
	return map[string]any{
		"gains_sek": sek(e.GainSetOff + e.GainOther), "losses_set_off_in_full_sek": sek(e.LossFullOffset), "losses_at_70_percent_sek": sek(e.LossQuotedTotal),
		"result_from_k4_sek": sek(e.NetK4), "other_capital_income_sek": sek(e.OtherIncome), "capital_income_sek": sek(e.CapitalIncome),
		"tax_30_percent_sek": sek(e.Tax), "deficit_sek": sek(e.Deficit), "tax_reduction_sek": sek(e.TaxReduction), "net_tax_effect_sek": sek(e.NetEffect()),
	}
}

func (t *taxPlugin) taxReport(raw json.RawMessage) (any, error) {
	in, err := decodeInput[struct {
		transactionInput
		Year        int     `json:"year"`
		OtherIncome float64 `json:"other_income"`
	}](raw)
	if err != nil {
		return nil, err
	}
	supported := false
	for _, y := range supportedYears {
		supported = supported || y == in.Year
	}
	if !supported {
		return nil, invalid("income year %d is not supported; supported years: %v", in.Year, supportedYears)
	}
	l, err := in.transactionInput.load()
	if err != nil {
		return nil, err
	}
	rows := Rows(l.result.Sales, in.Year, false)
	totals := SumRows(rows)
	est := totals.Estimate.Compute(in.OtherIncome)
	out := make([]reportRow, 0, len(rows))
	var proceeds, cost int64
	for _, r := range rows {
		proceeds += r.Proceeds
		cost += r.Cost
		out = append(out, reportRow{Name: r.Name, ISIN: r.ISIN, Type: r.Class.Label(), Section: r.Class.Section(), Quantity: r.Qty,
			ProceedsSEK: r.Proceeds, CostSEK: r.Cost, GainSEK: r.Gain, LossSEK: r.Loss, FlatRateSales: r.FlatRateSales, AverageSales: r.AverageSales, Notes: r.Notes})
	}
	truncated := len(out) > maxReportRows
	if truncated {
		out = out[:maxReportRows]
	}
	return map[string]any{
		"year": in.Year, "securities_sold": len(rows), "rows": out, "rows_truncated": truncated,
		"totals": map[string]any{
			"proceeds_sek": proceeds, "cost_sek": cost,
			"section_a_gain_sek": totals.GainA, "section_a_loss_sek": totals.LossA, "section_d_gain_sek": totals.GainD, "section_d_loss_sek": totals.LossD,
			"result_sek": totals.GainA + totals.GainD - totals.LossA - totals.LossD,
		},
		"estimate":    estimateJSON(est),
		"trades_read": len(l.txs), "first_trade": l.report.First.Format("2006-01-02"), "last_trade": l.report.Last.Format("2006-01-02"),
		"warnings": l.warnings(), "disclaimer": disclaimer,
	}, nil
}

func (t *taxPlugin) taxHoldings(raw json.RawMessage) (any, error) {
	in, err := decodeInput[struct {
		transactionInput
		AsOf string `json:"as_of"`
	}](raw)
	if err != nil {
		return nil, err
	}
	l, err := in.transactionInput.load()
	if err != nil {
		return nil, err
	}
	asOf := t.now()
	if in.AsOf != "" {
		d, ok := parseDate(in.AsOf)
		if !ok {
			return nil, invalid("as_of must be YYYY-MM-DD")
		}
		asOf = d
		var upTo []Tx
		for _, tx := range l.txs {
			if !tx.Date.After(d) {
				upTo = append(upTo, tx)
			}
		}
		l.result = Replay(upTo, l.opts)
	}
	type position struct {
		Name        string  `json:"name"`
		ISIN        string  `json:"isin,omitempty"`
		Type        string  `json:"type"`
		Quantity    float64 `json:"quantity"`
		TotalCost   float64 `json:"total_cost_sek"`
		AverageCost float64 `json:"average_cost_sek"`
		CostUnknown bool    `json:"cost_unknown,omitempty"`
	}
	positions := []position{}
	var total float64
	for _, h := range l.result.Holdings {
		positions = append(positions, position{Name: h.Name, ISIN: h.ISIN, Type: h.Class.Label(), Quantity: h.Qty, TotalCost: sek(h.Cost), AverageCost: sek(h.AvgCost()), CostUnknown: h.CostUnknown})
		total += h.Cost
	}
	return map[string]any{"as_of": asOf.Format("2006-01-02"), "positions": positions, "total_cost_sek": sek(total), "warnings": l.warnings(), "disclaimer": disclaimer}, nil
}

func (t *taxPlugin) taxEstimate(raw json.RawMessage) (any, error) {
	in, err := decodeInput[struct {
		GainListed        float64 `json:"gain_listed"`
		GainUnlisted      float64 `json:"gain_unlisted"`
		GainCrypto        float64 `json:"gain_crypto"`
		LossListed        float64 `json:"loss_listed"`
		LossInterestFunds float64 `json:"loss_interest_funds"`
		LossUnlisted      float64 `json:"loss_unlisted"`
		LossCrypto        float64 `json:"loss_crypto"`
		OtherIncome       float64 `json:"other_income"`
	}](raw)
	if err != nil {
		return nil, err
	}
	pos := func(v float64) float64 { return math.Max(v, 0) }
	e := Estimate{GainSetOff: pos(in.GainListed) + pos(in.GainUnlisted), GainOther: pos(in.GainCrypto),
		LossFull: pos(in.LossListed), LossQuoted: pos(in.LossInterestFunds) + pos(in.LossUnlisted) + pos(in.LossCrypto)}
	return map[string]any{"estimate": estimateJSON(e.Compute(in.OtherIncome)), "disclaimer": disclaimer}, nil
}
