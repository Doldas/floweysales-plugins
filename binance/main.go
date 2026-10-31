// Command binance is the FloweySales Binance provider plugin.
package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	sdk "github.com/Doldas/floweysales/pluginsdk"
	"github.com/Doldas/floweysales/pluginsdk/httpx"
	"github.com/Doldas/floweysales/pluginsdk/research"
)

//go:embed plugin.json
var manifestJSON []byte

func main() {
	sdk.Main(newPlugin(&binance{
		http: httpx.New("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36", 0),
		api:  "https://api.binance.com", data: "https://data-api.binance.vision",
	}))
}

// binance holds the two public hosts: api for trading data the tracker
// polls, data for the market-data-only research mirror.
type binance struct {
	http      *httpx.Client
	api, data string

	mu        sync.Mutex
	fetchedAt time.Time
	symbols   []sdk.SearchResult
}

func newPlugin(b *binance) sdk.Plugin {
	manifest, err := sdk.ParseManifest(manifestJSON)
	if err != nil {
		panic(err)
	}
	return sdk.Plugin{
		Manifest: manifest,
		Quote:    b.quote,
		History: func(ctx context.Context, p sdk.HistoryParams) (sdk.HistorySeries, error) {
			return b.history(ctx, p.Symbol, p.Range)
		},
		Search: func(ctx context.Context, p sdk.SearchParams) ([]sdk.SearchResult, error) {
			return b.search(ctx, p.Query)
		},
		Financials: func(_ context.Context, p sdk.FinancialsParams) (sdk.FinancialsResponse, error) {
			return sdk.FinancialsResponse{
				Symbol: p.Symbol, Provider: "BINANCE", Source: "none", FromYear: p.FromYear, ToYear: p.ToYear,
				Reports: []sdk.QuarterlyFinancial{}, Note: "Crypto pairs have no quarterly financial reports.",
			}, nil
		},
		Research: func(ctx context.Context, p sdk.ResearchParams) (sdk.ResearchResult, error) {
			return b.research(ctx, manifest, p)
		},
	}
}

