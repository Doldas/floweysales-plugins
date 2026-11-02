package main

import (
	"context"
	"encoding/base64"
	"encoding/csv"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	sdk "github.com/Doldas/floweysales/pluginsdk"
)

// supportedYears are the income years whose K4 field table has been checked
// against Skatteverket's K4 field table (SKV 2104; 2025P4 and 2026P4 are identical).
var supportedYears = []int{2025, 2026}

type taxPlugin struct{ now func() time.Time }

func (t *taxPlugin) tool(_ context.Context, p sdk.ToolParams) (sdk.ToolResult, error) {
	switch p.Tool {
	case "k4":
		return t.k4(p)
	case "holdings":
		return t.holdings(p)
	case "planner":
		return t.planner(p)
	case "estimate":
		return t.estimate(p)
	case "review":
		return t.review(p)
	case "guide":
		return t.guide(p)
	}
	return sdk.ToolResult{}, sdk.Errorf(sdk.CodeInvalidArgument, "unknown tool %q", p.Tool)
}

// ---- shared helpers ----

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

// num reads a number input; an empty or unreadable value is 0.
func num(p sdk.ToolParams, name string) float64 {
	if v, ok := p.Input[name].(float64); ok {
		return v
	}
	v, _ := parseNumber(p.String(name))
	return v
}

func invalid(format string, args ...any) error {
	return sdk.Errorf(sdk.CodeInvalidArgument, format, args...)
}

func text(s string) sdk.ToolBlock { return sdk.ToolBlock{Type: sdk.BlockText, Text: s} }
func notice(level, title, s string) sdk.ToolBlock {
	return sdk.ToolBlock{Type: sdk.BlockNotice, Level: level, Title: title, Text: s}
}

// kr formats whole kronor with a thin grouping space: 1 234 567.
func kr[T int64 | float64](v T) string {
	n := int64(math.Round(float64(v)))
	neg := n < 0
	if neg {
		n = -n
	}
	s := strconv.FormatInt(n, 10)
	var parts []string
	for len(s) > 3 {
		parts = append([]string{s[len(s)-3:]}, parts...)
		s = s[:len(s)-3]
	}
	parts = append([]string{s}, parts...)
	out := strings.Join(parts, " ")
	if neg {
		return "−" + out
	}
	return out
}

func dec(v float64, places int) string {
	return strings.Replace(strconv.FormatFloat(v, 'f', places, 64), ".", ",", 1)
}

