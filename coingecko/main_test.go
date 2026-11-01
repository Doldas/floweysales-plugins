package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sdk "github.com/Doldas/floweysales/pluginsdk"
	"github.com/Doldas/floweysales/pluginsdk/httpx"
	"github.com/Doldas/floweysales/pluginsdk/plugintest"
)

func server(t *testing.T, key string) (*httptest.Server, *[]string) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.RequestURI())
		if got := r.Header.Get("x-cg-demo-api-key"); got != key {
			t.Errorf("demo key header %q, want %q", got, key)
		}
		if strings.Contains(r.URL.RawQuery, "api_key") || strings.Contains(r.URL.RawQuery, "x_cg") {
			t.Errorf("the key must travel in a header: %s", r.URL.RawQuery)
		}
		switch {
		case r.URL.Path == "/api/v3/simple/price":
			if r.URL.Query().Get("ids") == "ghost" {
				_, _ = w.Write([]byte(`{}`))
				return
			}
			_, _ = w.Write([]byte(`{"bitcoin":{"usd":67123.5,"usd_24h_change":-1.25}}`))
		case strings.HasSuffix(r.URL.Path, "/market_chart"):
			if r.URL.Path == "/api/v3/coins/missing/market_chart" {
				http.Error(w, "not found", 404)
				return
			}
			_, _ = w.Write([]byte(`{"prices":[[1761900000000,100.5],[1761986400000,0],[1762072800000,101.25]],"total_volumes":[[1761900000000,5000],[1762072800000,6000]]}`))
		case r.URL.Path == "/api/v3/search":
			_, _ = w.Write([]byte(`{"coins":[{"id":"bitcoin","name":"Bitcoin","symbol":"btc"},{"id":"Bad ID!","name":"x","symbol":"x"},{"id":"bitcoin-cash","name":"Bitcoin Cash","symbol":"bch"}]}`))
		default:
			t.Errorf("unexpected %s", r.URL)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

func TestQuoteHistoryAndSearch(t *testing.T) {
	srv, _ := server(t, "demo")
	c := &coingecko{http: httpx.New("test", 0), base: srv.URL, key: "demo"}
	ctx := context.Background()

	a, err := c.quote(ctx, sdk.QuoteParams{Symbol: " Bitcoin "})
	if err != nil || a.Symbol != "bitcoin" || a.Price != 67123.5 || a.Currency != "USD" || a.Provider != "COINGECKO" || a.Data["changePercent24h"] != -1.25 {
		t.Fatalf("%+v %v", a, err)
	}
	if _, err := c.quote(ctx, sdk.QuoteParams{Symbol: "ghost"}); err == nil {
		t.Fatal("an unknown coin has no price, not a zero price")
	}
	if _, err := c.quote(ctx, sdk.QuoteParams{Symbol: "BTC/USD"}); err == nil {
		t.Fatal("a pair is not a coin ID")
	}

	s, err := c.history(ctx, "bitcoin", "1Y")
	if err != nil || s.Provider != "COINGECKO" || s.Currency != "USD" || s.Interval != "1d" || len(s.Points) != 2 {
		t.Fatalf("%+v %v", s, err)
	}
	if p := s.Points[0]; p.Open != 100.5 || p.High != 100.5 || p.Low != 100.5 || p.Close != 100.5 || p.Volume != 5000 || p.Timestamp != 1761900000 {
		t.Fatalf("a price line has o=h=l=c: %+v", p)
	}
	if long, _ := c.history(ctx, "bitcoin", "5Y"); long.Range != "1Y" {
		t.Fatalf("the free API serves one year, and the range says so: %q", long.Range)
	}
	if _, err := c.history(ctx, "missing", "1M"); err == nil || strings.Contains(err.Error(), srv.URL) {
		t.Fatalf("errors must not carry the URL: %v", err)
	}

	found, err := c.search(ctx, "bitcoin")
	if err != nil || len(found) != 2 || found[0].Symbol != "bitcoin" || found[0].Name != "Bitcoin (BTC)" || found[0].Type != "CRYPTO" {
		t.Fatalf("%+v %v", found, err)
	}
}

func TestRateLimitIsExplained(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "slow down", 429) }))
	defer srv.Close()
	c := &coingecko{http: httpx.New("test", 0), base: srv.URL}
	_, err := c.quote(context.Background(), sdk.QuoteParams{Symbol: "bitcoin"})
	if err == nil || !strings.Contains(err.Error(), "rate limit") {
		t.Fatalf("%v", err)
	}
}

func TestConformance(t *testing.T) {
	plugintest.Conformance(t, newPlugin(&coingecko{http: httpx.New("test", 0), base: "https://invalid.test"}))
}
