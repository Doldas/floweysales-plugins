// Package yahooapi reads Yahoo Finance's unofficial public chart, search and
// fundamentals interfaces. It is shared by the Yahoo plugin and by the Avanza
// plugin's labelled history fallback.
package yahooapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	sdk "github.com/Doldas/floweysales/pluginsdk"
	"github.com/Doldas/floweysales/pluginsdk/httpx"
)

// DefaultBase is Yahoo's public query host.
const DefaultBase = "https://query1.finance.yahoo.com"

// API is a Yahoo client; Base is overridable for hermetic tests.
type API struct {
	HTTP *httpx.Client
	Base string
}

// New returns a client for the public host.
func New() *API {
	return &API{HTTP: httpx.New("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36", 0), Base: DefaultBase}
}

func (a *API) get(ctx context.Context, path string) (map[string]any, error) {
	body, err := a.HTTP.Get(ctx, a.Base+path, nil)
	if err != nil {
		return nil, err
	}
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return parsed, nil
}

// Quote returns the chart meta's regular market price.
func (a *API) Quote(ctx context.Context, symbol string) (sdk.Asset, error) {
	formatted := httpx.Upper(symbol)
	parsed, err := a.get(ctx, "/v8/finance/chart/"+url.PathEscape(formatted))
	if err != nil {
		return sdk.Asset{}, fmt.Errorf("[Yahoo] request failed for ticker %s: %w", formatted, err)
	}
	chart, _ := parsed["chart"].(map[string]any)
	results, _ := chart["result"].([]any)
	if len(results) == 0 {
		return sdk.Asset{}, fmt.Errorf("[Yahoo] no chart result for %s", formatted)
	}
	first, _ := results[0].(map[string]any)
	meta, _ := first["meta"].(map[string]any)
	if meta == nil {
		return sdk.Asset{}, fmt.Errorf("[Yahoo] result item has no 'meta' for %s", formatted)
	}
	asset := sdk.Asset{
		Symbol:   httpx.String(meta, "symbol"),
		Currency: httpx.String(meta, "currency"),
		Price:    httpx.Float(meta, "regularMarketPrice"),
		Provider: "YAHOO",
	}
	switch {
	case httpx.String(meta, "longName") != "":
		asset.Name = httpx.String(meta, "longName")
	case httpx.String(meta, "shortName") != "":
		asset.Name = httpx.String(meta, "shortName")
	default:
		asset.Name = asset.Symbol
	}
	asset.Data = map[string]any{
		"assetDataProvider": "Yahoo", "type": "EQUITY", "currency": asset.Currency,
		"symbol": asset.Symbol, "name": asset.Name, "price": asset.Price, "full_data": parsed,
	}
	return asset, nil
}

var historySpecs = map[string]struct{ rng, interval string }{
	sdk.Range1D:  {"1d", "5m"},
	sdk.Range5D:  {"5d", "15m"},
	sdk.Range1M:  {"1mo", "1h"},
	sdk.Range3M:  {"3mo", "1d"},
	sdk.Range1Y:  {"1y", "1d"},
	sdk.Range3Y:  {"3y", "1d"},
	sdk.Range5Y:  {"5y", "1d"},
	sdk.Range10Y: {"10y", "1d"},
	// "max" reaches as far back as the listing goes (decades for old
	// stocks); weekly bars keep the payload to a few thousand points.
	sdk.RangeMAX: {"max", "1wk"},
}

