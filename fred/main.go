// Command fred is the FloweySales plugin for FRED macroeconomic series.
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
	"strconv"
	"strings"
	"time"

	sdk "github.com/Doldas/floweysales/pluginsdk"
	"github.com/Doldas/floweysales/pluginsdk/httpx"
)

//go:embed plugin.json
var manifestJSON []byte

const defaultBase = "https://api.stlouisfed.org"

// seriesID is FRED's identifier format, for example DGS10 or CPIAUCSL.
var seriesID = regexp.MustCompile(`^[A-Z0-9_]{1,30}$`)

type fred struct {
	http *httpx.Client
	base string
	key  string
	now  func() time.Time
}

func main() {
	sdk.Main(newPlugin(&fred{http: httpx.New("FloweySales-FRED-plugin/1.0", 600*time.Millisecond), base: defaultBase, key: os.Getenv("FRED_API_KEY"), now: time.Now}))
}

func newPlugin(f *fred) sdk.Plugin {
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
	}
}

// get calls the API. FRED only accepts the key as a query parameter, so
// errors are rewritten to never carry the request URL.
func (f *fred) get(ctx context.Context, path string, q url.Values, out any) error {
	if f.key == "" {
		return sdk.Errorf(sdk.CodeNotConfigured, "FRED_API_KEY must be configured on the backend")
	}
	q.Set("api_key", f.key)
	q.Set("file_type", "json")
	body, err := f.http.Get(ctx, f.base+path+"?"+q.Encode(), nil)
	if err != nil {
		var status *httpx.StatusError
		if errors.As(err, &status) {
			if status.Status == 400 {
				return sdk.Errorf(sdk.CodeInvalidArgument, "FRED rejected the request; check the series ID")
			}
			return sdk.Errorf(sdk.CodeUpstream, "FRED answered HTTP %d", status.Status)
		}
		return sdk.Errorf(sdk.CodeUpstream, "FRED request failed")
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("parse FRED response: %w", err)
	}
	return nil
}

func normalize(symbol string) (string, error) {
	s := strings.ToUpper(strings.TrimSpace(symbol))
	if !seriesID.MatchString(s) {
		return "", sdk.Errorf(sdk.CodeInvalidArgument, "%q is not a FRED series ID (for example DGS10)", symbol)
	}
	return s, nil
}

type seriesInfo struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Frequency  string `json:"frequency_short"`
	Units      string `json:"units_short"`
	Popularity int    `json:"popularity"`
}

func (f *fred) info(ctx context.Context, id string) (seriesInfo, error) {
	var parsed struct {
		Series []seriesInfo `json:"seriess"`
	}
	if err := f.get(ctx, "/fred/series", url.Values{"series_id": {id}}, &parsed); err != nil {
		return seriesInfo{}, err
	}
	if len(parsed.Series) == 0 {
		return seriesInfo{}, sdk.Errorf(sdk.CodeInvalidArgument, "unknown FRED series %s", id)
	}
	return parsed.Series[0], nil
}

var rangeStart = map[string]func(time.Time) time.Time{
	sdk.Range1D:  func(t time.Time) time.Time { return t.AddDate(0, 0, -14) }, // macro data needs a few observations
	sdk.Range5D:  func(t time.Time) time.Time { return t.AddDate(0, 0, -14) },
	sdk.Range1M:  func(t time.Time) time.Time { return t.AddDate(0, -1, 0) },
	sdk.Range3M:  func(t time.Time) time.Time { return t.AddDate(0, -3, 0) },
	sdk.Range1Y:  func(t time.Time) time.Time { return t.AddDate(-1, 0, 0) },
	sdk.Range3Y:  func(t time.Time) time.Time { return t.AddDate(-3, 0, 0) },
	sdk.Range5Y:  func(t time.Time) time.Time { return t.AddDate(-5, 0, 0) },
	sdk.Range10Y: func(t time.Time) time.Time { return t.AddDate(-10, 0, 0) },
	sdk.RangeMAX: func(time.Time) time.Time { return time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC) },
}

