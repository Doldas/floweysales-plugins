// Command avanza is the experimental FloweySales Avanza provider plugin. It
// reads unofficial public endpoints only; installing it requires accepting
// the terms in plugin.json.
package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/Doldas/floweysales-plugins/internal/yahooapi"
	sdk "github.com/Doldas/floweysales/pluginsdk"
	"github.com/Doldas/floweysales/pluginsdk/httpx"
	"github.com/Doldas/floweysales/pluginsdk/research"
)

//go:embed plugin.json
var manifestJSON []byte

func main() {
	sdk.Main(newPlugin(&avanza{
		// Public requests are spaced about one second apart per process.
		http:  httpx.New("FloweySales/1.0 (unofficial public market-data research)", time.Second),
		base:  "https://www.avanza.se",
		yahoo: yahooapi.New(),
	}))
}

type avanza struct {
	http  *httpx.Client
	base  string
	yahoo *yahooapi.API
}

var headers = map[string]string{"Accept": "application/json, text/plain, */*"}

func newPlugin(a *avanza) sdk.Plugin {
	manifest, err := sdk.ParseManifest(manifestJSON)
	if err != nil {
		panic(err)
	}
	return sdk.Plugin{
		Manifest: manifest,
		Quote: func(ctx context.Context, p sdk.QuoteParams) (sdk.Asset, error) {
			return a.instrument(ctx, strings.TrimSpace(p.Symbol))
		},
		History: func(ctx context.Context, p sdk.HistoryParams) (sdk.HistorySeries, error) {
			return a.history(ctx, strings.TrimSpace(p.Symbol), p.Range)
		},
		Search: func(ctx context.Context, p sdk.SearchParams) ([]sdk.SearchResult, error) {
			return a.search(ctx, p.Query, 10)
		},
		Financials: a.financials,
		Research: func(ctx context.Context, p sdk.ResearchParams) (sdk.ResearchResult, error) {
			op, err := research.Find(manifest, p.Operation)
			if err != nil {
				return sdk.ResearchResult{}, err
			}
			if _, err := research.Query(op, p, research.EquitySymbol); err != nil {
				return sdk.ResearchResult{}, err
			}
			asset, err := a.instrument(ctx, strings.TrimSpace(p.Symbol))
			if err != nil {
				return sdk.ResearchResult{}, err
			}
			return research.Result("AVANZA", op, p.Symbol, "unofficial Avanza public instrument endpoint", asset.Data,
				"Unofficial read-only Avanza data; may be delayed/incomplete; provider usage conditions apply."), nil
		},
		ForumPosts: func(ctx context.Context, p sdk.ForumParams) ([]sdk.ForumPost, error) {
			return a.forum(ctx, strings.TrimSpace(p.Symbol), p.Limit)
		},
	}
}

type hit struct {
	id, kind string // kind is the hit type: FUND or STOCK
	result   sdk.SearchResult
}

func (a *avanza) searchHits(ctx context.Context, query string, size int) ([]hit, error) {
	payload := map[string]any{
		"query":        query,
		"searchFilter": map[string]any{"types": []string{"FUND", "STOCK"}},
		"pagination":   map[string]any{"from": 0, "size": size},
	}
	body, err := a.http.PostJSON(ctx, a.base+"/_api/search/filtered-search", payload, headers)
	if err != nil {
		return nil, fmt.Errorf("[Avanza] search failed for %s: %w", query, err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("[Avanza] parse filtered-search: %w", err)
	}
	var hits []hit
	raw, _ := parsed["hits"].([]any)
	for _, h := range raw {
		m, ok := h.(map[string]any)
		if !ok {
			continue
		}
		id := httpx.String(m, "orderBookId")
		if id == "" {
			id = httpx.String(m, "id")
		}
		if id == "" {
			continue
		}
		name := httpx.String(m, "title")
		if name == "" {
			name = httpx.String(m, "name")
		}
		kind := strings.ToUpper(httpx.String(m, "type"))
		hits = append(hits, hit{id: id, kind: kind, result: sdk.SearchResult{Symbol: id, Name: name, Type: kind, Exchange: httpx.String(m, "marketPlaceName")}})
	}
	return hits, nil
}

func (a *avanza) search(ctx context.Context, query string, size int) ([]sdk.SearchResult, error) {
	hits, err := a.searchHits(ctx, query, size)
	if err != nil {
		return nil, err
	}
	out := make([]sdk.SearchResult, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.result)
	}
	return out, nil
}