// History reads the v8 chart API.
func (a *API) History(ctx context.Context, symbol, rng string) (sdk.HistorySeries, error) {
	spec, ok := historySpecs[rng]
	if !ok {
		rng, spec = sdk.Range1M, historySpecs[sdk.Range1M]
	}
	formatted := strings.ToUpper(strings.TrimSpace(symbol))
	parsed, err := a.get(ctx, fmt.Sprintf("/v8/finance/chart/%s?range=%s&interval=%s&includePrePost=false&events=div%%2Csplit",
		url.PathEscape(formatted), spec.rng, spec.interval))
	if err != nil {
		return sdk.HistorySeries{}, fmt.Errorf("[YahooHistory] request failed for %s: %w", formatted, err)
	}
	chart, _ := parsed["chart"].(map[string]any)
	results, _ := chart["result"].([]any)
	if len(results) == 0 {
		return sdk.HistorySeries{}, fmt.Errorf("[YahooHistory] no result for %s", formatted)
	}
	first, _ := results[0].(map[string]any)
	meta, _ := first["meta"].(map[string]any)
	timestamps, _ := first["timestamp"].([]any)
	indicators, _ := first["indicators"].(map[string]any)
	quotes, _ := indicators["quote"].([]any)
	if len(timestamps) == 0 || len(quotes) == 0 {
		return sdk.HistorySeries{}, fmt.Errorf("[YahooHistory] empty series for %s", formatted)
	}
	q, _ := quotes[0].(map[string]any)
	closes, _ := q["close"].([]any)
	opens, _ := q["open"].([]any)
	highs, _ := q["high"].([]any)
	lows, _ := q["low"].([]any)
	volumes, _ := q["volume"].([]any)
	at := func(values []any, i int) float64 {
		if i < len(values) {
			if v, ok := values[i].(float64); ok {
				return v
			}
		}
		return 0
	}

	series := sdk.HistorySeries{
		Symbol: httpx.String(meta, "symbol"), Provider: "YAHOO", Range: rng,
		Interval: spec.interval, Currency: httpx.String(meta, "currency"),
	}
	if series.Symbol == "" {
		series.Symbol = formatted
	}
	for i, tsRaw := range timestamps {
		ts, ok := tsRaw.(float64)
		c := at(closes, i)
		if !ok || c <= 0 {
			continue
		}
		o, h, l := httpx.FillOHLC(at(opens, i), at(highs, i), at(lows, i), c)
		series.Points = append(series.Points, sdk.HistoryPoint{Timestamp: int64(ts), Open: o, High: h, Low: l, Close: c, Volume: at(volumes, i)})
	}
	if len(series.Points) == 0 {
		return sdk.HistorySeries{}, fmt.Errorf("[YahooHistory] no usable bars for %s", formatted)
	}
	return series, nil
}

// Search queries the symbol search.
func (a *API) Search(ctx context.Context, query string) ([]sdk.SearchResult, error) {
	parsed, err := a.get(ctx, "/v1/finance/search?q="+url.QueryEscape(query)+"&quotesCount=10&newsCount=0")
	if err != nil {
		return nil, fmt.Errorf("[YahooSearch] %w", err)
	}
	results := []sdk.SearchResult{}
	quotes, _ := parsed["quotes"].([]any)
	for _, raw := range quotes {
		m, ok := raw.(map[string]any)
		if !ok || httpx.String(m, "symbol") == "" {
			continue
		}
		name := httpx.String(m, "shortname")
		if name == "" {
			name = httpx.String(m, "longname")
		}
		results = append(results, sdk.SearchResult{
			Symbol: httpx.String(m, "symbol"), Name: name,
			Type: strings.ToUpper(httpx.String(m, "quoteType")), Exchange: httpx.String(m, "exchange"),
		})
	}
	return results, nil
}

// Financials reads fundamentals-timeseries quarterly statements.
func (a *API) Financials(ctx context.Context, symbol string, fromYear, toYear int) (sdk.FinancialsResponse, error) {
	formatted := strings.ToUpper(strings.TrimSpace(symbol))
	now := time.Now()
	params := url.Values{}
	params.Set("symbol", formatted)
	params.Set("type", strings.Join([]string{
		"quarterlyTotalRevenue", "quarterlyGrossProfit", "quarterlyOperatingIncome",
		"quarterlyNetIncome", "quarterlyDilutedEPS", "quarterlyFreeCashFlow",
	}, ","))
	start, end := now.AddDate(-5, 0, 0), now.AddDate(0, 0, 1)
	if fromYear != 0 || toYear != 0 {
		start = time.Date(fromYear, time.January, 1, 0, 0, 0, 0, time.UTC)
		end = time.Date(toYear+1, time.January, 1, 0, 0, 0, 0, time.UTC)
	}
	params.Set("period1", fmt.Sprint(start.Unix()))
	params.Set("period2", fmt.Sprint(end.Unix()))
	params.Set("merge", "false")
	parsed, err := a.get(ctx, "/ws/fundamentals-timeseries/v1/finance/timeseries/"+url.PathEscape(formatted)+"?"+params.Encode())
	if err != nil {
		return sdk.FinancialsResponse{}, fmt.Errorf("[YahooFinancials] request failed for %s: %w", formatted, err)
	}
	return ParseFinancials(parsed, formatted, fromYear, toYear), nil
}

