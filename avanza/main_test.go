package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Doldas/floweysales-plugins/internal/yahooapi"
	sdk "github.com/Doldas/floweysales/pluginsdk"
	"github.com/Doldas/floweysales/pluginsdk/httpx"
	"github.com/Doldas/floweysales/pluginsdk/plugintest"
)

func TestHistoryFallsBackToLabelledYahoo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/_api/search/filtered-search":
			_, _ = w.Write([]byte(`{"hits":[{"orderBookId":"5269","type":"STOCK","title":"Volvo B"}]}`))
		case strings.HasPrefix(r.URL.Path, "/_api/price-chart/"):
			w.WriteHeader(http.StatusUnauthorized)
		case r.URL.Path == "/_api/market-guide/stock/5269":
			_, _ = w.Write([]byte(`{"name":"Volvo B","listing":{"tickerSymbol":"VOLV B","marketPlaceName":"Stockholmsbörsen","currency":"SEK"},"quote":{"last":250.5}}`))
		case r.URL.Path == "/v8/finance/chart/VOLV-B.ST":
			_, _ = w.Write([]byte(`{"chart":{"result":[{"meta":{"symbol":"VOLV-B.ST","currency":"SEK"},"timestamp":[1],"indicators":{"quote":[{"close":[250]}]}}]}}`))
		default:
			t.Errorf("unexpected %s", r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	a := &avanza{http: httpx.New("test", 0), base: srv.URL, yahoo: &yahooapi.API{HTTP: httpx.New("test", 0), Base: srv.URL}}
	p := newPlugin(a)
	series, err := p.History(context.Background(), sdk.HistoryParams{Symbol: "volvo", Range: "1M"})
	if err != nil || series.Provider != "YAHOO" || series.Currency != "SEK" || len(series.Points) != 1 {
		t.Fatalf("fallback must be labelled YAHOO: %+v err=%v", series, err)
	}
	asset, err := p.Quote(context.Background(), sdk.QuoteParams{Symbol: "5269"})
	if err != nil || asset.Price != 250.5 || asset.Currency != "SEK" || asset.Data["type"] != "STOCK" {
		t.Fatalf("asset=%+v err=%v", asset, err)
	}
	if !p.Manifest.Provider.Experimental || p.Manifest.Terms == "" {
		t.Fatal("Avanza must stay experimental with install terms")
	}
}

func TestConformance(t *testing.T) {
	plugintest.Conformance(t, newPlugin(&avanza{http: httpx.New("test", 0), base: "https://invalid.test", yahoo: yahooapi.New()}))
}
