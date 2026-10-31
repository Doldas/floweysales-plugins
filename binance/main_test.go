package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sdk "github.com/Doldas/floweysales/pluginsdk"
	"github.com/Doldas/floweysales/pluginsdk/httpx"
	"github.com/Doldas/floweysales/pluginsdk/plugintest"
)

func TestKlinesPaginateBackwardsAndParseStrings(t *testing.T) {
	var pages int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/klines":
			pages++
			// 1Y wants 366 daily bars with limit 1000: one full page is enough.
			var rows []string
			for i := 0; i < 400; i++ {
				rows = append(rows, fmt.Sprintf(`[%d,"1.0","2.0","0.5","1.5","10",0]`, (1700000000+i*86400)*1000))
			}
			_, _ = w.Write([]byte("[" + strings.Join(rows, ",") + "]"))
		case "/api/v3/time":
			if r.URL.RawQuery != "" {
				t.Errorf("server_time must not send credentials or params: %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"serverTime":1}`))
		default:
			t.Errorf("unexpected %s", r.URL)
		}
	}))
	defer srv.Close()
	b := &binance{http: httpx.New("test", 0), api: srv.URL, data: srv.URL}
	series, err := b.history(context.Background(), "btcusdt", "1Y")
	if err != nil || pages != 1 || len(series.Points) != 400 || series.Points[0].Timestamp != 1700000000 || series.Points[0].Close != 1.5 {
		t.Fatalf("pages=%d err=%v first=%+v", pages, err, series.Points)
	}
	p := newPlugin(b)
	res, err := p.Research(context.Background(), sdk.ResearchParams{Operation: "server_time"})
	if err != nil || res.Provider != "BINANCE" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	fin, _ := p.Financials(context.Background(), sdk.FinancialsParams{Symbol: "BTCUSDT"})
	if fin.Source != "none" || fin.Reports == nil {
		t.Fatalf("crypto financials must be an explicit empty list: %+v", fin)
	}
}

func TestConformance(t *testing.T) {
	plugintest.Conformance(t, newPlugin(&binance{http: httpx.New("test", 0), api: "https://invalid.test", data: "https://invalid.test"}))
}
