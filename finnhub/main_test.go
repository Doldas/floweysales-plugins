package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	sdk "github.com/Doldas/floweysales/pluginsdk"
	"github.com/Doldas/floweysales/pluginsdk/httpx"
	"github.com/Doldas/floweysales/pluginsdk/plugintest"
)

func TestTokenStaysInHeaderAndIsRequired(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.RawQuery, "secret-token") || r.Header.Get("X-Finnhub-Token") != "secret-token" {
			t.Errorf("token must be a header only: %s", r.URL)
		}
		_, _ = w.Write([]byte(`{"name":"NVIDIA"}`))
	}))
	defer srv.Close()
	p := newPlugin(&finnhub{http: httpx.New("test", 0), base: srv.URL, key: "secret-token"})
	res, err := p.Research(context.Background(), sdk.ResearchParams{Operation: "company_profile", Symbol: "NVDA"})
	if err != nil || strings.Contains(res.Source, "secret-token") {
		t.Fatalf("res=%+v err=%v", res, err)
	}

	unconfigured := newPlugin(&finnhub{http: httpx.New("test", 0), base: srv.URL})
	_, err = unconfigured.Quote(context.Background(), sdk.QuoteParams{Symbol: "NVDA"})
	var coded *sdk.ResponseError
	if !errors.As(err, &coded) || coded.Code != sdk.CodeNotConfigured {
		t.Fatalf("missing key must be not_configured, got %v", err)
	}
}

func TestYTDFilingsBecomeQuarters(t *testing.T) {
	var parsed reported
	_ = json.Unmarshal([]byte(`{"data":[
		{"year":2026,"quarter":2,"form":"10-Q","startDate":"2026-01-01 00:00:00","endDate":"2026-06-30 00:00:00","report":{"ic":[{"concept":"us-gaap_Revenues","value":300},{"concept":"us-gaap_EarningsPerShareDiluted","value":0.3}]}},
		{"year":2026,"quarter":1,"form":"10-Q","startDate":"2026-01-01 00:00:00","endDate":"2026-03-31 00:00:00","report":{"ic":[{"concept":"us-gaap_Revenues","value":100},{"concept":"us-gaap_EarningsPerShareDiluted","value":0.1}]}}
	]}`), &parsed)
	rows := quarterlyFromReported(parsed)
	if len(rows) != 2 || rows[0].PeriodEnd != "2026-06-30" || *rows[0].Revenue != 200 || *rows[0].EPS != 0.2 || *rows[1].Revenue != 100 {
		t.Fatalf("%+v", rows)
	}
	if rows[0].GrossProfit != nil {
		t.Fatal("missing concepts must stay nil, never zero")
	}
}

func TestConformance(t *testing.T) {
	plugintest.Conformance(t, newPlugin(&finnhub{http: httpx.New("test", 0), base: "https://invalid.test"}))
}

func TestCompanyNewsBecomesHeadlines(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/company-news" || r.URL.Query().Get("symbol") != "NVDA" || r.URL.Query().Get("from") == "" {
			t.Errorf("unexpected request %s", r.URL)
		}
		_, _ = w.Write([]byte(`[{"headline":"NVIDIA beats","url":"https://example.com/a","source":"Wire","datetime":1761900000},{"headline":"bad","url":"javascript:x"},{"headline":"Second","url":"https://example.com/b"}]`))
	}))
	defer srv.Close()
	p := newPlugin(&finnhub{http: httpx.New("test", 0), base: srv.URL, key: "k"})
	items, err := p.News(context.Background(), sdk.NewsParams{Symbol: "nvda", Limit: 1})
	if err != nil || len(items) != 1 || items[0].Title != "NVIDIA beats" || items[0].Published != 1761900000 || items[0].Provider != "FINNHUB" {
		t.Fatalf("items=%+v err=%v", items, err)
	}
}

