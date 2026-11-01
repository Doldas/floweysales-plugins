// Command finnhub is the FloweySales Finnhub provider plugin. It needs
// FINNHUB_API_KEY, which the backend forwards from its own configuration.
package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	sdk "github.com/Doldas/floweysales/pluginsdk"
	"github.com/Doldas/floweysales/pluginsdk/httpx"
	"github.com/Doldas/floweysales/pluginsdk/research"
)

//go:embed plugin.json
var manifestJSON []byte

func main() {
	sdk.Main(newPlugin(&finnhub{
		http: httpx.New("FloweySales/1.0", 0), base: "https://finnhub.io/api/v1",
		key: strings.TrimSpace(os.Getenv("FINNHUB_API_KEY")),
	}))
}

type finnhub struct {
	http *httpx.Client
	base string
	key  string
}

func newPlugin(f *finnhub) sdk.Plugin {
	manifest, err := sdk.ParseManifest(manifestJSON)
	if err != nil {
		panic(err)
	}
	return sdk.Plugin{
		Manifest: manifest,
		Quote:    f.quote,
		History: func(ctx context.Context, p sdk.HistoryParams) (sdk.HistorySeries, error) {
			return f.history(ctx, p.Symbol, p.Range)
		},
		Search: func(ctx context.Context, p sdk.SearchParams) ([]sdk.SearchResult, error) {
			return f.search(ctx, p.Query)
		},
		Financials: func(ctx context.Context, p sdk.FinancialsParams) (sdk.FinancialsResponse, error) {
			return f.financials(ctx, p.Symbol, p.FromYear, p.ToYear)
		},
		Research: func(ctx context.Context, p sdk.ResearchParams) (sdk.ResearchResult, error) {
			return f.research(ctx, manifest, p)
		},
		News:   f.news,
		Events: f.events,
	}
}

// get calls the API with the token in a header, never in the URL.
func (f *finnhub) get(ctx context.Context, path string, q url.Values, out any) error {
	if f.key == "" {
		return sdk.Errorf(sdk.CodeNotConfigured, "FINNHUB_API_KEY must be configured on the backend")
	}
	body, err := f.http.Get(ctx, f.base+path+"?"+q.Encode(), map[string]string{"X-Finnhub-Token": f.key})
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}
	return nil
}

func (f *finnhub) quote(ctx context.Context, p sdk.QuoteParams) (sdk.Asset, error) {
	formatted := httpx.Upper(p.Symbol)
	var parsed map[string]any
	if err := f.get(ctx, "/quote", url.Values{"symbol": {formatted}}, &parsed); err != nil {
		return sdk.Asset{}, fmt.Errorf("[Finnhub] quote failed for %s: %w", formatted, err)
	}
	a := sdk.Asset{Symbol: formatted, Name: formatted, Currency: "USD", Price: httpx.Float(parsed, "c"), Provider: "FINNHUB"}
	a.Data = map[string]any{
		"assetDataProvider": "FinnHub.io", "type": "EQUITY", "symbol": a.Symbol, "name": a.Name, "price": a.Price, "currency": a.Currency,
		"open": httpx.Float(parsed, "o"), "high": httpx.Float(parsed, "h"), "low": httpx.Float(parsed, "l"),
		"previousClose": httpx.Float(parsed, "pc"), "change": httpx.Float(parsed, "d"), "changePercent": httpx.Float(parsed, "dp"),
		"lastUpdated": httpx.Int(parsed, "t"),
	}
	return a, nil
}

var candleSpecs = map[string]struct {
	res  string
	span int64
}{
	sdk.Range1D:  {"5", 86400},
	sdk.Range5D:  {"15", 5 * 86400},
	sdk.Range1M:  {"60", 30 * 86400},
	sdk.Range3M:  {"D", 90 * 86400},
	sdk.Range1Y:  {"D", 365 * 86400},
	sdk.Range3Y:  {"D", 3 * 365 * 86400},
	sdk.Range5Y:  {"D", 5 * 365 * 86400},
	sdk.Range10Y: {"D", 10 * 365 * 86400},
	sdk.RangeMAX: {"W", 50 * 365 * 86400},
}