func bullets(items []string) string {
	var b strings.Builder
	for _, it := range items {
		b.WriteString("• " + it + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// fileInputs is the form shared by the tools that read transactions.
func fileInputs() []sdk.ToolInput {
	return []sdk.ToolInput{
		{Name: "files", Label: "Transaction files", Type: sdk.InputFiles, Required: true, Accept: ".csv,.txt,.tsv",
			Help: "Avanza's transaction export (Konto → Transaktioner → Exportera) or the generic CSV from the Guide. Include the whole history, also earlier years, so the average cost is right. The files are processed in memory and not stored."},
		{Name: "format", Label: "File format", Type: sdk.InputSelect, Default: "auto", Options: []sdk.ToolOption{
			{Value: "auto", Label: "Detect automatically"}, {Value: "avanza", Label: "Avanza transaction export"}, {Value: "generic", Label: "Generic CSV"}}},
		{Name: "exclude_accounts", Label: "Accounts to leave out", Type: sdk.InputText, Placeholder: "e.g. 1234567, 7654321",
			Help: "Account numbers exactly as in the Konto column. Leave out ISK, kapitalförsäkring, IPS and pension accounts: sales there are not declared on K4. Use Review import to list the accounts."},
		{Name: "classes", Label: "Fix instrument types", Type: sdk.InputTextarea, Placeholder: "SE0000108656=aktiefond\nSome Bond Fund=ranfond",
			Help: "One 'ISIN-or-exact-name=type' per line. Types: aktie, aktiefond, blandfond, ranfond, krypto, onoterad. Unlisted instruments default to aktie; check the types in the result."},
	}
}

func yearInput() sdk.ToolInput {
	opts := make([]sdk.ToolOption, len(supportedYears))
	for i, y := range supportedYears {
		opts[i] = sdk.ToolOption{Value: strconv.Itoa(y), Label: fmt.Sprintf("%d (declared in %d)", y, y+1)}
	}
	return sdk.ToolInput{Name: "year", Label: "Income year", Type: sdk.InputSelect, Required: true, Default: strconv.Itoa(supportedYears[len(supportedYears)-1]), Options: opts}
}

func methodInput() sdk.ToolInput {
	return sdk.ToolInput{Name: "method", Label: "Cost basis", Type: sdk.InputSelect, Default: "best", Options: []sdk.ToolOption{
		{Value: "best", Label: "Most favourable per sale (average cost or 20% flat rate)"},
		{Value: "average", Label: "Average cost only (genomsnittsmetoden)"}},
		Help: "The 20% flat rate (schablonmetoden) is only used for listed shares, never for funds."}
}

func parseYear(p sdk.ToolParams) (int, error) {
	y, err := strconv.Atoi(p.String("year"))
	if err != nil {
		return 0, invalid("choose an income year")
	}
	for _, s := range supportedYears {
		if s == y {
			return y, nil
		}
	}
	return 0, invalid("income year %d is not supported yet", y)
}

type loaded struct {
	txs    []Tx
	report *ImportReport
	result Result
	opts   Options
}

// load reads the shared inputs and replays the transactions.
func load(p sdk.ToolParams) (*loaded, error) {
	files := p.Files("files")
	if len(files) == 0 {
		return nil, invalid("add at least one transaction file")
	}
	in := make([]InputFile, len(files))
	for i, f := range files {
		in[i] = InputFile{Name: f.Name, Content: f.Content}
	}
	excluded := map[string]bool{}
	for _, a := range strings.FieldsFunc(p.String("exclude_accounts"), func(r rune) bool { return r == ',' || r == ';' || r == '\n' }) {
		if a = strings.ToLower(strings.TrimSpace(a)); a != "" {
			excluded[a] = true
		}
	}
	overrides, problems := ParseOverrides(p.String("classes"))
	if len(problems) > 0 {
		return nil, invalid("%s", strings.Join(problems, "; "))
	}
	txs, report, err := Import(in, p.String("format"), excluded)
	if err != nil {
		return nil, invalid("%v", err)
	}
	if len(txs) == 0 {
		return nil, invalid("no buys or sells were found in the files (%d rows read). Use Review import to see what the files contain", report.Rows)
	}
	opts := Options{Method: BestMethod, Overrides: overrides}
	if p.String("method") == "average" {
		opts.Method = AverageOnly
	}
	return &loaded{txs: txs, report: report, result: Replay(txs, opts), opts: opts}, nil
}

func (l *loaded) warnings() []string {
	return append(append([]string(nil), l.report.Warnings...), l.result.Warnings...)
}

func accountsTable(r *ImportReport) sdk.ToolBlock {
	accs := make([]string, 0, len(r.Accounts))
	for a := range r.Accounts {
		accs = append(accs, a)
	}
	sort.Strings(accs)
	b := sdk.ToolBlock{Type: sdk.BlockTable, Title: "Accounts included in this result",
		Columns: []sdk.ToolColumn{{Label: "Account"}, {Label: "Trades read", Align: "right"}}}
	for _, a := range accs {
		name := a
		if name == "" {
			name = "(no account column)"
		}
		b.Rows = append(b.Rows, []string{name, strconv.Itoa(r.Accounts[a])})
	}
	return b
}

func csvFile(name, desc string, records [][]string) *sdk.ToolOutput {
	var b strings.Builder
	b.WriteString("\ufeff")
	w := csv.NewWriter(&b)
	w.Comma = ';'
	_ = w.WriteAll(records)
	return &sdk.ToolOutput{Name: name, MIME: "text/csv", Encoding: "utf8", Content: b.String(), Description: desc}
}

// ---- K4 ----

func (t *taxPlugin) k4Inputs() []sdk.ToolInput {
	in := append([]sdk.ToolInput{yearInput()}, fileInputs()...)
	in = append(in, methodInput(),
		sdk.ToolInput{Name: "other_income", Label: "Other capital income, SEK (optional)", Type: sdk.InputNumber,
			Help: "Interest and dividends minus interest costs for the year, only used for the tax estimate."})
	return in
}

func (t *taxPlugin) k4(p sdk.ToolParams) (sdk.ToolResult, error) {
	year, err := parseYear(p)
	if err != nil {
		return sdk.ToolResult{}, err
	}
	l, err := load(p)
	if err != nil {
		return sdk.ToolResult{}, err
	}
	rows := Rows(l.result.Sales, year, false)
	res := sdk.ToolResult{Title: fmt.Sprintf("K4 for income year %d", year)}
	blocks := &res.Blocks

	if len(rows) == 0 {
		*blocks = append(*blocks, notice(sdk.LevelInfo, "No sales in "+strconv.Itoa(year),
			fmt.Sprintf("The files contain no sales dated %d, so there is nothing to report on K4 for that year. (%d trades were read between %s and %s.)",
				year, len(l.txs), l.report.First.Format("2006-01-02"), l.report.Last.Format("2006-01-02"))))
		*blocks = append(*blocks, accountsTable(l.report))
		return res, nil
	}
	totals := SumRows(rows)
	est := totals.Estimate.Compute(num(p, "other_income"))
	pages := Pages(rows)

	*blocks = append(*blocks, sdk.ToolBlock{Type: sdk.BlockStats, Stats: []sdk.ToolStat{
		{Label: "Securities sold", Value: strconv.Itoa(len(rows)), Hint: fmt.Sprintf("%d K4 page(s)", len(pages))},
		{Label: "Sale proceeds", Value: kr(sumProceeds(rows)) + " kr"},
		{Label: "Cost basis", Value: kr(sumCost(rows)) + " kr"},
		{Label: "Result", Value: kr(totals.GainA+totals.GainD-totals.LossA-totals.LossD) + " kr", Hint: "gains minus losses, before the 70% rule"},
		{Label: "Estimated tax", Value: kr(est.Tax) + " kr", Hint: "30% — an estimate, Skatteverket decides"},
	}})

	*blocks = append(*blocks, notice(sdk.LevelWarning, "Check before you declare",
		bullets([]string{
			"Sales in an ISK, kapitalförsäkring, IPS or pension account are not declared on K4. Make sure none are included (see the account list below).",
			"This is a calculation from your own files, not tax advice. Compare the figures with your contract notes and the bank's year-end statement before you sign.",
			"The types below (aktie, fond, räntefond …) were detected from names. A wrong type changes whether the 20% rule can be used and how a loss counts.",
		})))
	if w := l.warnings(); len(w) > 0 {
		*blocks = append(*blocks, notice(sdk.LevelWarning, "Things to look at", bullets(w)))
	}

	*blocks = append(*blocks, notice(sdk.LevelInfo, "How to fill in the K4",
		bullets([]string{
			fmt.Sprintf("Open the K4 attachment (SKV 2104) in your income tax return for %d: on paper or in Skatteverket's e-service.", year),
			"For each row in the tables below, copy Antal, Beteckning, Försäljningspris and Omkostnadsbelopp, then Vinst or Förlust (only one of them has a value).",
			"Use the same page and row order; when a table says page 2, start a new K4 page. Compare the Summa line with the form's own sum.",
			"Then carry the totals to INK1: boxes 7.4 and 8.3 for section A, 7.5 and 8.4 for section D (table further down).",
			"Use the Copy button on a table to paste the rows into a spreadsheet, or download the Excel workbook.",
		})))
	for _, pg := range pages {
		if len(pg.A) > 0 {
			*blocks = append(*blocks, k4Table(fmt.Sprintf("K4 page %d of %d — section A (listed shares, funds)", pg.Number, len(pages)), pg.A, pg.SumA))
		}
		if len(pg.D) > 0 {
			*blocks = append(*blocks, k4Table(fmt.Sprintf("K4 page %d of %d — section D (other: crypto, unlisted)", pg.Number, len(pages)), pg.D, pg.SumD))
		}
	}

	*blocks = append(*blocks, sdk.ToolBlock{Type: sdk.BlockTable, Title: "Carry these to the income tax return (INK1)",
		Columns: []sdk.ToolColumn{{Label: "Box"}, {Label: "What"}, {Label: "SEK", Align: "right"}},
		Rows: [][]string{
			{"7.4", "Gain, funds etc. (K4 section A)", kr(totals.GainA)},
			{"8.3", "Loss, funds etc. (K4 section A)", kr(totals.LossA)},
			{"7.5", "Gain, other (K4 section D)", kr(totals.GainD)},
			{"8.4", "Loss, other (K4 section D)", kr(totals.LossD)},
		}})
	*blocks = append(*blocks, text("Skatteverket may already have filled boxes 7.4 and 8.3 from the bank's control statements. If so, add your K4 sum to the pre-filled amount as the form instructs, and do not do the offsetting (kvittning) or the 70% reduction (kvotering) yourself: Skatteverket does that."))
	*blocks = append(*blocks, estimateBlocks(est)...)
	*blocks = append(*blocks, accountsTable(l.report))

	detail := [][]string{{"datum", "konto", "värdepapper", "isin", "typ", "antal", "försäljningspris_sek", "avgift_sek", "omkostnadsbelopp_sek", "metod", "genomsnittsmetod_sek", "schablon_20_sek", "vinst_förlust_sek", "anmärkning"}}
	for _, s := range l.result.Sales {
		if s.Date.Year() != year {
			continue
		}
		detail = append(detail, []string{s.Date.Format("2006-01-02"), s.Account, s.Name, s.ISIN, s.Class.Label(), formatQty(s.Qty),
			dec(s.Proceeds, 2), dec(s.Fee, 2), dec(s.Cost, 2), string(s.Method), dec(s.Average, 2), dec(s.FlatRate, 2), dec(s.Gain(), 2), strings.Join(s.Notes, "; ")})
	}
	rowsCSV := [][]string{{"avsnitt", "antal", "beteckning", "isin", "typ", "försäljningspris", "omkostnadsbelopp", "vinst", "förlust"}}
	for _, r := range rows {
		rowsCSV = append(rowsCSV, []string{r.Class.Section(), qtyText(r), r.Name, r.ISIN, r.Class.Label(),
			strconv.FormatInt(r.Proceeds, 10), strconv.FormatInt(r.Cost, 10), strconv.FormatInt(r.Gain, 10), strconv.FormatInt(r.Loss, 10)})
	}
	book, err := k4Workbook(year, pages, rows, totals, est, l.result.Sales, l.warnings())
	if err != nil {
		return sdk.ToolResult{}, sdk.Errorf(sdk.CodeInternal, "build workbook: %v", err)
	}
	*blocks = append(*blocks,
		sdk.ToolBlock{Type: sdk.BlockFile, File: &sdk.ToolOutput{Name: fmt.Sprintf("k4-%d.xlsx", year), MIME: xlsxMIME, Encoding: "base64", Content: b64(book),
			Description: "Excel workbook: summary, the K4 rows laid out like the form (section A and D, with SUM lines), every sale and the things to check."}},
		sdk.ToolBlock{Type: sdk.BlockFile, File: csvFile(fmt.Sprintf("k4-%d-rader.csv", year), "The K4 rows as CSV, for pasting into any spreadsheet.", rowsCSV)},
		sdk.ToolBlock{Type: sdk.BlockFile, File: csvFile(fmt.Sprintf("forsaljningar-%d.csv", year), "Every sale with the cost by both methods, for your records.", detail)})
	return res, nil
}

func k4Table(title string, rows []Row, s Sums) sdk.ToolBlock {
	b := sdk.ToolBlock{Type: sdk.BlockTable, Title: title, Columns: []sdk.ToolColumn{
		{Label: "Antal", Align: "right"}, {Label: "Beteckning"}, {Label: "Försäljningspris", Align: "right"}, {Label: "Omkostnadsbelopp", Align: "right"},
		{Label: "Vinst", Align: "right"}, {Label: "Förlust", Align: "right"}, {Label: "Type"}, {Label: "Cost method"}}}
	for _, r := range rows {
		qty := strconv.FormatInt(r.Qty, 10)
		if r.Class.Section() == "D" {
			qty = formatQty(r.QtyExact)
		}
		b.Rows = append(b.Rows, []string{qty, rowLabel(r), kr(r.Proceeds), kr(r.Cost), kr(r.Gain), kr(r.Loss), r.Class.Label(), methodLabel(r)})
	}
	b.Rows = append(b.Rows, []string{"", "Summa", kr(s.Proceeds), kr(s.Cost), kr(s.Gain), kr(s.Loss), "", ""})
	return b
}

func methodLabel(r Row) string {
	switch {
	case r.FlatRateSales > 0 && r.AverageSales > 0:
		return "blandad"
	case r.FlatRateSales > 0:
		return "schablon 20 %"
	}
	return "genomsnitt"
}

func sumProceeds(rows []Row) int64 {
	var n int64
	for _, r := range rows {
		n += r.Proceeds
	}
	return n
}

func sumCost(rows []Row) int64 {
	var n int64
	for _, r := range rows {
		n += r.Cost
	}
	return n
}

func estimateBlocks(e Estimate) []sdk.ToolBlock {
	rows := [][]string{
		{"Gains (K4)", kr(e.GainSetOff + e.GainOther)},
		{"Losses set off in full against gains", "−" + kr(e.LossFullOffset)},
		{"Remaining losses counted at 70%", "−" + kr(e.LossQuotedTotal)},
		{"Result from K4", kr(e.NetK4)},
	}
	if e.OtherIncome != 0 {
		rows = append(rows, []string{"Other capital income (your input)", kr(e.OtherIncome)})
	}
	rows = append(rows, []string{"Capital income", kr(e.CapitalIncome)})
	switch {
	case e.Tax > 0:
		rows = append(rows, []string{"Tax at 30%", kr(e.Tax)})
	case e.Deficit > 0:
		rows = append(rows, []string{"Deficit of capital (underskott)", kr(e.Deficit)}, []string{"Tax reduction (skattereduktion)", kr(e.TaxReduction)})
	}
	return []sdk.ToolBlock{
		{Type: sdk.BlockTable, Title: "Estimated tax effect (simplified)", Columns: []sdk.ToolColumn{{Label: "Step"}, {Label: "SEK", Align: "right"}}, Rows: rows},
		text("The estimate follows Skatteverket's rules in simplified form: losses on listed shares and equity/mixed funds offset gains in full, other losses count at 70%, tax is 30% of positive capital income, and a deficit gives a tax reduction of 30% up to 100 000 kr and 30 000 kr plus 21% of the excess above. Only Skatteverket's own assessment counts."),
	}
}

// ---- holdings ----

func (t *taxPlugin) holdings(p sdk.ToolParams) (sdk.ToolResult, error) {
	l, err := load(p)
	if err != nil {
		return sdk.ToolResult{}, err
	}
	asOf := t.now()
	if v := p.String("as_of"); v != "" {
		d, ok := parseDate(v)
		if !ok {
			return sdk.ToolResult{}, invalid("the date must be YYYY-MM-DD")
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
	res := sdk.ToolResult{Title: "Holdings and average cost on " + asOf.Format("2006-01-02")}
	tbl := sdk.ToolBlock{Type: sdk.BlockTable, Title: "Remaining holdings", Columns: []sdk.ToolColumn{
		{Label: "Security"}, {Label: "ISIN"}, {Label: "Type"}, {Label: "Quantity", Align: "right"}, {Label: "Total cost, SEK", Align: "right"}, {Label: "Average cost / unit", Align: "right"}, {Label: "Note"}}}
	opening := [][]string{{"date", "type", "name", "isin", "class", "quantity", "amount_sek", "fee_sek", "account"}}
	next := asOf.AddDate(0, 0, 1).Format("2006-01-02")
	var total float64
	for _, h := range l.result.Holdings {
		note := ""
		if h.CostUnknown {
			note = "cost unknown for transferred shares"
		}
		tbl.Rows = append(tbl.Rows, []string{h.Name, h.ISIN, h.Class.Label(), formatQty(h.Qty), kr(h.Cost), dec(h.AvgCost(), 2), note})
		opening = append(opening, []string{next, "opening", h.Name, h.ISIN, string(h.Class), strconv.FormatFloat(h.Qty, 'f', -1, 64), strconv.FormatFloat(h.Cost, 'f', 2, 64), "0", ""})
		total += h.Cost
	}
	if len(tbl.Rows) == 0 {
		res.Blocks = append(res.Blocks, notice(sdk.LevelInfo, "Nothing held", "No securities remain on that date according to the files."))
		return res, nil
	}
	res.Blocks = append(res.Blocks, sdk.ToolBlock{Type: sdk.BlockStats, Stats: []sdk.ToolStat{
		{Label: "Positions", Value: strconv.Itoa(len(tbl.Rows))}, {Label: "Total cost basis", Value: kr(total) + " kr"}}})
	if w := l.warnings(); len(w) > 0 {
		res.Blocks = append(res.Blocks, notice(sdk.LevelWarning, "Things to look at", bullets(w)))
	}
	book, err := holdingsWorkbook(asOf.Format("2006-01-02"), l.result.Holdings)
	if err != nil {
		return sdk.ToolResult{}, sdk.Errorf(sdk.CodeInternal, "build workbook: %v", err)
	}
	res.Blocks = append(res.Blocks, tbl, accountsTable(l.report),
		sdk.ToolBlock{Type: sdk.BlockFile, File: &sdk.ToolOutput{Name: "holdings-" + asOf.Format("2006-01-02") + ".xlsx", MIME: xlsxMIME, Encoding: "base64", Content: b64(book),
			Description: "The holdings above as an Excel sheet."}},
		sdk.ToolBlock{Type: sdk.BlockFile, File: csvFile("opening-balance-"+next+".csv",
			"The holdings above as opening balances. Next year, upload this file instead of all the old history (generic CSV format).", opening)})
	return res, nil
}

// ---- planner ----

func (t *taxPlugin) planner(p sdk.ToolParams) (sdk.ToolResult, error) {
	year, err := parseYear(p)
	if err != nil {
		return sdk.ToolResult{}, err
	}
	l, err := load(p)
	if err != nil {
		return sdk.ToolResult{}, err
	}
	planned, problems := parsePlanned(p.String("planned"), l.result.Holdings, year)
	if len(planned) == 0 {
		msg := "Write the sales to try, one per line: name or ISIN; quantity; price per unit in SEK; fee in SEK (optional)."
		if len(problems) > 0 {
			msg = bullets(problems)
		}
		return sdk.ToolResult{}, invalid("%s", msg)
	}
	other := num(p, "other_income")
	before := SumRows(Rows(l.result.Sales, year, false)).Estimate.Compute(other)
	withPlan := Replay(append(append([]Tx(nil), l.txs...), planned...), l.opts)
	after := SumRows(Rows(withPlan.Sales, year, true)).Estimate.Compute(other)

	res := sdk.ToolResult{Title: fmt.Sprintf("What if I sell — income year %d", year)}
	if len(problems) > 0 {
		res.Blocks = append(res.Blocks, notice(sdk.LevelWarning, "Lines that could not be used", bullets(problems)))
	}
	tbl := sdk.ToolBlock{Type: sdk.BlockTable, Title: "Planned sales", Columns: []sdk.ToolColumn{
		{Label: "Security"}, {Label: "Quantity", Align: "right"}, {Label: "Proceeds", Align: "right"}, {Label: "Cost basis", Align: "right"}, {Label: "Gain / loss", Align: "right"}, {Label: "Cost method"}}}
	for _, s := range withPlan.Sales {
		if !s.Planned {
			continue
		}
		tbl.Rows = append(tbl.Rows, []string{s.Name, formatQty(s.Qty), kr(s.Proceeds), kr(s.Cost), kr(s.Gain()), string(s.Method)})
	}
	res.Blocks = append(res.Blocks, tbl, sdk.ToolBlock{Type: sdk.BlockTable, Title: "Effect on this year's tax (estimate)",
		Columns: []sdk.ToolColumn{{Label: ""}, {Label: "Without", Align: "right"}, {Label: "With the sales", Align: "right"}, {Label: "Difference", Align: "right"}},
		Rows: [][]string{
			{"Result from K4", kr(before.NetK4), kr(after.NetK4), kr(after.NetK4 - before.NetK4)},
			{"Tax", kr(before.Tax), kr(after.Tax), kr(after.Tax - before.Tax)},
			{"Tax reduction on a deficit", kr(before.TaxReduction), kr(after.TaxReduction), kr(after.TaxReduction - before.TaxReduction)},
			{"Net tax cost", kr(before.NetEffect()), kr(after.NetEffect()), kr(after.NetEffect() - before.NetEffect())},
		}})
	if w := l.warnings(); len(w) > 0 {
		res.Blocks = append(res.Blocks, notice(sdk.LevelWarning, "Things to look at", bullets(w)))
	}
	res.Blocks = append(res.Blocks, text("The planned sales are treated as made on 31 December of the income year with the price you give; fees reduce the proceeds. Nothing is sold or stored. It is an estimate, not advice."))
	return res, nil
}

func parsePlanned(textIn string, holdings []Holding, year int) ([]Tx, []string) {
	var out []Tx
	var problems []string
	end := time.Date(year, 12, 31, 0, 0, 0, 0, time.UTC)
	for i, line := range strings.Split(textIn, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		f := strings.Split(line, ";")
		if len(f) < 3 {
			problems = append(problems, fmt.Sprintf("line %d needs 'name or ISIN; quantity; price'", i+1))
			continue
		}
		qty, ok1 := parseNumber(f[1])
		price, ok2 := parseNumber(f[2])
		var fee float64
		if len(f) > 3 {
			fee, _ = parseNumber(f[3])
		}
		if !ok1 || !ok2 || qty <= 0 || price <= 0 {
			problems = append(problems, fmt.Sprintf("line %d: quantity and price must be positive numbers", i+1))
			continue
		}
		h, err := findHolding(strings.TrimSpace(f[0]), holdings)
		if err != nil {
			problems = append(problems, fmt.Sprintf("line %d: %v", i+1, err))
			continue
		}
		if qty > h.Qty+epsilon {
			problems = append(problems, fmt.Sprintf("line %d: you hold %s of %s, not %s", i+1, formatQty(h.Qty), h.Name, formatQty(qty)))
			continue
		}
		out = append(out, Tx{Date: end, Kind: KindSell, Name: h.Name, ISIN: h.ISIN, Class: h.Class, Qty: qty, Gross: qty * price, Fee: math.Abs(fee), Planned: true, Source: fmt.Sprintf("plan line %d", i+1)})
	}
	return out, problems
}

func findHolding(q string, holdings []Holding) (Holding, error) {
	var matches []Holding
	for _, h := range holdings {
		if strings.EqualFold(h.ISIN, q) || strings.EqualFold(h.Name, q) {
			return h, nil
		}
		if q != "" && strings.Contains(strings.ToLower(h.Name), strings.ToLower(q)) {
			matches = append(matches, h)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return Holding{}, fmt.Errorf("no holding matches %q", q)
	}
	return Holding{}, fmt.Errorf("%q matches several holdings; use the ISIN", q)
}

// ---- estimate ----

func (t *taxPlugin) estimate(p sdk.ToolParams) (sdk.ToolResult, error) {
	pos := func(name string) float64 { return math.Max(num(p, name), 0) }
	e := Estimate{GainSetOff: pos("gain_listed") + pos("gain_unlisted"), GainOther: pos("gain_crypto"),
		LossFull: pos("loss_listed"), LossQuoted: pos("loss_interest_funds") + pos("loss_unlisted") + pos("loss_crypto")}
	e = e.Compute(num(p, "other_income"))
	res := sdk.ToolResult{Title: "Tax estimate on securities sales"}
	res.Blocks = append(res.Blocks, sdk.ToolBlock{Type: sdk.BlockStats, Stats: []sdk.ToolStat{
		{Label: "Capital income", Value: kr(e.CapitalIncome) + " kr"},
		{Label: "Tax at 30%", Value: kr(e.Tax) + " kr"},
		{Label: "Tax reduction", Value: kr(e.TaxReduction) + " kr", Hint: "on a deficit"},
	}})
	res.Blocks = append(res.Blocks, estimateBlocks(e)...)
	return res, nil
}

// ---- review ----

func (t *taxPlugin) review(p sdk.ToolParams) (sdk.ToolResult, error) {
	l, err := load(p)
	if err != nil {
		return sdk.ToolResult{}, err
	}
	r := l.report
	res := sdk.ToolResult{Title: "Review of the files"}
	res.Blocks = append(res.Blocks, sdk.ToolBlock{Type: sdk.BlockStats, Stats: []sdk.ToolStat{
		{Label: "Format", Value: r.Format}, {Label: "Rows read", Value: strconv.Itoa(r.Rows)}, {Label: "Trades used", Value: strconv.Itoa(len(l.txs))},
		{Label: "Period", Value: r.First.Format("2006-01-02") + " – " + r.Last.Format("2006-01-02")}}})
	res.Blocks = append(res.Blocks, accountsTable(r))
	res.Blocks = append(res.Blocks, notice(sdk.LevelInfo, "Which accounts to include",
		"Only accounts where you trade directly (a regular depot/aktie- och fondkonto) belong on K4. Leave out ISK, kapitalförsäkring (KF), IPS and pension accounts: they are taxed through other rules. Put their account numbers in 'Accounts to leave out' and run again."))
	types := sdk.ToolBlock{Type: sdk.BlockTable, Title: "Transaction types in the files", Columns: []sdk.ToolColumn{{Label: "Type"}, {Label: "Rows", Align: "right"}, {Label: "Used for tax"}}}
	names := make([]string, 0, len(r.Types))
	for k := range r.Types {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		used := "yes"
		if r.Ignored[k] > 0 {
			used = "no (no tax event)"
		}
		types.Rows = append(types.Rows, []string{k, strconv.Itoa(r.Types[k]), used})
	}
	res.Blocks = append(res.Blocks, types)

	inst := sdk.ToolBlock{Type: sdk.BlockTable, Title: "Instruments and the type used for each", Columns: []sdk.ToolColumn{
		{Label: "Security"}, {Label: "ISIN"}, {Label: "Type"}, {Label: "How"}}}
	seen := map[string]bool{}
	var keys []string
	byKey := map[string]Tx{}
	for _, tx := range l.txs {
		if !seen[tx.Key()] {
			seen[tx.Key()] = true
			keys = append(keys, tx.Key())
			byKey[tx.Key()] = tx
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		return strings.ToLower(byKey[keys[i]].Name) < strings.ToLower(byKey[keys[j]].Name)
	})
	for _, k := range keys {
		how := "from the file or your fix"
		if l.result.Guessed[k] {
			how = "guessed from the name"
		}
		inst.Rows = append(inst.Rows, []string{byKey[k].Name, byKey[k].ISIN, l.result.Classes[k].Label(), how})
	}
	res.Blocks = append(res.Blocks, inst)
	if w := l.warnings(); len(w) > 0 {
		res.Blocks = append(res.Blocks, notice(sdk.LevelWarning, "Things to look at", bullets(w)))
	}
	return res, nil
}

// ---- guide ----

const genericTemplate = "date;type;name;isin;class;quantity;amount_sek;fee_sek;account\n" +
	"2023-03-10;buy;Volvo B;SE0000115446;aktie;100;21000;39;Depå 1\n" +
	"2024-05-02;buy;Volvo B;SE0000115446;aktie;50;11500;39;Depå 1\n" +
	"2025-02-14;sell;Volvo B;SE0000115446;aktie;80;19600;39;Depå 1\n" +
	"2025-01-01;opening;Global Index Fond;SE0000000001;aktiefond;12.5;4200;0;Depå 1\n" +
	"2025-06-03;split;Volvo B;SE0000115446;aktie;70;;;Depå 1\n"

func (t *taxPlugin) guide(sdk.ToolParams) (sdk.ToolResult, error) {
	res := sdk.ToolResult{Title: "Selling securities and the K4 — how it works"}
	res.Blocks = []sdk.ToolBlock{
		text("When you sell shares or fund units in a regular depot (not an ISK or kapitalförsäkring) the profit is capital income, taxed at 30%. You report each security sold on the K4 attachment to the income tax return, in the spring the year after the sale. The income tax return for 2026 is due in early May 2027; check the exact date on skatteverket.se."),
		{Type: sdk.BlockTable, Title: "The rules this plugin applies", Columns: []sdk.ToolColumn{{Label: "Topic"}, {Label: "Rule"}}, Rows: [][]string{
			{"Gain or loss", "Sale proceeds minus fee, minus the cost basis (omkostnadsbelopp). The purchase fee is part of the cost."},
			{"Average cost (genomsnittsmetoden)", "For identical securities the cost per unit is the total cost of all units you held divided by the number of units. A split changes the number of units but not the total cost."},
			{"Flat rate (schablonmetoden)", "For listed shares you may instead set the cost to 20% of the proceeds after the fee. This plugin takes the better of the two per sale unless you choose average cost only. It never applies the flat rate to funds."},
			{"Foreign shares", "Amounts are converted to SEK at the rate on the trade date. The plugin uses the SEK values and rates in your bank's export; compare with the contract notes."},
			{"K4 sections", "Section A: listed shares, equity/mixed/bond funds. Section D: crypto assets and unlisted securities. Section A has nine rows per page and section D seven; more pages are added automatically."},
			{"Boxes in the return", "Section A gains go to box 7.4 and losses to 8.3; section D gains to 7.5 and losses to 8.4."},
			{"Losses", "Losses on listed shares and on equity and mixed funds offset gains in full. Other losses (bond funds, crypto, unlisted) and any remaining loss count at 70%. Skatteverket does the offsetting; this plugin only estimates it."},
			{"Deficit", "A net deficit of capital gives a tax reduction of 30% up to 100 000 kr and 30 000 kr plus 21% of the excess above that."},
			{"ISK, KF, IPS, pensions", "Not reported on K4. Leave those accounts out."},
		}},
		notice(sdk.LevelWarning, "What this plugin does not handle",
			bullets([]string{
				"Options, futures, CFDs, warrants and currency trading (K4 sections B and C).",
				"Spin-offs, mergers, rights issues and other company events other than splits: add an adjustment by hand in the generic CSV or check the company's history on Skatteverket's 'Aktiehistorik' page.",
				"Shares received as gifts or inheritance, and qualified shares in closely held companies (K10).",
				"Deferred gains (uppskov), K4 section B.",
				"Fractional fund units: the K4 form takes whole units, so quantities are rounded.",
			})),
		text("Generic CSV format. Use it for other brokers or to add history by hand. Columns: date (YYYY-MM-DD), type (buy, sell, opening, split, transfer_in, transfer_out), name, isin, class (aktie, aktiefond, blandfond, ranfond, krypto, onoterad), quantity, amount_sek (price × quantity before the fee; for 'opening' the total cost; empty for split), fee_sek, account. For a split give the change in the number of units as the quantity (a 2:1 split of 100 shares is +100). Semicolon- or comma-separated; decimals with point or comma."),
		{Type: sdk.BlockFile, File: &sdk.ToolOutput{Name: "generic-transactions-template.csv", MIME: "text/csv", Encoding: "utf8", Content: genericTemplate, Description: "Example rows for the generic CSV format"}},
		text("Sources: Skatteverket — Deklarera aktier och övriga värdepapper (skatteverket.se/privat/skatter/vardepapper), 'Räkna ut omkostnadsbelopp', 'Kvittning och kvotering', and the K4 form SKV 2104."),
	}
	return res, nil
}

// qtyText is the quantity as the K4 section takes it: whole units in A, decimals in D.
func qtyText(r Row) string {
	if r.Class.Section() == "D" {
		return formatQty(r.QtyExact)
	}
	return strconv.FormatInt(r.Qty, 10)
}
