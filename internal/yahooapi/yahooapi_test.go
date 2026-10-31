package yahooapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