// ParseFinancials converts a fundamentals-timeseries payload.
func ParseFinancials(parsed map[string]any, symbol string, fromYear, toYear int) sdk.FinancialsResponse {
	ts, _ := parsed["timeseries"].(map[string]any)
	results, _ := ts["result"].([]any)
	byDate := map[string]*sdk.QuarterlyFinancial{}
	for _, item := range results {
		entry, _ := item.(map[string]any)
		meta, _ := entry["meta"].(map[string]any)
		types, _ := meta["type"].([]any)
		if len(types) == 0 {
			continue
		}
		typeName, _ := types[0].(string)
		values, _ := entry[typeName].([]any)
		for _, raw := range values {
			v, _ := raw.(map[string]any)
			date := httpx.String(v, "asOfDate")
			rv, _ := v["reportedValue"].(map[string]any)
			num, ok := rv["raw"].(float64)
			if date == "" || !ok {
				continue
			}
			row := byDate[date]
			if row == nil {
				row = &sdk.QuarterlyFinancial{Period: sdk.CalendarQuarterLabel(date), PeriodBasis: "calendar", PeriodEnd: date, Currency: httpx.String(v, "currencyCode")}
				byDate[date] = row
			}
			val := num
			switch typeName {
			case "quarterlyTotalRevenue":
				row.Revenue = &val
			case "quarterlyGrossProfit":
				row.GrossProfit = &val
			case "quarterlyOperatingIncome":
				row.OperatingIncome = &val
			case "quarterlyNetIncome":
				row.NetIncome = &val
			case "quarterlyDilutedEPS":
				row.EPS = &val
			case "quarterlyFreeCashFlow":
				row.FreeCashFlow = &val
			}
		}
	}
	reports := make([]sdk.QuarterlyFinancial, 0, len(byDate))
	for _, row := range byDate {
		// Some Yahoo company fundamentals map the same EBIT metric to both
		// gross profit and operating income (e.g. CGI). Do not manufacture a
		// gross margin from an indistinguishable value.
		if row.GrossProfit != nil && row.OperatingIncome != nil && *row.GrossProfit == *row.OperatingIncome && *row.GrossProfit != 0 {
			row.GrossProfit = nil
			row.DataQualityWarnings = append(row.DataQualityWarnings, "gross_profit omitted: provider reported exactly the same value as operating_income; verify official statements")
		}
		reports = append(reports, *row)
	}
	sdk.SortByPeriodDesc(reports)
	reports = sdk.FilterFinancialYears(reports, fromYear, toYear)
	res := sdk.FinancialsResponse{FromYear: fromYear, ToYear: toYear, Symbol: symbol, Provider: "YAHOO", Source: "yahoo", Reports: reports}
	if len(reports) == 0 {
		res.Note = "No quarterly statements available for this instrument and requested reporting period."
	}
	return res
}

// News reads the headlines Yahoo's public search returns for a ticker.
func (a *API) News(ctx context.Context, symbol string, limit int) ([]sdk.NewsItem, error) {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	if symbol == "" {
		return nil, sdk.Errorf(sdk.CodeInvalidArgument, "symbol is required")
	}
	if limit < 1 || limit > 20 {
		limit = 5
	}
	parsed, err := a.get(ctx, "/v1/finance/search?q="+url.QueryEscape(symbol)+"&quotesCount=0&newsCount="+fmt.Sprint(limit))
	if err != nil {
		return nil, err
	}
	return ParseNews(parsed, symbol), nil
}

// ParseNews maps the search response's news array to news items.
func ParseNews(parsed map[string]any, symbol string) []sdk.NewsItem {
	out := []sdk.NewsItem{}
	list, _ := parsed["news"].([]any)
	for _, raw := range list {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		title, _ := item["title"].(string)
		link, _ := item["link"].(string)
		if title == "" || !strings.HasPrefix(link, "https://") {
			continue
		}
		publisher, _ := item["publisher"].(string)
		published := int64(0)
		if t, ok := item["providerPublishTime"].(float64); ok && t > 0 {
			published = int64(t)
		}
		out = append(out, sdk.NewsItem{Title: title, URL: link, Source: publisher, Published: published, Symbol: symbol, Provider: "YAHOO"})
	}
	return out
}
