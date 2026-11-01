package yahooapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Doldas/floweysales/pluginsdk/httpx"
)

func TestHistorySkipsNullClosesAndFillsOHLC(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.RawQuery, "range=1mo&interval=1h") {
			t.Errorf("unexpected query %s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"chart":{"result":[{"meta":{"symbol":"GIB","currency":"USD"},"timestamp":[1,2,3],
			"indicators":{"quote":[{"close":[10,null,12],"open":[9.5,null,null],"high":[10.5,null,12.5],"low":[9,null,11.5],"volume":[100,null,300]}]}}]}}`))
	}))
	defer srv.Close()
	api := &API{HTTP: httpx.New("test", 0), Base: srv.URL}
	series, err := api.History(context.Background(), "gib", "1M")
	if err != nil {
		t.Fatal(err)
	}
	if series.Provider != "YAHOO" || series.Currency != "USD" || len(series.Points) != 2 {
		t.Fatalf("%+v", series)
	}
	if p := series.Points[1]; p.Open != 12 || p.Close != 12 || p.Volume != 300 {
		t.Fatalf("missing open must default to close: %+v", p)
	}
}

func TestFinancialsOmitsIndistinguishableGrossProfit(t *testing.T) {
	parsed := map[string]any{"timeseries": map[string]any{"result": []any{
		map[string]any{"meta": map[string]any{"type": []any{"quarterlyGrossProfit"}}, "quarterlyGrossProfit": []any{
			map[string]any{"asOfDate": "2026-06-30", "currencyCode": "CAD", "reportedValue": map[string]any{"raw": 500.0}}}},
		map[string]any{"meta": map[string]any{"type": []any{"quarterlyOperatingIncome"}}, "quarterlyOperatingIncome": []any{
			map[string]any{"asOfDate": "2026-06-30", "reportedValue": map[string]any{"raw": 500.0}}}},
	}}}
	res := ParseFinancials(parsed, "GIB", 0, 0)
	if len(res.Reports) != 1 || res.Reports[0].GrossProfit != nil || len(res.Reports[0].DataQualityWarnings) != 1 {
		t.Fatalf("%+v", res.Reports)
	}
	if res.Reports[0].Period != "CY2026 Q2" || res.Reports[0].Currency != "CAD" {
		t.Fatalf("%+v", res.Reports[0])
	}
}

const eventsBody = `{"chart":{"result":[{"meta":{"symbol":"KO","currency":"USD"},"timestamp":[1],"events":{
 "dividends":{"1760000000":{"amount":0.51,"date":1760000000},"1752000000":{"amount":0,"date":1752000000},"1700000000":{"amount":0.4,"date":1700000000}},
 "splits":{"1761000000":{"date":1761000000,"numerator":4,"denominator":1,"splitRatio":"4:1"},"1761500000":{"date":1761500000,"numerator":3,"denominator":2}}}}]}}`

func TestParseEventsKeepsTheWindowAndLeavesUnknownAmountsAbsent(t *testing.T) {
	var parsed map[string]any
	if err := json.Unmarshal([]byte(eventsBody), &parsed); err != nil {
		t.Fatal(err)
	}
	from := time.Date(2025, 7, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC)
	events := ParseEvents(parsed, "KO", from, to)
	if len(events) != 4 {
		t.Fatalf("the 2023 dividend is outside the window: %+v", events)
	}
	if events[0].Kind != "dividend" || events[0].Date != "2025-07-08" || events[0].Amount != nil {
		t.Fatalf("a zero amount is unknown, not 0: %+v", events[0])
	}
	if e := events[1]; e.Kind != "dividend" || e.Date != "2025-10-09" || e.Amount == nil || *e.Amount != 0.51 || e.Currency != "USD" || !e.Confirmed || e.Provider != "YAHOO" {
		t.Fatalf("%+v", e)
	}
	if events[2].Kind != "split" || events[2].Ratio != "4:1" || events[3].Ratio != "3:2" {
		t.Fatalf("splits: %+v %+v", events[2], events[3])
	}
	if got := ParseEvents(map[string]any{}, "KO", from, to); len(got) != 0 || got == nil {
		t.Fatalf("no result is an empty list: %v", got)
	}
}

func TestEventsRequestsTheWindowAndValidatesDates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("events") != "div,split" || q.Get("period1") != "1751328000" || q.Get("period2") != "1767225600" {
			t.Errorf("query %s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(eventsBody))
	}))
	defer srv.Close()
	api := &API{HTTP: httpx.New("test", 0), Base: srv.URL}
	events, err := api.Events(context.Background(), "ko", "2025-07-01", "2025-12-31")
	if err != nil || len(events) != 4 {
		t.Fatalf("%v %+v", err, events)
	}
	for _, c := range [][2]string{{"bad", "2025-12-31"}, {"2025-07-01", "bad"}, {"2025-12-31", "2025-07-01"}, {"2024-01-01", "2025-12-31"}} {
		if _, err := api.Events(context.Background(), "KO", c[0], c[1]); err == nil {
			t.Errorf("%v should be refused", c)
		}
	}
	if _, err := api.Events(context.Background(), " ", "2025-07-01", "2025-12-31"); err == nil {
		t.Error("a symbol is required")
	}
}
