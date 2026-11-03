package main

import (
	"encoding/json"
	"strings"
	"testing"

	sdk "github.com/Doldas/floweysales/pluginsdk"
	"github.com/Doldas/floweysales/pluginsdk/plugintest"
)

func callDomain(t *testing.T, op string, input map[string]any) (map[string]any, error) {
	t.Helper()
	c := plugintest.Start(t, newPlugin(&taxPlugin{now: fixedNow}))
	raw, _ := json.Marshal(input)
	var out map[string]any
	err := c.Call(sdk.MethodDomain, sdk.DomainParams{Operation: op, Input: raw}, &out)
	return out, err
}

func TestDomainTaxReport(t *testing.T) {
	out, err := callDomain(t, sdk.CapTaxReport, map[string]any{"year": 2026, "csv": e2eCSV, "exclude_accounts": "9999999"})
	if err != nil {
		t.Fatal(err)
	}
	if out["year"] != float64(2026) || out["securities_sold"] != float64(2) {
		t.Fatalf("report = %v", out)
	}
	rows := out["rows"].([]any)
	names := map[string]bool{}
	for _, r := range rows {
		names[r.(map[string]any)["name"].(string)] = true
	}
	if !names["Volvo B"] || names["ISK Aktie"] {
		t.Fatalf("the ISK account must be left out: %v", names)
	}
	est := out["estimate"].(map[string]any)
	if _, ok := est["tax_30_percent_sek"]; !ok || !strings.Contains(out["disclaimer"].(string), "not tax advice") {
		t.Fatalf("estimate/disclaimer missing: %v", out)
	}
}

func TestDomainTaxHoldingsAndEstimate(t *testing.T) {
	out, err := callDomain(t, sdk.CapTaxHoldings, map[string]any{"csv": e2eCSV, "as_of": "2025-12-31", "exclude_accounts": "9999999"})
	if err != nil {
		t.Fatal(err)
	}
	positions := out["positions"].([]any)
	if len(positions) != 1 || positions[0].(map[string]any)["quantity"] != float64(150) {
		t.Fatalf("holdings at year end 2025 = %v", out)
	}
	est, err := callDomain(t, sdk.CapTaxEstimate, map[string]any{"gain_listed": 100000, "loss_listed": 40000})
	if err != nil {
		t.Fatal(err)
	}
	if tax := est["estimate"].(map[string]any)["tax_30_percent_sek"]; tax != float64(18000) {
		t.Fatalf("30%% of 60 000 is 18 000, got %v", tax)
	}
}

func TestDomainRefusesBadInput(t *testing.T) {
	for name, tc := range map[string]struct {
		op    string
		input map[string]any
	}{
		"no csv":           {sdk.CapTaxReport, map[string]any{"year": 2026}},
		"unsupported year": {sdk.CapTaxReport, map[string]any{"year": 1999, "csv": e2eCSV}},
		"bad date":         {sdk.CapTaxHoldings, map[string]any{"csv": e2eCSV, "as_of": "yesterday"}},
		"unknown op":       {"tax_everything", map[string]any{}},
	} {
		if _, err := callDomain(t, tc.op, tc.input); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