func (b *binance) quote(ctx context.Context, p sdk.QuoteParams) (sdk.Asset, error) {
	formatted := httpx.Upper(p.Symbol)
	body, err := b.http.Get(ctx, b.api+"/api/v3/ticker/price?symbol="+url.QueryEscape(formatted), nil)
	if err != nil {
		return sdk.Asset{}, fmt.Errorf("[Binance] request failed or symbol not found: %s: %w", formatted, err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		return sdk.Asset{}, fmt.Errorf("[Binance] failed to parse asset for %s: %w", formatted, err)
	}
	a := sdk.Asset{Symbol: httpx.String(parsed, "symbol"), Price: httpx.Float(parsed, "price"), Currency: "USD", Provider: "BINANCE"}
	a.Name = a.Symbol
	a.Data = map[string]any{"assetDataProvider": "Binance", "type": "CRYPTO", "symbol": a.Symbol, "name": a.Name, "price": a.Price, "currency": a.Currency}
	return a, nil
}

var klineSpecs = map[string]struct {
	interval        string
	limit, wantBars int
}{
	sdk.Range1D:  {"5m", 288, 288},
	sdk.Range5D:  {"15m", 480, 480},
	sdk.Range1M:  {"1h", 720, 720},
	sdk.Range3M:  {"4h", 540, 540},
	sdk.Range1Y:  {"1d", 1000, 366},
	sdk.Range3Y:  {"1d", 1000, 1100},
	sdk.Range5Y:  {"1d", 1000, 1830},
	sdk.Range10Y: {"1d", 1000, 3660},
	sdk.RangeMAX: {"1w", 1000, 2000},
}

func (b *binance) history(ctx context.Context, symbol, rng string) (sdk.HistorySeries, error) {
	formatted := httpx.Upper(symbol)
	spec, ok := klineSpecs[rng]
	if !ok {
		rng, spec = sdk.Range1M, klineSpecs[sdk.Range1M]
	}
	// Walk backwards with endTime pagination; each request returns at most
	// 1000 klines.
	var points []sdk.HistoryPoint
	endTime := int64(0)
	const maxPages = 8
	for page := 0; page < maxPages; page++ {
		endpoint := fmt.Sprintf("%s/api/v3/klines?symbol=%s&interval=%s&limit=%d", b.api, url.QueryEscape(formatted), spec.interval, spec.limit)
		if endTime > 0 {
			endpoint += fmt.Sprintf("&endTime=%d", endTime)
		}
		body, err := b.http.Get(ctx, endpoint, nil)
		if err != nil {
			if page == 0 {
				return sdk.HistorySeries{}, fmt.Errorf("[BinanceHistory] request failed for %s: %w", formatted, err)
			}
			break
		}
		var raw [][]any
		if err := json.Unmarshal(body, &raw); err != nil {
			if page == 0 {
				return sdk.HistorySeries{}, fmt.Errorf("[BinanceHistory] parse failed for %s: %w", formatted, err)
			}
			break
		}
		pagePts := parseKlines(raw)
		if len(pagePts) == 0 {
			break
		}
		points = append(pagePts, points...)
		if len(points) >= spec.wantBars || len(raw) < spec.limit {
			break
		}
		// Timestamps are normalized to seconds; Binance wants milliseconds.
		endTime = pagePts[0].Timestamp*1000 - 1
	}
	if len(points) == 0 {
		return sdk.HistorySeries{}, fmt.Errorf("[BinanceHistory] no klines for %s", formatted)
	}
	if maxKeep := spec.wantBars + spec.limit; len(points) > maxKeep {
		points = points[len(points)-maxKeep:]
	}
	return sdk.HistorySeries{Symbol: formatted, Provider: "BINANCE", Range: rng, Interval: spec.interval, Currency: "USDT", Points: points}, nil
}

// parseKlines converts raw kline arrays. Klines carry the open time as a
// number and prices/volume as strings (e.g. "81517.39000000").
func parseKlines(raw [][]any) []sdk.HistoryPoint {
	var out []sdk.HistoryPoint
	for _, k := range raw {
		if len(k) < 7 {
			continue
		}
		c := httpx.ToFloat(k[4])
		if c <= 0 {
			continue
		}
		out = append(out, sdk.HistoryPoint{
			Timestamp: int64(httpx.ToFloat(k[0]) / 1000),
			Open:      httpx.ToFloat(k[1]), High: httpx.ToFloat(k[2]), Low: httpx.ToFloat(k[3]), Close: c, Volume: httpx.ToFloat(k[5]),
		})
	}
	return out
}

// exchangeSymbols caches exchangeInfo for ten minutes.
func (b *binance) exchangeSymbols(ctx context.Context) ([]sdk.SearchResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.symbols != nil && time.Since(b.fetchedAt) < 10*time.Minute {
		return b.symbols, nil
	}
	body, err := b.http.Get(ctx, b.api+"/api/v3/exchangeInfo", nil)
	if err != nil {
		return nil, fmt.Errorf("[BinanceSearch] %w", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("[BinanceSearch] parse: %w", err)
	}
	symbols := []sdk.SearchResult{}
	arr, _ := parsed["symbols"].([]any)
	for _, item := range arr {
		m, ok := item.(map[string]any)
		if !ok || httpx.String(m, "symbol") == "" {
			continue
		}
		if status := httpx.String(m, "status"); status != "" && status != "TRADING" {
			continue
		}
		symbols = append(symbols, sdk.SearchResult{
			Symbol: httpx.String(m, "symbol"), Name: httpx.String(m, "baseAsset") + " / " + httpx.String(m, "quoteAsset"),
			Type: "CRYPTO", Exchange: "Binance",
		})
	}
	b.symbols, b.fetchedAt = symbols, time.Now()
	return symbols, nil
}

func (b *binance) search(ctx context.Context, query string) ([]sdk.SearchResult, error) {
	symbols, err := b.exchangeSymbols(ctx)
	if err != nil {
		return nil, err
	}
	q := httpx.Upper(query)
	results := []sdk.SearchResult{}
	for _, s := range symbols {
		if strings.HasPrefix(s.Symbol, q) {
			results = append(results, s)
		}
		if len(results) >= 10 {
			break
		}
	}
	return results, nil
}

func (b *binance) research(ctx context.Context, manifest sdk.Manifest, p sdk.ResearchParams) (sdk.ResearchResult, error) {
	op, err := research.Find(manifest, p.Operation)
	if err != nil {
		return sdk.ResearchResult{}, err
	}
	q, err := research.Query(op, p, research.CryptoSymbol)
	if err != nil {
		return sdk.ResearchResult{}, err
	}
	switch op.Operation {
	case "recent_trades", "aggregate_trades":
		q.Set("limit", "50")
	case "order_book":
		q.Set("limit", "20")
	}
	endpoint := b.data + op.Path
	if encoded := q.Encode(); encoded != "" {
		endpoint += "?" + encoded
	}
	data, err := research.FetchJSON(ctx, http.DefaultClient, "BINANCE", endpoint, nil)
	if err != nil {
		return sdk.ResearchResult{}, err
	}
	return research.Result("BINANCE", op, p.Symbol, "https://data-api.binance.vision"+op.Path, data, ""), nil
}