func (f *finnhub) history(ctx context.Context, symbol, rng string) (sdk.HistorySeries, error) {
	spec, ok := candleSpecs[rng]
	if !ok {
		rng, spec = sdk.Range1M, candleSpecs[sdk.Range1M]
	}
	formatted := httpx.Upper(symbol)
	now := time.Now().Unix()
	var parsed struct {
		S string    `json:"s"`
		T []float64 `json:"t"`
		O []float64 `json:"o"`
		H []float64 `json:"h"`
		L []float64 `json:"l"`
		C []float64 `json:"c"`
		V []float64 `json:"v"`
	}
	q := url.Values{"symbol": {formatted}, "resolution": {spec.res}, "from": {fmt.Sprint(now - spec.span)}, "to": {fmt.Sprint(now)}}
	if err := f.get(ctx, "/stock/candle", q, &parsed); err != nil {
		return sdk.HistorySeries{}, fmt.Errorf("[FinnhubHistory] request failed for %s: %w", formatted, err)
	}
	if parsed.S != "ok" {
		return sdk.HistorySeries{}, fmt.Errorf("[FinnhubHistory] no data for %s (status %q)", formatted, parsed.S)
	}
	at := func(v []float64, i int) float64 {
		if i < len(v) {
			return v[i]
		}
		return 0
	}
	// Candles carry no currency and international symbols are not USD, so it
	// stays unknown; quote snapshots refuse a series without a currency.
	series := sdk.HistorySeries{Symbol: formatted, Provider: "FINNHUB", Range: rng, Interval: spec.res}
	for i, ts := range parsed.T {
		c := at(parsed.C, i)
		if c <= 0 {
			continue
		}
		o, h, l := httpx.FillOHLC(at(parsed.O, i), at(parsed.H, i), at(parsed.L, i), c)
		series.Points = append(series.Points, sdk.HistoryPoint{Timestamp: int64(ts), Open: o, High: h, Low: l, Close: c, Volume: at(parsed.V, i)})
	}
	if len(series.Points) == 0 {
		return sdk.HistorySeries{}, fmt.Errorf("[FinnhubHistory] empty series for %s", formatted)
	}
	return series, nil
}

func (f *finnhub) search(ctx context.Context, query string) ([]sdk.SearchResult, error) {
	var parsed struct {
		Result []struct {
			Symbol, Description, Type, DisplaySymbol string
		} `json:"result"`
	}
	if err := f.get(ctx, "/search", url.Values{"q": {query}}, &parsed); err != nil {
		return nil, fmt.Errorf("[FinnhubSearch] %w", err)
	}
	results := []sdk.SearchResult{}
	for _, r := range parsed.Result {
		if r.Symbol != "" {
			results = append(results, sdk.SearchResult{Symbol: r.Symbol, Name: r.Description, Type: strings.ToUpper(r.Type), Exchange: r.DisplaySymbol})
		}
	}
	return results, nil
}

type reported struct {
	Data []struct {
		Year      int    `json:"year"`
		Quarter   int    `json:"quarter"`
		Form      string `json:"form"`
		StartDate string `json:"startDate"`
		EndDate   string `json:"endDate"`
		Report    struct {
			IC []struct {
				Concept string  `json:"concept"`
				Label   string  `json:"label"`
				Value   float64 `json:"value"`
			} `json:"ic"`
		} `json:"report"`
	} `json:"data"`
}

func (f *finnhub) financials(ctx context.Context, symbol string, fromYear, toYear int) (sdk.FinancialsResponse, error) {
	formatted := httpx.Upper(symbol)
	var parsed reported
	if err := f.get(ctx, "/stock/financials-reported", url.Values{"symbol": {formatted}, "freq": {"quarterly"}}, &parsed); err != nil {
		return sdk.FinancialsResponse{}, fmt.Errorf("[FinnhubFinancials] request failed for %s: %w", formatted, err)
	}
	reports := quarterlyFromReported(parsed)
	reports = sdk.FilterFinancialYears(reports, fromYear, toYear)
	if fromYear == 0 && toYear == 0 && len(reports) > 8 {
		reports = reports[:8]
	}
	res := sdk.FinancialsResponse{Symbol: formatted, Provider: "FINNHUB", Source: "finnhub", FromYear: fromYear, ToYear: toYear, Reports: reports}
	if len(reports) == 0 {
		res.Note = "No quarterly SEC filings available from Finnhub for this instrument and requested reporting period."
	}
	return res, nil
}

