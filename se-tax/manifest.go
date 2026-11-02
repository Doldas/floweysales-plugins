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
		Protocol: sdk.ProtocolVersion, ID: "se-tax", Name: "Swedish tax (Skatteverket)", Version: "1.0.0",
		Description: "A Tax tab for Swedish investors: turns Avanza or generic CSV transactions into the K4 attachment for selling shares and funds, with average cost and the 20% flat rate, an Excel export, holdings, a what-if sale planner and a tax estimate.",
		Publisher:   "FloweySales", Homepage: "https://github.com/Doldas/floweysales-plugins/tree/master/se-tax", License: "MIT",
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
