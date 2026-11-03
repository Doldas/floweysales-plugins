package main

import (
	"time"

	sdk "github.com/Doldas/floweysales/pluginsdk"
)

// buildManifest is the single source of the plugin's description; plugin.json
// is generated from it (UPDATE_MANIFEST=1 go test ./se-tax) and a test fails
// when the two drift apart.
func buildManifest() sdk.Manifest {
	t := &taxPlugin{now: time.Now}
	num := func(name, label, help string) sdk.ToolInput {
		return sdk.ToolInput{Name: name, Label: label, Type: sdk.InputNumber, Help: help}
	}
	return sdk.Manifest{
		Protocol: sdk.ProtocolVersion, ID: "se-tax", Name: "Swedish tax (Skatteverket)", Version: "1.1.0",
		Description: "A Tax tab for Swedish investors: turns Avanza or generic CSV transactions into the K4 attachment for selling shares and funds, with average cost and the 20% flat rate, an Excel export, holdings, a what-if sale planner and a tax estimate.",
		Publisher:   "FloweySales", Homepage: "https://github.com/Doldas/floweysales-plugins/tree/master/se-tax", License: "MIT",
		// A tax domain provider: the same calculations as the Tax tab, for the app and the AI agent.
		Provider: sdk.ProviderInfo{ID: "SE_TAX", Label: "Swedish tax (Skatteverket)", Type: sdk.TypeTax,
			Capabilities: []string{sdk.CapTaxReport, sdk.CapTaxHoldings, sdk.CapTaxEstimate},
			Notes:        "Calculations from transactions you supply (Avanza export or generic CSV); nothing is stored. Not tax advice.",
			Operations: []sdk.DomainOperation{
				{Capability: sdk.CapTaxReport, Description: "Sales of an income year per security with cost basis (genomsnittsmetoden or the 20% flat rate), K4 section totals and a simplified tax estimate.",
					Inputs: append([]sdk.DomainField{{Name: "year", Type: sdk.FieldInteger, Description: "Income year, 2025 or 2026", Required: true}},
						append(transactionFields(), sdk.DomainField{Name: "other_income", Type: sdk.FieldNumber, Description: "Other capital income in SEK for the estimate (interest and dividends minus interest costs)"})...)},
				{Capability: sdk.CapTaxHoldings, Description: "What is held at a date, with total and average cost per unit.",
					Inputs: append([]sdk.DomainField{{Name: "as_of", Type: sdk.FieldString, Description: "Date YYYY-MM-DD; default today"}}, transactionFields()...)},
				{Capability: sdk.CapTaxEstimate, Description: "Tax on gains and losses you already know: losses on listed shares offset gains in full, other losses count at 70%, tax is 30%, a deficit gives a tax reduction.",
					Inputs: []sdk.DomainField{
						{Name: "gain_listed", Type: sdk.FieldNumber, Description: "Gains on listed shares and equity/mixed funds, SEK"},
						{Name: "loss_listed", Type: sdk.FieldNumber, Description: "Losses on listed shares and equity/mixed funds, SEK"},
						{Name: "gain_unlisted", Type: sdk.FieldNumber, Description: "Gains on unlisted shares and bond funds, SEK"},
						{Name: "loss_interest_funds", Type: sdk.FieldNumber, Description: "Losses on bond funds, SEK (count at 70%)"},
						{Name: "loss_unlisted", Type: sdk.FieldNumber, Description: "Losses on unlisted shares, SEK (count at 70%)"},
						{Name: "gain_crypto", Type: sdk.FieldNumber, Description: "Gains on crypto, SEK"},
						{Name: "loss_crypto", Type: sdk.FieldNumber, Description: "Losses on crypto, SEK (count at 70%)"},
						{Name: "other_income", Type: sdk.FieldNumber, Description: "Other capital income, SEK; may be negative"},
					}},
			}},
		Tools: &sdk.ToolsInfo{
			Tab: sdk.ToolsTab{Label: "Tax", Icon: "bi-receipt",
				Description: "Swedish capital gains tax on shares and funds: fill in the K4 attachment for your income tax return, or export it to Excel. Your files are processed in memory and are not stored."},
			Tools: []sdk.Tool{
				{ID: "k4", Label: "K4 report", Submit: "Calculate K4", Inputs: t.k4Inputs(),
					Description: "Upload your transactions and get every K4 row (antal, beteckning, försäljningspris, omkostnadsbelopp, vinst, förlust), the totals for boxes 7.4/8.3, a tax estimate, and an Excel workbook laid out like the form."},
				{ID: "holdings", Label: "Holdings & cost basis", Submit: "Show holdings", Inputs: append(fileInputs(),
					sdk.ToolInput{Name: "as_of", Label: "As of date", Type: sdk.InputDate, Help: "Defaults to today. Use 31 December for the year-end position."}),
					Description: "Your remaining holdings with total and average cost per unit (genomsnittligt omkostnadsbelopp), as a table and an Excel sheet. Exports an opening-balance file so next year you do not need the old history."},
				{ID: "planner", Label: "What if I sell?", Submit: "Compare", Inputs: append(append([]sdk.ToolInput{yearInput()}, fileInputs()...), methodInput(),
					sdk.ToolInput{Name: "planned", Label: "Planned sales", Type: sdk.InputTextarea, Required: true, Placeholder: "Volvo B; 50; 245.50; 39\nSE0000115446; 10; 250",
						Help: "One per line: name or ISIN; quantity; price per unit in SEK; fee in SEK (optional)."},
					num("other_income", "Other capital income, SEK (optional)", "Interest and dividends minus interest costs, for the estimate.")),
					Description: "Try selling before year-end: see the cost basis, the gain and how much the year's tax changes, including the effect of losses."},
				{ID: "estimate", Label: "Tax estimate", Submit: "Estimate", Inputs: []sdk.ToolInput{
					num("gain_listed", "Gains on listed shares and equity/mixed funds, SEK", ""),
					num("loss_listed", "Losses on listed shares and equity/mixed funds, SEK", "Offset gains in full."),
					num("gain_unlisted", "Gains on unlisted shares and bond funds, SEK", ""),
					num("loss_interest_funds", "Losses on bond funds, SEK", "Count at 70%."),
					num("loss_unlisted", "Losses on unlisted shares, SEK", "Count at 70%."),
					num("gain_crypto", "Gains on crypto, SEK", ""),
					num("loss_crypto", "Losses on crypto, SEK", "Count at 70%."),
					num("other_income", "Other capital income, SEK", "Interest and dividends minus interest costs; may be negative."),
				}, Description: "A quick calculator from totals: the 30% tax, the 70% rule for losses and the tax reduction on a deficit."},
				{ID: "review", Label: "Review import", Submit: "Review files", Inputs: fileInputs(),
					Description: "Check what was read from your files: accounts (so you can leave out ISK and pension accounts), transaction types and the type guessed for each instrument."},
				{ID: "guide", Label: "Guide & rules", Submit: "Show guide", Description: "How K4 works, the rules this plugin applies, what it does not cover, and a CSV template for other brokers."},
			},
		},
	}
}

// transactionFields are the inputs shared by the operations that read transactions.
func transactionFields() []sdk.DomainField {
	return []sdk.DomainField{
		{Name: "csv", Type: sdk.FieldString, Required: true, Description: "The whole transaction history as CSV text: Avanza's transaction export or the generic CSV. Include earlier years so the average cost is right. Never invent transactions."},
		{Name: "format", Type: sdk.FieldString, Description: "File format, default auto", Enum: []string{"auto", "avanza", "generic"}},
		{Name: "exclude_accounts", Type: sdk.FieldString, Description: "Account numbers to leave out, comma separated (ISK, kapitalförsäkring, IPS and pension accounts are not declared on K4)"},
		{Name: "classes", Type: sdk.FieldString, Description: "Instrument type overrides, one 'ISIN-or-name=type' per line (aktie, aktiefond, blandfond, ranfond, krypto, onoterad)"},
		{Name: "method", Type: sdk.FieldString, Description: "Cost basis: best (default) or average only", Enum: []string{"best", "average"}},
	}
}