// quarterlyFromReported turns SEC filings into per-quarter rows. 10-Q income
// statements report year-to-date values; rows are sorted ascending and the
// previous report's YTD figure is subtracted to recover each quarter.
func quarterlyFromReported(parsed reported) []sdk.QuarterlyFinancial {
	rows := parsed.Data
	sort.Slice(rows, func(i, j int) bool { return rows[i].EndDate < rows[j].EndDate })
	reports := make([]sdk.QuarterlyFinancial, 0, len(rows))
	var prev [6]*float64 // revenue, gross, operating, net, eps, fcf
	for _, r := range rows {
		periodEnd := r.EndDate
		if i := strings.IndexByte(periodEnd, ' '); i > 0 {
			periodEnd = periodEnd[:i]
		}
		cumulative := monthsBetween(r.StartDate, r.EndDate) > 4
		var cur [6]*float64
		for _, item := range r.Report.IC {
			concept, label := item.Concept, strings.ToLower(item.Label)
			v := item.Value
			switch {
			case cur[0] == nil && (strings.Contains(concept, "RevenueFromContractWithCustomer") || strings.HasSuffix(concept, "Revenues") ||
				label == "total revenue" || label == "revenue" || label == "net sales"):
				cur[0] = &v
			case cur[1] == nil && strings.Contains(concept, "GrossProfit"):
				cur[1] = &v
			case cur[2] == nil && strings.Contains(concept, "OperatingIncomeLoss"):
				cur[2] = &v
			case cur[3] == nil && strings.HasSuffix(concept, "NetIncomeLoss"):
				cur[3] = &v
			case cur[4] == nil && strings.Contains(concept, "EarningsPerShareDiluted"):
				cur[4] = &v
			case cur[5] == nil && strings.Contains(concept, "FreeCashFlow"):
				cur[5] = &v
			}
		}
		var q [6]*float64
		for i := range cur {
			switch {
			case cur[i] == nil:
			case !cumulative || prev[i] == nil:
				q[i] = cur[i]
			default:
				d := *cur[i] - *prev[i]
				q[i] = &d
			}
			// Keep the last known YTD value per concept; a missing concept in
			// one filing must not break the delta chain for later quarters.
			if cur[i] != nil {
				prev[i] = cur[i]
			}
		}
		reports = append(reports, sdk.QuarterlyFinancial{
			Period: fmt.Sprintf("Q%d FY%d", r.Quarter, r.Year), PeriodBasis: "fiscal", PeriodEnd: periodEnd,
			Currency: "USD", Form: r.Form,
			Revenue: round(q[0], 1), GrossProfit: round(q[1], 1), OperatingIncome: round(q[2], 1),
			NetIncome: round(q[3], 1), EPS: round(q[4], 1e4), FreeCashFlow: round(q[5], 1),
		})
	}
	sdk.SortByPeriodDesc(reports)
	return reports
}

// round trims float error from cumulative subtraction; scale 1 rounds to
// whole units, 1e4 keeps four decimals.
func round(p *float64, scale float64) *float64 {
	if p == nil {
		return nil
	}
	v := math.Round(*p*scale) / scale
	return &v
}

// monthsBetween returns the whole-month distance between two timestamps
// ("2006-01-02 15:04:05" or "2006-01-02"); 0 on parse failure.
func monthsBetween(start, end string) int {
	parse := func(s string) (time.Time, bool) {
		for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02"} {
			if t, err := time.Parse(layout, strings.TrimSpace(s)); err == nil {
				return t, true
			}
		}
		return time.Time{}, false
	}
	a, okA := parse(start)
	b, okB := parse(end)
	if !okA || !okB || b.Before(a) {
		return 0
	}
	return (b.Year()-a.Year())*12 + int(b.Month()) - int(a.Month())
}

func (f *finnhub) research(ctx context.Context, manifest sdk.Manifest, p sdk.ResearchParams) (sdk.ResearchResult, error) {
	op, err := research.Find(manifest, p.Operation)
	if err != nil {
		return sdk.ResearchResult{}, err
	}
	q, err := research.Query(op, p, research.EquitySymbol)
	if err != nil {
		return sdk.ResearchResult{}, err
	}
	if f.key == "" {
		return sdk.ResearchResult{}, sdk.Errorf(sdk.CodeNotConfigured, "FINNHUB_API_KEY must be configured on the backend")
	}
	if op.Operation == "basic_financials" {
		q.Set("metric", "all")
	}
	data, err := research.FetchJSON(ctx, http.DefaultClient, "FINNHUB", f.base+op.Path+"?"+q.Encode(), map[string]string{"X-Finnhub-Token": f.key})
	if err != nil {
		return sdk.ResearchResult{}, err
	}
	return research.Result("FINNHUB", op, p.Symbol, "https://finnhub.io/api/v1"+op.Path, data, ""), nil
}

