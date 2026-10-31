package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