// resolve turns a query into an orderbook id and whether it is a fund.
// Numeric queries are ids already; their kind is unknown (isFund false).
func (a *avanza) resolve(ctx context.Context, query string) (id string, isFund bool, err error) {
	if httpx.IsDigits(query) {
		return query, false, nil
	}
	hits, err := a.searchHits(ctx, query, 5)
	if err != nil {
		return "", false, err
	}
	if len(hits) == 0 {
		return "", false, sdk.Errorf(sdk.CodeInvalidArgument, "[Avanza] no instrument found for %s", query)
	}
	return hits[0].id, hits[0].kind == "FUND", nil
}

func (a *avanza) getJSON(ctx context.Context, path string) (map[string]any, error) {
	body, err := a.http.Get(ctx, a.base+path, headers)
	if err != nil {
		return nil, err
	}
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("[Avanza] parse %s: %w", path, err)
	}
	return parsed, nil
}

// instrument fetches a fund guide or stock market guide snapshot.
func (a *avanza) instrument(ctx context.Context, query string) (sdk.Asset, error) {
	id, isFund, err := a.resolve(ctx, query)
	if err != nil {
		return sdk.Asset{}, err
	}
	if isFund {
		j, err := a.getJSON(ctx, "/_api/fund-guide/guide/"+url.PathEscape(id))
		if err != nil {
			return sdk.Asset{}, fmt.Errorf("[Avanza] fund guide failed for %s: %w", id, err)
		}
		return fundAsset(j), nil
	}
	j, err := a.getJSON(ctx, "/_api/market-guide/stock/"+url.PathEscape(id))
	if err != nil {
		return sdk.Asset{}, fmt.Errorf("[Avanza] market guide failed for %s: %w", id, err)
	}
	return stockAsset(j), nil
}

func fundAsset(j map[string]any) sdk.Asset {
	f := func(k string) float64 { return httpx.Float(j, k) }
	s := func(k string) string { return httpx.String(j, k) }
	holdings := [][2]any{}
	if hcd, ok := j["holdingChartData"].([]any); ok {
		for _, item := range hcd {
			if m, ok := item.(map[string]any); ok {
				holdings = append(holdings, [2]any{httpx.String(m, "name"), httpx.Float(m, "y")})
			}
		}
	}
	a := sdk.Asset{Symbol: s("isin"), Name: s("name"), Price: f("nav"), Currency: s("currency"), Provider: "AVANZA"}
	a.Data = map[string]any{
		"assetDataProvider": "Avanza", "type": "FUND",
		"symbol": a.Symbol, "name": a.Name, "price": a.Price, "isin": s("isin"),
		"description": s("description"), "navDate": s("navDate"), "currency": a.Currency, "rating": int(f("rating")),
		"fees": map[string]any{"product": f("productFee"), "management": f("managementFee")},
		"risk": map[string]any{"value": int(f("risk")), "text": s("riskText")},
		"performance": map[string]any{
			"1d": f("developmentOneDay"), "1m": f("developmentOneMonth"), "3m": f("developmentThreeMonths"),
			"6m": f("developmentSixMonths"), "1y": f("developmentOneYear"), "thisYear": f("developmentThisYear"),
			"3y": f("developmentThreeYears"), "5y": f("developmentFiveYears"),
		},
		"esg": map[string]any{
			"score": f("esgScore"), "rating": int(f("sustainabilityRating")), "lowCarbon": httpx.Bool(j, "lowCarbon"),
			"environmentalScore": f("environmentalScore"), "socialScore": f("socialScore"),
			"governanceScore": f("governanceScore"), "carbonRiskScore": f("carbonRiskScore"),
			"category": s("sustainabilityRatingCategoryName"), "svanen": httpx.Bool(j, "svanen"),
		},
		"holdings": holdings,
		"extended_info": map[string]any{
			"indexFund": httpx.Bool(j, "indexFund"), "sharpeRatio": f("sharpeRatio"), "standardDeviation": f("standardDeviation"),
			"capital": f("capital"), "primaryBenchmark": s("primaryBenchmark"), "recommendedHoldingPeriod": s("recommendedHoldingPeriod"),
		},
	}
	return a
}

