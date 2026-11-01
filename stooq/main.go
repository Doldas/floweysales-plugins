// Command stooq is the FloweySales Stooq provider plugin: free daily bars for
// US tickers and a few indices, as a fallback when other providers are rate
// limited.
package main

import (
	"context"
	_ "embed"
	"encoding/csv"
	"errors"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	sdk "github.com/Doldas/floweysales/pluginsdk"
	"github.com/Doldas/floweysales/pluginsdk/httpx"
)

//go:embed plugin.json
var manifestJSON []byte

const defaultBase = "https://stooq.com"

type stooq struct {
	http *httpx.Client
	base string
	now  func() time.Time
}

func main() {
	sdk.Main(newPlugin(&stooq{http: httpx.New("FloweySales-Stooq-plugin/1.0", time.Second), base: defaultBase, now: time.Now}))
}

func newPlugin(s *stooq) sdk.Plugin {
	manifest, err := sdk.ParseManifest(manifestJSON)
	if err != nil {
		panic(err)
	}
	return sdk.Plugin{
		Manifest: manifest,
		Quote:    s.quote,
		History: func(ctx context.Context, p sdk.HistoryParams) (sdk.HistorySeries, error) {
			return s.history(ctx, p.Symbol, p.Range)
		},
	}
}

// indices maps Yahoo-style index symbols to Stooq's.
var indices = map[string]string{"^SPX": "^spx", "^GSPC": "^spx", "^DJI": "^dji", "^NDX": "^ndx", "^IXIC": "^ndq"}

var usTicker = regexp.MustCompile(`^[A-Z]{1,6}(-[A-Z]{1,2})?$`)

// stooqSymbol maps a Yahoo-style symbol to Stooq's: AAPL becomes aapl.us and
// BRK-B brk-b.us. currency is USD only for US listings and empty for indices.
func stooqSymbol(symbol string) (id, currency string, err error) {
	s := httpx.Upper(symbol)
	if id, ok := indices[s]; ok {
		return id, "", nil
	}
	if usTicker.MatchString(s) {
		return strings.ToLower(s) + ".us", "USD", nil
	}
	return "", "", sdk.Errorf(sdk.CodeInvalidArgument,
		"Stooq supports US tickers such as AAPL and the indices ^SPX, ^DJI, ^NDX and ^IXIC; %q is not supported", symbol)
}

// rangeDays is how far back each range reaches; Stooq serves daily bars only.
var rangeDays = map[string]int{
	sdk.Range1D: 3, sdk.Range5D: 7, sdk.Range1M: 31, sdk.Range3M: 92, sdk.Range1Y: 366,
	sdk.Range3Y: 1096, sdk.Range5Y: 1827, sdk.Range10Y: 3653, sdk.RangeMAX: 0,
}

// parseCSV reads Stooq's daily CSV: Date,Open,High,Low,Close,Volume. Rows
// with an unusable close are skipped. A body that is not that CSV (Stooq
// answers "No data" for unknown symbols) is an error.
func parseCSV(body []byte) ([]sdk.HistoryPoint, error) {
	rows, err := csv.NewReader(strings.NewReader(string(body))).ReadAll()
	if err != nil || len(rows) < 2 || len(rows[0]) < 5 || !strings.EqualFold(rows[0][0], "Date") {
		return nil, errors.New("no data")
	}
	col := map[string]int{}
	for i, name := range rows[0] {
		col[strings.ToLower(strings.TrimSpace(name))] = i
	}
	for _, need := range []string{"date", "open", "high", "low", "close"} {
		if _, ok := col[need]; !ok {
			return nil, errors.New("unexpected columns")
		}
	}
	num := func(row []string, name string) float64 {
		i, ok := col[name]
		if !ok || i >= len(row) {
			return 0
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(row[i]), 64)
		if err != nil || v < 0 {
			return 0
		}
		return v
	}
	points := make([]sdk.HistoryPoint, 0, len(rows)-1)
	for _, row := range rows[1:] {
		if len(row) <= col["date"] {
			continue
		}
		day, err := time.Parse("2006-01-02", strings.TrimSpace(row[col["date"]]))
		closePrice := num(row, "close")
		if err != nil || closePrice <= 0 {
			continue
		}
		o, h, l := httpx.FillOHLC(num(row, "open"), num(row, "high"), num(row, "low"), closePrice)
		points = append(points, sdk.HistoryPoint{Timestamp: day.Unix(), Open: o, High: h, Low: l, Close: closePrice, Volume: num(row, "volume")})
	}
	sort.Slice(points, func(i, j int) bool { return points[i].Timestamp < points[j].Timestamp })
	return points, nil
}

func (s *stooq) daily(ctx context.Context, symbol string) ([]sdk.HistoryPoint, string, string, error) {
	id, currency, err := stooqSymbol(symbol)
	if err != nil {
		return nil, "", "", err
	}
	body, err := s.http.Get(ctx, s.base+"/q/d/l/?"+url.Values{"s": {id}, "i": {"d"}}.Encode(), nil)
	if err != nil {
		return nil, "", "", sdk.Errorf(sdk.CodeUpstream, "Stooq request failed for %s", httpx.Upper(symbol))
	}
	points, err := parseCSV(body)
	if err != nil || len(points) == 0 {
		return nil, "", "", sdk.Errorf(sdk.CodeUpstream, "Stooq has no data for %s", httpx.Upper(symbol))
	}
	return points, id, currency, nil
}

func (s *stooq) history(ctx context.Context, symbol, rng string) (sdk.HistorySeries, error) {
	days, ok := rangeDays[rng]
	if !ok {
		rng, days = sdk.Range1M, rangeDays[sdk.Range1M]
	}
	points, _, currency, err := s.daily(ctx, symbol)
	if err != nil {
		return sdk.HistorySeries{}, err
	}
	if days > 0 {
		cutoff := time.Unix(points[len(points)-1].Timestamp, 0).UTC().AddDate(0, 0, -days).Unix()
		i := sort.Search(len(points), func(i int) bool { return points[i].Timestamp >= cutoff })
		points = points[i:]
	}
	return sdk.HistorySeries{Symbol: httpx.Upper(symbol), Provider: "STOOQ", Range: rng, Interval: "1d", Currency: currency, Points: points}, nil
}

func (s *stooq) quote(ctx context.Context, p sdk.QuoteParams) (sdk.Asset, error) {
	points, _, currency, err := s.daily(ctx, p.Symbol)
	if err != nil {
		return sdk.Asset{}, err
	}
	last := points[len(points)-1]
	symbol := httpx.Upper(p.Symbol)
	a := sdk.Asset{Symbol: symbol, Name: symbol, Price: last.Close, Currency: currency, Provider: "STOOQ"}
	a.Data = map[string]any{"assetDataProvider": "Stooq", "type": "EQUITY", "symbol": symbol, "name": symbol, "price": last.Close, "currency": currency,
		"open": last.Open, "high": last.High, "low": last.Low, "lastUpdated": last.Timestamp}
	if len(points) > 1 && points[len(points)-2].Close > 0 {
		prev := points[len(points)-2].Close
		a.Data["previousClose"] = prev
		a.Data["changePercent"] = (last.Close - prev) / prev * 100
	}
	return a, nil
}
