package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Doldas/floweysales-plugins/internal/yahooapi"
	sdk "github.com/Doldas/floweysales/pluginsdk"
	"github.com/Doldas/floweysales/pluginsdk/httpx"
	"github.com/Doldas/floweysales/pluginsdk/plugintest"
)

func TestManifestAndNewsResearch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/finance/search" || r.URL.Query().Get("q") != "NVDA" || r.URL.Query().Get("quotesCount") != "0" {
			t.Errorf("unexpected request %s", r.URL)
		}
		_, _ = w.Write([]byte(`{"news":[{"title":"x"}]}`))
	}))
	defer srv.Close()
	p := newPlugin(&yahooapi.API{HTTP: httpx.New("test", 0), Base: srv.URL})
	if p.Manifest.ID != "yahoo" || p.Manifest.Provider.ID != "YAHOO" {
		t.Fatalf("%+v", p.Manifest)
	}
	res, err := p.Research(context.Background(), sdk.ResearchParams{Operation: "market_news", Symbol: "nvda"})
	if err != nil || res.Symbol != "NVDA" || res.Source != yahooapi.DefaultBase+"/v1/finance/search" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if _, err := p.Research(context.Background(), sdk.ResearchParams{Operation: "order_book", Symbol: "NVDA"}); err == nil {
		t.Fatal("undeclared operation accepted")
	}
}

func TestConformance(t *testing.T) { plugintest.Conformance(t, newPlugin(yahooapi.New())) }