func stockAsset(j map[string]any) sdk.Asset {
	a := sdk.Asset{Name: httpx.String(j, "name"), Provider: "AVANZA"}
	listing, _ := j["listing"].(map[string]any)
	quote, _ := j["quote"].(map[string]any)
	keyIndicators, _ := j["keyIndicators"].(map[string]any)
	a.Symbol = httpx.String(listing, "tickerSymbol")
	a.Currency = httpx.String(listing, "currency")
	a.Price = httpx.Float(quote, "last") // last paid
	a.Data = map[string]any{
		"assetDataProvider": "Avanza", "type": "STOCK", "orderbookId": httpx.String(j, "orderbookId"), "isin": httpx.String(j, "isin"),
		"symbol": a.Symbol, "name": a.Name, "price": a.Price, "currency": a.Currency,
		"quote": quote, "keyIndicators": keyIndicators, "listing": listing,
	}
	return a
}

var timePeriods = map[string]string{
	sdk.Range1D: "ONE_DAY", sdk.Range5D: "ONE_WEEK", sdk.Range1M: "ONE_MONTH", sdk.Range3M: "THREE_MONTHS",
	sdk.Range1Y: "ONE_YEAR", sdk.Range3Y: "THREE_YEARS", sdk.Range5Y: "FIVE_YEARS", sdk.Range10Y: "MAX", sdk.RangeMAX: "MAX",
}

func (a *avanza) history(ctx context.Context, query, rng string) (sdk.HistorySeries, error) {
	id, isFund, err := a.resolve(ctx, query)
	if err != nil {
		return sdk.HistorySeries{}, err
	}
	period, ok := timePeriods[rng]
	if !ok {
		rng, period = sdk.Range1M, timePeriods[sdk.Range1M]
	}
	kind := "stock"
	if isFund {
		kind = "fund"
	}
	chart, err := a.getJSON(ctx, fmt.Sprintf("/_api/price-chart/%s/%s?timePeriod=%s", kind, url.PathEscape(id), period))
	if err != nil && kind == "stock" {
		// Plain numeric ids default to stock; retry as fund before giving up.
		kind = "fund"
		chart, err = a.getJSON(ctx, fmt.Sprintf("/_api/price-chart/fund/%s?timePeriod=%s", url.PathEscape(id), period))
	}
	if err == nil {
		if series := parseChart(chart, id, rng, kind); len(series.Points) > 0 {
			return series, nil
		}
	}
	return a.yahooFallback(ctx, id, rng)
}

func parseChart(chart map[string]any, id, rng, kind string) sdk.HistorySeries {
	series := sdk.HistorySeries{Symbol: id, Provider: "AVANZA", Range: rng, Interval: kind}
	candles, _ := chart["candles"].([]any)
	for _, item := range candles {
		m, _ := item.(map[string]any)
		c := httpx.Float(m, "c")
		if c <= 0 {
			continue
		}
		ts := httpx.Int(m, "t")
		// The API has served both seconds and milliseconds; normalize.
		if ts > 1_000_000_000_000 {
			ts /= 1000
		}
		series.Points = append(series.Points, sdk.HistoryPoint{Timestamp: ts, Open: httpx.Float(m, "o"), High: httpx.Float(m, "h"), Low: httpx.Float(m, "l"), Close: c})
	}
	return series
}

