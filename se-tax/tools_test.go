package main

import (
	"encoding/base64"
	"strings"
	"testing"

	sdk "github.com/Doldas/floweysales/pluginsdk"
	"github.com/Doldas/floweysales/pluginsdk/plugintest"
)

const e2eCSV = avanzaHeader +
	"2023-03-10;1111111;Köp;Volvo B;100;210;-21 039;SEK;39;;SEK;SE0000115446;\n" +
	"2024-05-02;1111111;Köp;Volvo B;50;230;-11 539;SEK;39;;SEK;SE0000115446;\n" +
	"2026-02-14;1111111;Sälj;Volvo B;-80;245,50;19 601;SEK;39;;SEK;SE0000115446;\n" +
	"2026-03-01;1111111;Köp;Spiltan Aktiefond Investmentbolag;10;100;-1 000;SEK;0;;SEK;SE0001;\n" +
	"2026-09-01;1111111;Sälj;Spiltan Aktiefond Investmentbolag;-10;80;800;SEK;0;;SEK;SE0001;\n" +
	"2026-04-01;9999999;Köp;ISK Aktie;10;100;-1 000;SEK;0;;SEK;SE0002;\n" +
	"2026-05-01;9999999;Sälj;ISK Aktie;-10;300;3 000;SEK;0;;SEK;SE0002;\n"

func callTool(t *testing.T, tool string, input map[string]any) (sdk.ToolResult, error) {
	t.Helper()
	c := plugintest.Start(t, newPlugin(&taxPlugin{now: fixedNow}))
	var out sdk.ToolResult
	err := c.Call(sdk.MethodTool, sdk.ToolParams{Tool: tool, Input: input}, &out)
	return out, err
}

func blocksOf(r sdk.ToolResult, typ string) []sdk.ToolBlock {
	var out []sdk.ToolBlock
	for _, b := range r.Blocks {
		if b.Type == typ {
			out = append(out, b)
		}
	}
	return out
}

func fileNamed(r sdk.ToolResult, name string) *sdk.ToolOutput {
	for _, b := range blocksOf(r, sdk.BlockFile) {
		if b.File.Name == name {
			return b.File
		}
	}
	return nil
}

func files() []map[string]string {
	return []map[string]string{{"name": "avanza.csv", "content": e2eCSV}}
}

