// Command coingecko is the FloweySales CoinGecko provider plugin. It reads
// the public API; COINGECKO_API_KEY is an optional demo key.
package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	sdk "github.com/Doldas/floweysales/pluginsdk"
	"github.com/Doldas/floweysales/pluginsdk/httpx"
)

//go:embed plugin.json
var manifestJSON []byte

const defaultBase = "https://api.coingecko.com"

// coinID is CoinGecko's identifier format, for example bitcoin or usd-coin.
var coinID = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,99}$`)

type coingecko struct {
	http *httpx.Client
	base string
	key  string
}

func main() {
	sdk.Main(newPlugin(&coingecko{http: httpx.New("FloweySales-CoinGecko-plugin/1.0", time.Second), base: defaultBase, key: strings.TrimSpace(os.Getenv("COINGECKO_API_KEY"))}))
}

func newPlugin(c *coingecko) sdk.Plugin {
	manifest, err := sdk.ParseManifest(manifestJSON)
	if err != nil {
		panic(err)
	}
	return sdk.Plugin{
		Manifest: manifest,
		Quote:    c.quote,
		History: func(ctx context.Context, p sdk.HistoryParams) (sdk.HistorySeries, error) {
			return c.history(ctx, p.Symbol, p.Range)
		},
		Search: func(ctx context.Context, p sdk.SearchParams) ([]sdk.SearchResult, error) {
			return c.search(ctx, p.Query)
		},
	}
}

// get calls the API. The demo key travels in a header, never in the URL, and
// errors never carry the request URL.
func (c *coingecko) get(ctx context.Context, path string, out any) error {
	headers := map[string]string{}
	if c.key != "" {
		headers["x-cg-demo-api-key"] = c.key
	}
	body, err := c.http.Get(ctx, c.base+path, headers)
	if err != nil {
		var status *httpx.StatusError
		if errors.As(err, &status) {
			switch status.Status {
			case 404:
				return sdk.Errorf(sdk.CodeInvalidArgument, "CoinGecko does not know that coin ID")
			case 429:
				return sdk.Errorf(sdk.CodeUpstream, "CoinGecko rate limit reached; try again in a minute or set COINGECKO_API_KEY")
			}
			return sdk.Errorf(sdk.CodeUpstream, "CoinGecko answered HTTP %d", status.Status)
		}
		return sdk.Errorf(sdk.CodeUpstream, "CoinGecko request failed")
	}
	if err := json.Unmarshal(body, out); err != nil {
		return sdk.Errorf(sdk.CodeUpstream, "parse CoinGecko response: %v", err)
	}
	return nil
}

func normalize(symbol string) (string, error) {
	id := strings.ToLower(strings.TrimSpace(symbol))
	if !coinID.MatchString(id) {
		return "", sdk.Errorf(sdk.CodeInvalidArgument, "%q is not a CoinGecko coin ID such as bitcoin", symbol)
	}
	return id, nil
}

func (c *coingecko) quote(ctx context.Context, p sdk.QuoteParams) (sdk.Asset, error) {
	id, err := normalize(p.Symbol)
	if err != nil {
		return sdk.Asset{}, err
	}
	var parsed map[string]map[string]float64
	q := url.Values{"ids": {id}, "vs_currencies": {"usd"}, "include_24hr_change": {"true"}}
	if err := c.get(ctx, "/api/v3/simple/price?"+q.Encode(), &parsed); err != nil {
		return sdk.Asset{}, err
	}
	row, ok := parsed[id]
	price := row["usd"]
	if !ok || price <= 0 {
		return sdk.Asset{}, sdk.Errorf(sdk.CodeInvalidArgument, "CoinGecko has no USD price for %s", id)
	}
	a := sdk.Asset{Symbol: id, Name: id, Price: price, Currency: "USD", Provider: "COINGECKO"}
	a.Data = map[string]any{"assetDataProvider": "CoinGecko", "type": "CRYPTO", "symbol": id, "name": id, "price": price, "currency": "USD"}
	if change, ok := row["usd_24h_change"]; ok {
		a.Data["changePercent24h"] = change
	}
	return a, nil
}

// freeHistoryDays is how far back the free API reaches.
const freeHistoryDays = 365

// chartSpecs maps a range to the market_chart days parameter and the
// resolution CoinGecko serves for it (5-minute for 1 day, hourly up to 90
// days, daily beyond).
var chartSpecs = map[string]struct {
	days     int
	interval string
	served   string // the range the data actually covers
}{
	sdk.Range1D:  {1, "5m", sdk.Range1D},
	sdk.Range5D:  {5, "1h", sdk.Range5D},
	sdk.Range1M:  {30, "1h", sdk.Range1M},
	sdk.Range3M:  {90, "1h", sdk.Range3M},
	sdk.Range1Y:  {365, "1d", sdk.Range1Y},
	sdk.Range3Y:  {freeHistoryDays, "1d", sdk.Range1Y},
	sdk.Range5Y:  {freeHistoryDays, "1d", sdk.Range1Y},
	sdk.Range10Y: {freeHistoryDays, "1d", sdk.Range1Y},
	sdk.RangeMAX: {freeHistoryDays, "1d", sdk.Range1Y},
}

func (c *coingecko) history(ctx context.Context, symbol, rng string) (sdk.HistorySeries, error) {
	id, err := normalize(symbol)
	if err != nil {
		return sdk.HistorySeries{}, err
	}
	spec, ok := chartSpecs[rng]
	if !ok {
		rng, spec = sdk.Range1M, chartSpecs[sdk.Range1M]
	}
	var parsed struct {
		Prices       [][2]float64 `json:"prices"`
		TotalVolumes [][2]float64 `json:"total_volumes"`
	}
	q := url.Values{"vs_currency": {"usd"}, "days": {fmt.Sprint(spec.days)}}
	if err := c.get(ctx, "/api/v3/coins/"+url.PathEscape(id)+"/market_chart?"+q.Encode(), &parsed); err != nil {
		return sdk.HistorySeries{}, err
	}
	volume := map[int64]float64{}
	for _, v := range parsed.TotalVolumes {
		volume[int64(v[0])/1000] = v[1]
	}
	points := make([]sdk.HistoryPoint, 0, len(parsed.Prices))
	for _, p := range parsed.Prices {
		price, ts := p[1], int64(p[0])/1000
		if price <= 0 || ts <= 0 {
			continue
		}
		points = append(points, sdk.HistoryPoint{Timestamp: ts, Open: price, High: price, Low: price, Close: price, Volume: volume[ts]})
	}
	if len(points) == 0 {
		return sdk.HistorySeries{}, sdk.Errorf(sdk.CodeUpstream, "CoinGecko returned no prices for %s", id)
	}
	return sdk.HistorySeries{Symbol: id, Provider: "COINGECKO", Range: spec.served, Interval: spec.interval, Currency: "USD", Points: points}, nil
}

func (c *coingecko) search(ctx context.Context, query string) ([]sdk.SearchResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return []sdk.SearchResult{}, nil
	}
	var parsed struct {
		Coins []struct {
			ID     string `json:"id"`
			Name   string `json:"name"`
			Symbol string `json:"symbol"`
		} `json:"coins"`
	}
	if err := c.get(ctx, "/api/v3/search?"+url.Values{"query": {query}}.Encode(), &parsed); err != nil {
		return nil, err
	}
	out := []sdk.SearchResult{}
	for _, coin := range parsed.Coins {
		if !coinID.MatchString(coin.ID) {
			continue
		}
		out = append(out, sdk.SearchResult{Symbol: coin.ID, Name: coin.Name + " (" + strings.ToUpper(coin.Symbol) + ")", Type: "CRYPTO", Exchange: "CoinGecko"})
		if len(out) == 10 {
			break
		}
	}
	return out, nil
}
