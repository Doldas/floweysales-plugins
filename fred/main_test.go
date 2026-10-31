package main

import (
	"context"
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

func testServer(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("api_key") != "k" || r.URL.Query().Get("file_type") != "json" {
			t.Errorf("missing key or format: %s", r.URL.Path)
		}
		switch r.URL.Path {
		case "/fred/series":
			if r.URL.Query().Get("series_id") == "NOPE" {
				w.WriteHeader(400)
				return
			}
			_, _ = w.Write([]byte(`{"seriess":[{"id":"DGS10","title":"10-Year Treasury","frequency_short":"D","units_short":"Percent"}]}`))
		case "/fred/series/observations":
			_, _ = w.Write([]byte(`{"observations":[{"date":"2026-10-28","value":"4.10"},{"date":"2026-10-29","value":"."},{"date":"2026-10-30","value":"4.20"}]}`))
		case "/fred/series/search":
			_, _ = w.Write([]byte(`{"seriess":[{"id":"UNRATE","title":"Unemployment Rate","frequency_short":"M","units_short":"Percent"}]}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
}

func TestHistoryQuoteAndSearch(t *testing.T) {
	srv := testServer(t)
	defer srv.Close()
	p := newPlugin(&fred{http: httpx.New("test", 0), base: srv.URL, key: "k", now: func() time.Time { return time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC) }})
	series, err := p.History(context.Background(), sdk.HistoryParams{Symbol: "dgs10", Range: "1M"})
	if err != nil || len(series.Points) != 2 || series.Currency != "Percent" || series.Interval != "1d" || series.Provider != "FRED" {
		t.Fatalf("missing values must be dropped, not zero: %+v %v", series, err)
	}
	asset, err := p.Quote(context.Background(), sdk.QuoteParams{Symbol: "DGS10"})
	if err != nil || asset.Price != 4.2 || asset.Data["observation_date"] != "2026-10-30" {
		t.Fatalf("%+v %v", asset, err)
	}
	hits, err := p.Search(context.Background(), sdk.SearchParams{Query: "unemployment"})
	if err != nil || len(hits) != 1 || hits[0].Symbol != "UNRATE" {
		t.Fatalf("%+v %v", hits, err)
	}
}

func TestKeyNeverLeaksAndIsRequired(t *testing.T) {
	srv := testServer(t)
	defer srv.Close()
	p := newPlugin(&fred{http: httpx.New("test", 0), base: srv.URL, key: "k", now: time.Now})
	_, err := p.History(context.Background(), sdk.HistoryParams{Symbol: "NOPE", Range: "1Y"})
	var coded *sdk.ResponseError
	if !errors.As(err, &coded) || coded.Code != sdk.CodeInvalidArgument || strings.Contains(err.Error(), "api_key") {
		t.Fatalf("got %v", err)
	}
	if _, err := p.History(context.Background(), sdk.HistoryParams{Symbol: "BAD SYMBOL!", Range: "1Y"}); err == nil {
		t.Fatal("invalid series id accepted")
	}
	unconfigured := newPlugin(&fred{http: httpx.New("test", 0), base: srv.URL, now: time.Now})
	if _, err := unconfigured.Quote(context.Background(), sdk.QuoteParams{Symbol: "DGS10"}); !errors.As(err, &coded) || coded.Code != sdk.CodeNotConfigured {
		t.Fatalf("missing key must be not_configured, got %v", err)
	}
}

func TestConformance(t *testing.T) {
	plugintest.Conformance(t, newPlugin(&fred{http: httpx.New("test", 0), base: defaultBase, now: time.Now}))
}
