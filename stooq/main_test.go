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

const csvBody = "Date,Open,High,Low,Close,Volume\n2026-10-27,100,102,99,101,1000\n2026-10-28,101,103,100,102.5,1200\n2026-10-29,102,,,0,0\n2026-10-30,102.5,104,101,103,900\n"

func newServer(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/q/d/l/" || r.URL.Query().Get("i") != "d" {
			t.Errorf("unexpected %s", r.URL)
		}
		switch r.URL.Query().Get("s") {
		case "aapl.us", "^spx":
			_, _ = w.Write([]byte(csvBody))
		default:
			_, _ = w.Write([]byte("No data"))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestSymbolMapping(t *testing.T) {
	for in, want := range map[string]string{"AAPL": "aapl.us", " brk-b ": "brk-b.us", "SPY": "spy.us", "^GSPC": "^spx", "^SPX": "^spx", "^IXIC": "^ndq"} {
		if got, _, err := stooqSymbol(in); err != nil || got != want {
			t.Errorf("%q: %q %v, want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"VOLV-B.ST", "ASML.AS", "BTCUSDT", "^OMX", "", "A B"} {
		if _, _, err := stooqSymbol(in); err == nil {
			t.Errorf("%q should be unsupported with a clear error", in)
		}
	}
	if _, cur, _ := stooqSymbol("^SPX"); cur != "" {
		t.Error("an index has no currency")
	}
}

func TestHistoryQuoteAndLabels(t *testing.T) {
	s := &stooq{http: httpx.New("test", 0), base: newServer(t).URL}
	ctx := context.Background()
	series, err := s.history(ctx, "aapl", "1M")
	if err != nil || series.Provider != "STOOQ" || series.Interval != "1d" || series.Currency != "USD" || len(series.Points) != 3 {
		t.Fatalf("%+v %v", series, err)
	}
	if p := series.Points[0]; p.Close != 101 || p.High != 102 || p.Volume != 1000 {
		t.Fatalf("%+v", p)
	}
	idx, err := s.history(ctx, "^SPX", "MAX")
	if err != nil || idx.Currency != "" || idx.Provider != "STOOQ" {
		t.Fatalf("an index has no guessed currency: %+v %v", idx, err)
	}
	short, _ := s.history(ctx, "AAPL", "1D")
	if len(short.Points) != 3 {
		t.Fatalf("1D keeps the last few days: %d", len(short.Points))
	}
	a, err := s.quote(ctx, sdk.QuoteParams{Symbol: "AAPL"})
	if err != nil || a.Price != 103 || a.Provider != "STOOQ" || a.Currency != "USD" || a.Data["previousClose"] != 102.5 {
		t.Fatalf("%+v %v", a, err)
	}
	if _, err := s.history(ctx, "ZZZZ", "1M"); err == nil || !strings.Contains(err.Error(), "no data") {
		t.Fatalf("unknown symbols are explained: %v", err)
	}
	if _, err := s.history(ctx, "VOLV-B.ST", "1M"); err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("%v", err)
	}
}

func TestParseCSVRejectsOtherBodies(t *testing.T) {
	for _, body := range []string{"", "No data", "<html>blocked</html>", "Date,Open\n2026-01-01,1\n"} {
		if _, err := parseCSV([]byte(body)); err == nil {
			t.Errorf("%q should not parse", body)
		}
	}
}

func TestConformance(t *testing.T) {
	plugintest.Conformance(t, newPlugin(&stooq{http: httpx.New("test", 0), base: "https://invalid.test"}))
}
