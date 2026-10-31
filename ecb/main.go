// Command ecb is the FloweySales plugin for ECB euro reference exchange rates.
package main

import (
	"context"
	_ "embed"
	"encoding/csv"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	sdk "github.com/Doldas/floweysales/pluginsdk"
	"github.com/Doldas/floweysales/pluginsdk/httpx"
)

//go:embed plugin.json
var manifestJSON []byte

const defaultBase = "https://data-api.ecb.europa.eu"

// currencies are the ECB reference rate currencies (against EUR).
var currencies = map[string]string{
	"USD": "US dollar", "JPY": "Japanese yen", "BGN": "Bulgarian lev", "CZK": "Czech koruna", "DKK": "Danish krone",
	"GBP": "Pound sterling", "HUF": "Hungarian forint", "PLN": "Polish zloty", "RON": "Romanian leu", "SEK": "Swedish krona",
	"CHF": "Swiss franc", "ISK": "Icelandic krona", "NOK": "Norwegian krone", "TRY": "Turkish lira", "AUD": "Australian dollar",
	"BRL": "Brazilian real", "CAD": "Canadian dollar", "CNY": "Chinese yuan renminbi", "HKD": "Hong Kong dollar",
	"IDR": "Indonesian rupiah", "ILS": "Israeli shekel", "INR": "Indian rupee", "KRW": "South Korean won",
	"MXN": "Mexican peso", "MYR": "Malaysian ringgit", "NZD": "New Zealand dollar", "PHP": "Philippine peso",
	"SGD": "Singapore dollar", "THB": "Thai baht", "ZAR": "South African rand", "EUR": "Euro",
}

type ecb struct {
	http *httpx.Client
	base string
	now  func() time.Time
}

func main() {
	sdk.Main(newPlugin(&ecb{http: httpx.New("FloweySales-ECB-plugin/1.0", 500*time.Millisecond), base: defaultBase, now: time.Now}))
}

func newPlugin(e *ecb) sdk.Plugin {
	manifest, err := sdk.ParseManifest(manifestJSON)
	if err != nil {
		panic(err)
	}
	return sdk.Plugin{
		Manifest: manifest,
		Quote:    e.quote,
		History: func(ctx context.Context, p sdk.HistoryParams) (sdk.HistorySeries, error) {
			return e.history(ctx, p.Symbol, p.Range)
		},
		Search: func(_ context.Context, p sdk.SearchParams) ([]sdk.SearchResult, error) { return search(p.Query), nil },
	}
}

// parsePair reads "EURUSD", "EUR/USD", "EUR-USD" or a bare "USD" (per euro).
func parsePair(symbol string) (base, quote string, err error) {
	s := strings.NewReplacer("/", "", "-", "", "_", "", "=X", "", " ", "").Replace(strings.ToUpper(strings.TrimSpace(symbol)))
	switch len(s) {
	case 3:
		base, quote = "EUR", s
	case 6:
		base, quote = s[:3], s[3:]
	default:
		return "", "", sdk.Errorf(sdk.CodeInvalidArgument, "%q is not a currency pair such as EURUSD", symbol)
	}
	if _, ok := currencies[base]; !ok {
		return "", "", sdk.Errorf(sdk.CodeInvalidArgument, "the ECB publishes no reference rate for %s", base)
	}
	if _, ok := currencies[quote]; !ok || base == quote {
		return "", "", sdk.Errorf(sdk.CodeInvalidArgument, "the ECB publishes no reference rate for %s", quote)
	}
	return base, quote, nil
}

var rangeStart = map[string]func(time.Time) time.Time{
	sdk.Range1D:  func(t time.Time) time.Time { return t.AddDate(0, 0, -10) }, // daily data: at least a few fixings
	sdk.Range5D:  func(t time.Time) time.Time { return t.AddDate(0, 0, -10) },
	sdk.Range1M:  func(t time.Time) time.Time { return t.AddDate(0, -1, 0) },
	sdk.Range3M:  func(t time.Time) time.Time { return t.AddDate(0, -3, 0) },
	sdk.Range1Y:  func(t time.Time) time.Time { return t.AddDate(-1, 0, 0) },
	sdk.Range3Y:  func(t time.Time) time.Time { return t.AddDate(-3, 0, 0) },
	sdk.Range5Y:  func(t time.Time) time.Time { return t.AddDate(-5, 0, 0) },
	sdk.Range10Y: func(t time.Time) time.Time { return t.AddDate(-10, 0, 0) },
	sdk.RangeMAX: func(time.Time) time.Time { return time.Date(1999, 1, 4, 0, 0, 0, 0, time.UTC) },
}