// news reads company news for the last week from /company-news.
func (f *finnhub) news(ctx context.Context, p sdk.NewsParams) ([]sdk.NewsItem, error) {
	symbol := strings.ToUpper(strings.TrimSpace(p.Symbol))
	if symbol == "" {
		return nil, sdk.Errorf(sdk.CodeInvalidArgument, "symbol is required")
	}
	limit := p.Limit
	if limit < 1 || limit > 20 {
		limit = 5
	}
	now := time.Now().UTC()
	q := url.Values{"symbol": {symbol}, "from": {now.AddDate(0, 0, -7).Format("2006-01-02")}, "to": {now.Format("2006-01-02")}}
	var rows []struct {
		Headline string `json:"headline"`
		URL      string `json:"url"`
		Source   string `json:"source"`
		Summary  string `json:"summary"`
		Datetime int64  `json:"datetime"`
	}
	if err := f.get(ctx, "/company-news", q, &rows); err != nil {
		return nil, err
	}
	out := []sdk.NewsItem{}
	for _, r := range rows {
		if r.Headline == "" || !strings.HasPrefix(r.URL, "https://") {
			continue
		}
		summary := r.Summary
		if len(summary) > 400 {
			summary = summary[:400]
		}
		out = append(out, sdk.NewsItem{Title: r.Headline, URL: r.URL, Source: r.Source, Published: r.Datetime, Summary: summary, Symbol: symbol, Provider: "FINNHUB"})
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

// events returns earnings dates and, when the API plan allows it, dividends
// between two ISO dates. A date is confirmed when Finnhub names the reporting
// hour or when it is within a week; otherwise it is an estimate. Dividends are
// a paid-plan endpoint: when it is refused, earnings are returned alone.
func (f *finnhub) events(ctx context.Context, p sdk.EventsParams) ([]sdk.CorporateEvent, error) {
	symbol := strings.ToUpper(strings.TrimSpace(p.Symbol))
	if symbol == "" {
		return nil, sdk.Errorf(sdk.CodeInvalidArgument, "symbol is required")
	}
	start, err := time.Parse("2006-01-02", p.From)
	if err != nil {
		return nil, sdk.Errorf(sdk.CodeInvalidArgument, "from must be a date like 2026-11-01")
	}
	end, err := time.Parse("2006-01-02", p.To)
	if err != nil || end.Before(start) || end.Sub(start) > 400*24*time.Hour {
		return nil, sdk.Errorf(sdk.CodeInvalidArgument, "to must be a date within 400 days after from")
	}
	out := []sdk.CorporateEvent{}
	q := url.Values{"symbol": {symbol}, "from": {p.From}, "to": {p.To}}

	var earnings struct {
		Calendar []struct {
			Date   string `json:"date"`
			Hour   string `json:"hour"`
			Symbol string `json:"symbol"`
		} `json:"earningsCalendar"`
	}
	earningsErr := f.get(ctx, "/calendar/earnings", q, &earnings)
	soon := time.Now().UTC().AddDate(0, 0, 7).Format("2006-01-02")
	for _, row := range earnings.Calendar {
		if _, err := time.Parse("2006-01-02", row.Date); err != nil || (row.Symbol != "" && !strings.EqualFold(row.Symbol, symbol)) {
			continue
		}
		out = append(out, sdk.CorporateEvent{Symbol: symbol, Kind: "earnings", Date: row.Date, Confirmed: row.Hour != "" || row.Date <= soon, Provider: "FINNHUB"})
	}

	var dividends struct {
		Data []struct {
			Date     string  `json:"date"`
			Amount   float64 `json:"amount"`
			Currency string  `json:"currency"`
		} `json:"data"`
	}
	dividendsErr := f.get(ctx, "/stock/dividend2", url.Values{"symbol": {symbol}}, &dividends)
	for _, row := range dividends.Data {
		if row.Date < p.From || row.Date > p.To {
			continue
		}
		ev := sdk.CorporateEvent{Symbol: symbol, Kind: "dividend", Date: row.Date, Currency: row.Currency, Confirmed: true, Provider: "FINNHUB"}
		if row.Amount > 0 {
			amount := row.Amount
			ev.Amount = &amount
		}
		out = append(out, ev)
	}
	if earningsErr != nil && dividendsErr != nil {
		return nil, earningsErr
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	return out, nil
}