func TestK4EndToEnd(t *testing.T) {
	res, err := callTool(t, "k4", map[string]any{
		"year": "2026", "files": files(), "exclude_accounts": "9999999", "personal_number": "19811218-9876",
		"name": "Åke Öberg", "postal_code": "123 45", "city": "Göteborg", "other_income": 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	tables := blocksOf(res, sdk.BlockTable)
	var k4 sdk.ToolBlock
	for _, b := range tables {
		if strings.Contains(b.Title, "section A") {
			k4 = b
		}
	}
	if len(k4.Rows) != 3 { // two securities and the sum row
		t.Fatalf("K4 table: %+v", k4)
	}
	// Volvo: average cost 80 × (21 039+11 539)/150 = 17 374.93; flat rate 20% of 19 601 = 3 920, so average wins.
	volvo := k4.Rows[1]
	if volvo[0] != "80" || !strings.Contains(volvo[1], "Volvo B") || volvo[2] != "19\u00a0601" || volvo[3] != "17\u00a0375" || volvo[4] != "2\u00a0226" {
		t.Errorf("Volvo row %q", volvo)
	}
	fund := k4.Rows[0] // sorted by name: Spiltan before Volvo
	if !strings.Contains(fund[1], "Spiltan") || fund[5] != "200" {
		t.Errorf("fund row %q", fund)
	}
	// The ISK account was excluded: its 2 000 kr gain must not appear anywhere.
	for _, b := range blocksOf(res, sdk.BlockTable) {
		for _, row := range b.Rows {
			if strings.Contains(strings.Join(row, " "), "ISK Aktie") {
				t.Errorf("excluded account leaked into %q", b.Title)
			}
		}
	}
	sru := fileNamed(res, "BLANKETTER.SRU")
	if sru == nil || fileNamed(res, "INFO.SRU") == nil || fileNamed(res, "k4-2026-rader.csv") == nil || fileNamed(res, "forsaljningar-2026.csv") == nil {
		t.Fatalf("missing files: %+v", blocksOf(res, sdk.BlockFile))
	}
	raw, err := base64.StdEncoding.DecodeString(sru.Content)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	for _, want := range []string{"#BLANKETT K4-2026P4", "#IDENTITET 198112189876 20270315 103000", "#UPPGIFT 3100 10", "#UPPGIFT 3105 200", "#UPPGIFT 3110 80", "#UPPGIFT 3114 2226", "#UPPGIFT 3304 2226", "#UPPGIFT 3305 200", "#FIL_SLUT"} {
		if !strings.Contains(body, want) {
			t.Errorf("SRU lacks %q:\n%s", want, strings.ReplaceAll(body, "\r\n", "\n"))
		}
	}
	if strings.Contains(strings.Join(noticeTexts(res), " "), "19811218") {
		t.Error("the personal number must be masked in the display")
	}
}

func noticeTexts(r sdk.ToolResult) []string {
	var out []string
	for _, b := range r.Blocks {
		out = append(out, b.Title, b.Text)
	}
	return out
}

func TestK4WithoutPersonalNumberSkipsTheSRUFilesButKeepsTheCSV(t *testing.T) {
	res, err := callTool(t, "k4", map[string]any{"year": "2026", "files": files(), "exclude_accounts": "9999999"})
	if err != nil {
		t.Fatal(err)
	}
	if fileNamed(res, "BLANKETTER.SRU") != nil || fileNamed(res, "k4-2026-rader.csv") == nil {
		t.Errorf("%+v", blocksOf(res, sdk.BlockFile))
	}
	res, err = callTool(t, "k4", map[string]any{"year": "2026", "files": files(), "personal_number": "19811218-9875", "name": "X", "postal_code": "1", "city": ""})
	if err != nil || fileNamed(res, "BLANKETTER.SRU") != nil {
		t.Fatalf("a bad personal number must not produce a file: %v", err)
	}
	var found bool
	for _, b := range blocksOf(res, sdk.BlockNotice) {
		found = found || (b.Level == sdk.LevelError && strings.Contains(b.Text, "check digit") && strings.Contains(b.Text, "postal code"))
	}
	if !found {
		t.Errorf("expected an error notice listing the problems: %+v", blocksOf(res, sdk.BlockNotice))
	}
}

func TestK4YearWithoutSalesSaysSo(t *testing.T) {
	res, err := callTool(t, "k4", map[string]any{"year": "2025", "files": files()})
	if err != nil {
		t.Fatal(err)
	}
	if n := blocksOf(res, sdk.BlockNotice); len(n) == 0 || !strings.Contains(n[0].Title, "No sales") || len(blocksOf(res, sdk.BlockFile)) != 0 {
		t.Errorf("%+v", res.Blocks)
	}
}

func TestToolsRejectBadInput(t *testing.T) {
	for name, tc := range map[string]struct {
		tool  string
		input map[string]any
	}{
		"unsupported year":  {"k4", map[string]any{"year": "2019", "files": files()}},
		"no files":          {"k4", map[string]any{"year": "2026"}},
		"bad override":      {"k4", map[string]any{"year": "2026", "files": files(), "classes": "nonsense"}},
		"unreadable file":   {"review", map[string]any{"files": []map[string]string{{"name": "x", "content": "a;b\n1;2"}}}},
		"only ignored rows": {"review", map[string]any{"files": []map[string]string{{"name": "x", "content": avanzaHeader + "2026-01-01;1;Insättning;;;;100;SEK;;;;;\n"}}}},
		"bad plan":          {"planner", map[string]any{"year": "2026", "files": files(), "planned": "Volvo B; abc; 100"}},
		"unknown holding":   {"planner", map[string]any{"year": "2026", "files": files(), "planned": "Nokia; 1; 100"}},
	} {
		if _, err := callTool(t, tc.tool, tc.input); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestHoldingsAndOpeningBalanceRoundTrip(t *testing.T) {
	res, err := callTool(t, "holdings", map[string]any{"files": files(), "exclude_accounts": "9999999", "as_of": "2026-12-31"})
	if err != nil {
		t.Fatal(err)
	}
	opening := fileNamed(res, "opening-balance-2027-01-01.csv")
	if opening == nil {
		t.Fatalf("%+v", res.Blocks)
	}
	// Volvo: 150 bought for 32 578, 80 sold → 70 left costing 32 578 × 70/150.
	if !strings.Contains(opening.Content, "Volvo B") || !strings.Contains(opening.Content, "70") || !strings.Contains(opening.Content, "15203.07") {
		t.Errorf("%s", opening.Content)
	}
	// Selling the carried-over balance next year needs no old history.
	next := opening.Content + "2027-06-01;sell;Volvo B;SE0000115446;aktie;70;20000;0;\n"
	res, err = callTool(t, "k4", map[string]any{"year": "2026", "files": []map[string]string{{"name": "o.csv", "content": next}}})
	if err != nil {
		t.Fatal(err)
	}
	_ = res
}

func TestPlannerShowsTheTaxEffect(t *testing.T) {
	res, err := callTool(t, "planner", map[string]any{"year": "2026", "files": files(), "exclude_accounts": "9999999", "planned": "Volvo B; 70; 300; 39"})
	if err != nil {
		t.Fatal(err)
	}
	tables := blocksOf(res, sdk.BlockTable)
	if len(tables) < 2 {
		t.Fatalf("%+v", res.Blocks)
	}
	plan := tables[0].Rows[0]
	// 70 × 300 − 39 = 20 961 proceeds; average cost 32 578 × 70/150 = 15 203.07 → gain ≈ 5 758.
	if plan[2] != "20\u00a0961" || plan[3] != "15\u00a0203" {
		t.Errorf("plan row %q", plan)
	}
	effect := tables[1]
	if effect.Rows[1][2] == effect.Rows[1][1] {
		t.Errorf("the sale must change the tax: %q", effect.Rows)
	}
}

func TestEstimateAndGuideNeedNoFiles(t *testing.T) {
	res, err := callTool(t, "estimate", map[string]any{"gain_listed": 10000, "loss_listed": 4000, "other_income": 0})
	if err != nil {
		t.Fatal(err)
	}
	stats := blocksOf(res, sdk.BlockStats)[0].Stats
	if stats[1].Value != "1\u00a0800 kr" {
		t.Errorf("%+v", stats)
	}
	res, err = callTool(t, "guide", nil)
	if err != nil {
		t.Fatal(err)
	}
	tpl := fileNamed(res, "generic-transactions-template.csv")
	if tpl == nil {
		t.Fatal("the guide must offer the CSV template")
	}
	txs, rep, err := Import([]InputFile{{Name: "t.csv", Content: tpl.Content}}, "auto", nil)
	if err != nil || len(txs) != 5 || len(rep.Warnings) != 0 {
		t.Errorf("the template must be importable: %d rows, %v, %v", len(txs), rep.Warnings, err)
	}
	r := Replay(txs, Options{Method: AverageOnly})
	if len(r.Warnings) != 0 || len(r.Sales) != 1 {
		t.Errorf("the template must replay cleanly: %v", r.Warnings)
	}
}

func TestReviewListsAccountsAndGuessedTypes(t *testing.T) {
	res, err := callTool(t, "review", map[string]any{"files": files()})
	if err != nil {
		t.Fatal(err)
	}
	joined := ""
	for _, b := range blocksOf(res, sdk.BlockTable) {
		for _, row := range b.Rows {
			joined += strings.Join(row, "|") + "\n"
		}
	}
	for _, want := range []string{"1111111|5", "9999999|2", "Spiltan Aktiefond Investmentbolag|SE0001|Aktiefond/ETF|guessed from the name"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in\n%s", want, joined)
		}
	}
}