// euroRates returns date → units of currency per euro for each currency.
func (e *ecb) euroRates(ctx context.Context, from time.Time, codes []string) (map[string]map[string]float64, error) {
	path := fmt.Sprintf("/service/data/EXR/D.%s.EUR.SP00.A?format=csvdata&startPeriod=%s", strings.Join(codes, "+"), from.Format("2006-01-02"))
	body, err := e.http.Get(ctx, e.base+path, map[string]string{"Accept": "text/csv"})
	if err != nil {
		var status *httpx.StatusError
		if errors.As(err, &status) && status.Status == 404 {
			return map[string]map[string]float64{}, nil // no observations in the window
		}
		return nil, sdk.Errorf(sdk.CodeUpstream, "ECB data API request failed: %v", err)
	}
	rows, err := csv.NewReader(strings.NewReader(string(body))).ReadAll()
	if err != nil || len(rows) == 0 {
		return nil, sdk.Errorf(sdk.CodeUpstream, "ECB returned malformed CSV")
	}
	col := map[string]int{}
	for i, name := range rows[0] {
		col[strings.TrimSpace(name)] = i
	}
	ci, okC := col["CURRENCY"]
	ti, okT := col["TIME_PERIOD"]
	vi, okV := col["OBS_VALUE"]
	if !okC || !okT || !okV {
		return nil, sdk.Errorf(sdk.CodeUpstream, "ECB CSV lacks CURRENCY, TIME_PERIOD or OBS_VALUE")
	}
	out := map[string]map[string]float64{}
	for _, row := range rows[1:] {
		if len(row) <= max(ci, ti, vi) {
			continue
		}
		v, err := strconv.ParseFloat(row[vi], 64)
		if err != nil || v <= 0 {
			continue // missing fixings stay absent
		}
		if out[row[ci]] == nil {
			out[row[ci]] = map[string]float64{}
		}
		out[row[ci]][row[ti]] = v
	}
	return out, nil
}

// pairPoints derives base→quote rates for the days both euro rates exist.
func pairPoints(rates map[string]map[string]float64, base, quote string) []sdk.HistoryPoint {
	rate := func(code, day string) (float64, bool) {
		if code == "EUR" {
			return 1, true
		}
		v, ok := rates[code][day]
		return v, ok
	}
	days := map[string]bool{}
	for _, code := range []string{base, quote} {
		for d := range rates[code] {
			days[d] = true
		}
	}
	sorted := make([]string, 0, len(days))
	for d := range days {
		sorted = append(sorted, d)
	}
	sort.Strings(sorted)
	points := []sdk.HistoryPoint{}
	for _, d := range sorted {
		b, okB := rate(base, d)
		q, okQ := rate(quote, d)
		t, err := time.Parse("2006-01-02", d)
		if !okB || !okQ || err != nil {
			continue
		}
		v := q / b
		points = append(points, sdk.HistoryPoint{Timestamp: t.Unix(), Open: v, High: v, Low: v, Close: v})
	}
	return points
}

func (e *ecb) history(ctx context.Context, symbol, rng string) (sdk.HistorySeries, error) {
	base, quote, err := parsePair(symbol)
	if err != nil {
		return sdk.HistorySeries{}, err
	}
	start, ok := rangeStart[rng]
	if !ok {
		rng, start = sdk.Range1M, rangeStart[sdk.Range1M]
	}
	codes := []string{}
	for _, c := range []string{base, quote} {
		if c != "EUR" {
			codes = append(codes, c)
		}
	}
	rates, err := e.euroRates(ctx, start(e.now().UTC()), codes)
	if err != nil {
		return sdk.HistorySeries{}, err
	}
	return sdk.HistorySeries{Symbol: base + quote, Provider: "ECB", Range: rng, Interval: "1d", Currency: quote, Points: pairPoints(rates, base, quote)}, nil
}

func (e *ecb) quote(ctx context.Context, p sdk.QuoteParams) (sdk.Asset, error) {
	series, err := e.history(ctx, p.Symbol, sdk.Range5D)
	if err != nil {
		return sdk.Asset{}, err
	}
	if len(series.Points) == 0 {
		return sdk.Asset{}, sdk.Errorf(sdk.CodeUpstream, "no recent ECB reference rate for %s", series.Symbol)
	}
	last := series.Points[len(series.Points)-1]
	return sdk.Asset{Symbol: series.Symbol, Name: series.Symbol[:3] + "/" + series.Symbol[3:] + " ECB reference rate", Price: last.Close, Currency: series.Currency, Provider: "ECB",
		Data: map[string]any{"assetDataProvider": "ECB", "type": "fx_reference_rate", "fixing_date": time.Unix(last.Timestamp, 0).UTC().Format("2006-01-02")}}, nil
}

// search lists EUR pairs, and the cross pair when the query names two currencies.
func search(query string) []sdk.SearchResult {
	q := strings.ToUpper(strings.TrimSpace(query))
	out := []sdk.SearchResult{}
	if q == "" {
		return out
	}
	if base, quote, err := parsePair(q); err == nil && len(strings.ReplaceAll(q, "/", "")) == 6 {
		return append(out, sdk.SearchResult{Symbol: base + quote, Name: currencies[base] + " in " + currencies[quote], Type: "FX", Exchange: "ECB"})
	}
	codes := make([]string, 0, len(currencies))
	for c := range currencies {
		codes = append(codes, c)
	}
	sort.Strings(codes)
	for _, c := range codes {
		if c == "EUR" || (!strings.Contains(c, q) && !strings.Contains(strings.ToUpper(currencies[c]), q)) {
			continue
		}
		out = append(out, sdk.SearchResult{Symbol: "EUR" + c, Name: "Euro in " + currencies[c], Type: "FX", Exchange: "ECB"})
		if len(out) == 10 {
			break
		}
	}
	return out
}