// observations returns the series' values from start; FRED marks missing
// values with "." and those stay out rather than becoming zero.
func (f *fred) observations(ctx context.Context, id string, start time.Time) ([]sdk.HistoryPoint, error) {
	var parsed struct {
		Observations []struct {
			Date  string `json:"date"`
			Value string `json:"value"`
		} `json:"observations"`
	}
	q := url.Values{"series_id": {id}, "observation_start": {start.Format("2006-01-02")}, "limit": {"100000"}}
	if err := f.get(ctx, "/fred/series/observations", q, &parsed); err != nil {
		return nil, err
	}
	points := []sdk.HistoryPoint{}
	for _, o := range parsed.Observations {
		v, err := strconv.ParseFloat(o.Value, 64)
		if err != nil {
			continue
		}
		t, err := time.Parse("2006-01-02", o.Date)
		if err != nil {
			continue
		}
		points = append(points, sdk.HistoryPoint{Timestamp: t.Unix(), Open: v, High: v, Low: v, Close: v})
	}
	return points, nil
}

func interval(frequency string) string {
	switch strings.ToUpper(frequency) {
	case "D":
		return "1d"
	case "W", "BW":
		return "1wk"
	case "M":
		return "1mo"
	case "Q":
		return "3mo"
	case "A":
		return "1y"
	}
	return "1d"
}

func (f *fred) history(ctx context.Context, symbol, rng string) (sdk.HistorySeries, error) {
	id, err := normalize(symbol)
	if err != nil {
		return sdk.HistorySeries{}, err
	}
	start, ok := rangeStart[rng]
	if !ok {
		rng, start = sdk.Range1M, rangeStart[sdk.Range1M]
	}
	info, err := f.info(ctx, id)
	if err != nil {
		return sdk.HistorySeries{}, err
	}
	from := start(f.now().UTC())
	if strings.ToUpper(info.Frequency) != "D" && rng != sdk.RangeMAX {
		// Monthly and quarterly series need a longer window to show anything.
		from = from.AddDate(0, -3, 0)
	}
	points, err := f.observations(ctx, id, from)
	if err != nil {
		return sdk.HistorySeries{}, err
	}
	return sdk.HistorySeries{Symbol: id, Provider: "FRED", Range: rng, Interval: interval(info.Frequency), Currency: info.Units, Points: points}, nil
}

func (f *fred) quote(ctx context.Context, p sdk.QuoteParams) (sdk.Asset, error) {
	series, err := f.history(ctx, p.Symbol, sdk.Range1Y)
	if err != nil {
		return sdk.Asset{}, err
	}
	if len(series.Points) == 0 {
		return sdk.Asset{}, sdk.Errorf(sdk.CodeUpstream, "FRED has no recent observation for %s", series.Symbol)
	}
	info, _ := f.info(ctx, series.Symbol)
	last := series.Points[len(series.Points)-1]
	return sdk.Asset{Symbol: series.Symbol, Name: info.Title, Price: last.Close, Currency: series.Currency, Provider: "FRED",
		Data: map[string]any{"assetDataProvider": "FRED", "type": "macro_series", "observation_date": time.Unix(last.Timestamp, 0).UTC().Format("2006-01-02"), "frequency": info.Frequency, "units": info.Units}}, nil
}

func (f *fred) search(ctx context.Context, query string) ([]sdk.SearchResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return []sdk.SearchResult{}, nil
	}
	var parsed struct {
		Series []seriesInfo `json:"seriess"`
	}
	q := url.Values{"search_text": {query}, "limit": {"10"}, "order_by": {"popularity"}, "sort_order": {"desc"}}
	if err := f.get(ctx, "/fred/series/search", q, &parsed); err != nil {
		return nil, err
	}
	out := []sdk.SearchResult{}
	for _, s := range parsed.Series {
		out = append(out, sdk.SearchResult{Symbol: s.ID, Name: s.Title + " (" + s.Units + ", " + s.Frequency + ")", Type: "MACRO", Exchange: "FRED"})
	}
	return out, nil
}