// yahooTicker maps an Avanza instrument to its Yahoo-compatible ticker via
// the public market guide (e.g. "VOLV B" on Stockholmsbörsen → "VOLV-B.ST").
func (a *avanza) yahooTicker(ctx context.Context, id string) (string, error) {
	guide, err := a.getJSON(ctx, "/_api/market-guide/stock/"+url.PathEscape(id))
	if err != nil {
		return "", fmt.Errorf("[Avanza] market guide failed for %s: %w", id, err)
	}
	listing, _ := guide["listing"].(map[string]any)
	ticker := strings.ReplaceAll(httpx.String(listing, "tickerSymbol"), " ", "-")
	if ticker == "" {
		return "", fmt.Errorf("[Avanza] no Yahoo-compatible ticker for %s", id)
	}
	if strings.Contains(httpx.String(listing, "marketPlaceName"), "Stockholmsb") {
		ticker += ".ST"
	}
	return ticker, nil
}

// yahooFallback serves history through Yahoo Finance when Avanza's anonymous
// price-chart API refuses. The data keeps its actual Yahoo origin; it is
// never reported as direct AVANZA market data.
func (a *avanza) yahooFallback(ctx context.Context, id, rng string) (sdk.HistorySeries, error) {
	ticker, err := a.yahooTicker(ctx, id)
	if err != nil {
		return sdk.HistorySeries{}, err
	}
	series, err := a.yahoo.History(ctx, ticker, rng)
	if err != nil {
		return sdk.HistorySeries{}, fmt.Errorf("[Avanza] Yahoo fallback failed for %s (→ %s): %w", id, ticker, err)
	}
	series.Provider = "YAHOO"
	return series, nil
}

// financials resolves the Yahoo ticker through the market guide (which only
// accepts orderbook ids, so tickers go through search first).
func (a *avanza) financials(ctx context.Context, p sdk.FinancialsParams) (sdk.FinancialsResponse, error) {
	symbol := strings.TrimSpace(p.Symbol)
	id, _, err := a.resolve(ctx, symbol)
	if err != nil {
		return sdk.FinancialsResponse{}, err
	}
	ticker, err := a.yahooTicker(ctx, id)
	if err != nil {
		return sdk.FinancialsResponse{}, err
	}
	res, err := a.yahoo.Financials(ctx, ticker, p.FromYear, p.ToYear)
	if err != nil {
		return sdk.FinancialsResponse{}, err
	}
	res.Symbol, res.Provider = symbol, "AVANZA"
	return res, nil
}

func (a *avanza) forum(ctx context.Context, query string, limit int) ([]sdk.ForumPost, error) {
	posts := []sdk.ForumPost{}
	if query == "" {
		return posts, nil
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	id, _, err := a.resolve(ctx, query)
	if err != nil {
		return nil, err
	}
	body, err := a.http.Get(ctx, fmt.Sprintf("%s/_api/market-guide/forum/%s?limit=%d", a.base, url.PathEscape(id), limit), headers)
	if err != nil {
		return nil, fmt.Errorf("[Avanza] forum posts failed for %s: %w", id, err)
	}
	var raw any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("[Avanza] parse forum posts: %w", err)
	}
	var items []any
	switch t := raw.(type) {
	case []any:
		items = t
	case map[string]any:
		items, _ = t["posts"].([]any)
	}
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		post := sdk.ForumPost{ID: httpx.Int(m, "id"), Title: "No Title", Symbol: query, Author: "Avanza Trader"}
		if user, ok := m["user"].(map[string]any); ok && httpx.String(user, "username") != "" {
			post.Author = httpx.String(user, "username")
		} else if un := httpx.String(m, "username"); un != "" {
			post.Author = un
		}
		if t := httpx.String(m, "title"); t != "" {
			post.Title = t
		} else if s := httpx.String(m, "subject"); s != "" {
			post.Title = s
		}
		post.Content = httpx.String(m, "body")
		if post.Content == "" {
			post.Content = httpx.String(m, "message")
		}
		post.Upvotes = httpx.Int(m, "likesCount")
		if post.Upvotes == 0 {
			post.Upvotes = httpx.Int(m, "upvotes")
		}
		if created, ok := m["created"]; ok {
			post.Timestamp = created
		} else {
			post.Timestamp = time.Now().Unix()
		}
		posts = append(posts, post)
	}
	return posts, nil
}