func TestEventsCombineEarningsAndDividends(t *testing.T) {
	soon := time.Now().UTC().AddDate(0, 0, 3).Format("2006-01-02")
	far := time.Now().UTC().AddDate(0, 0, 40).Format("2006-01-02")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Finnhub-Token") != "k" || strings.Contains(r.URL.RawQuery, "token") {
			t.Errorf("token must be a header only: %s", r.URL)
		}
		switch r.URL.Path {
		case "/calendar/earnings":
			if r.URL.Query().Get("symbol") != "AAPL" || r.URL.Query().Get("from") != "2026-11-01" {
				t.Errorf("window: %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"earningsCalendar":[{"date":"` + far + `","hour":"","symbol":"AAPL"},{"date":"` + soon + `","hour":"amc","symbol":"AAPL"},{"date":"nonsense","symbol":"AAPL"},{"date":"` + soon + `","symbol":"MSFT"}]}`))
		case "/stock/dividend2":
			_, _ = w.Write([]byte(`{"data":[{"date":"2026-11-14","amount":0.26,"currency":"USD"},{"date":"2026-12-12","amount":0},{"date":"2025-01-01","amount":9}]}`))
		default:
			t.Errorf("unexpected %s", r.URL)
		}
	}))
	defer srv.Close()
	p := newPlugin(&finnhub{http: httpx.New("test", 0), base: srv.URL, key: "k"})
	events, err := p.Events(context.Background(), sdk.EventsParams{Symbol: "aapl", From: "2026-11-01", To: "2026-12-31"})
	if err != nil || len(events) != 4 {
		t.Fatalf("%v %+v", err, events)
	}
	byKind := map[string][]sdk.CorporateEvent{}
	for _, e := range events {
		byKind[e.Kind] = append(byKind[e.Kind], e)
		if e.Provider != "FINNHUB" || e.Symbol != "AAPL" {
			t.Errorf("%+v", e)
		}
	}
	if len(byKind["earnings"]) != 2 || !byKind["earnings"][0].Confirmed || byKind["earnings"][1].Confirmed {
		t.Fatalf("a near date with an hour is confirmed, a far estimate is not: %+v", byKind["earnings"])
	}
	d := byKind["dividend"]
	if len(d) != 2 || d[0].Amount == nil || *d[0].Amount != 0.26 || d[0].Currency != "USD" || d[1].Amount != nil {
		t.Fatalf("a zero dividend amount is unknown, not 0: %+v", d)
	}
	for _, bad := range []sdk.EventsParams{{Symbol: "", From: "2026-11-01", To: "2026-11-02"}, {Symbol: "AAPL", From: "x", To: "2026-11-02"}, {Symbol: "AAPL", From: "2026-11-01", To: "2028-01-01"}} {
		if _, err := p.Events(context.Background(), bad); err == nil {
			t.Errorf("%+v should be refused", bad)
		}
	}
}

func TestEventsSkipDividendsTheKeyCannotRead(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/stock/dividend2" {
			http.Error(w, "You don't have access to this resource.", 403)
			return
		}
		_, _ = w.Write([]byte(`{"earningsCalendar":[{"date":"2026-11-20","hour":"bmo","symbol":"AAPL"}]}`))
	}))
	defer srv.Close()
	p := newPlugin(&finnhub{http: httpx.New("test", 0), base: srv.URL, key: "k"})
	events, err := p.Events(context.Background(), sdk.EventsParams{Symbol: "AAPL", From: "2026-11-01", To: "2026-12-31"})
	if err != nil || len(events) != 1 || events[0].Kind != "earnings" {
		t.Fatalf("earnings alone: %v %+v", err, events)
	}
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "no", 403) }))
	defer failing.Close()
	p = newPlugin(&finnhub{http: httpx.New("test", 0), base: failing.URL, key: "k"})
	if _, err := p.Events(context.Background(), sdk.EventsParams{Symbol: "AAPL", From: "2026-11-01", To: "2026-12-31"}); err == nil {
		t.Fatal("when both fail the error is reported")
	}
}
