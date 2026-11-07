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

func TestNewsKeepsOnlyHTTPSHeadlines(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("q") != "AAPL" || r.URL.Query().Get("newsCount") != "3" {
			t.Errorf("unexpected request %s", r.URL)
		}
		_, _ = w.Write([]byte(`{"news":[{"title":"Apple event","link":"https://finance.yahoo.com/a","publisher":"Reuters","providerPublishTime":1761900000},{"title":"x","link":"http://insecure"}]}`))
	}))
	defer srv.Close()
	p := newPlugin(&yahooapi.API{HTTP: httpx.New("test", 0), Base: srv.URL})
	items, err := p.News(context.Background(), sdk.NewsParams{Symbol: "aapl", Limit: 3})
	if err != nil || len(items) != 1 || items[0].Source != "Reuters" || items[0].Symbol != "AAPL" {
		t.Fatalf("items=%+v err=%v", items, err)
	}
}

const yahooRSS = `<rss version="2.0"><channel>
<item><title>Saab raises outlook</title><link>https://example.com/saab?.tsrc=rss</link><pubDate>Sat, 07 Nov 2026 19:23:00 +0000</pubDate></item>
<item><title>Other story</title><link>https://example.com/other</link><pubDate>Fri, 06 Nov 2026 08:00:00 +0000</pubDate></item>
<item><title>Undated</title><link>https://example.com/undated</link></item>
</channel></rss>`

func newsServer(t *testing.T, search string, rssStatus int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rss/2.0/headline" {
			if r.URL.Query().Get("s") != "SAAB-B.ST" {
				t.Errorf("feed request %s", r.URL)
			}
			if rssStatus != http.StatusOK {
				w.WriteHeader(rssStatus)
				return
			}
			_, _ = w.Write([]byte(yahooRSS))
			return
		}
		_, _ = w.Write([]byte(search))
	}))
}

func TestNewsFromFeedWhenSearchHasNoHeadlines(t *testing.T) {
	srv := newsServer(t, `{"news":[]}`, http.StatusOK)
	defer srv.Close()
	p := newPlugin(&yahooapi.API{HTTP: httpx.New("test", 0), Base: srv.URL, FeedBase: srv.URL})
	items, err := p.News(context.Background(), sdk.NewsParams{Symbol: "saab-b.st", Limit: 5})
	if err != nil || len(items) != 3 || items[0].Published != 1794079380 || items[1].Published != 1793952000 || items[2].Published != 0 {
		t.Fatalf("items=%+v err=%v", items, err)
	}
	if items[0].Provider != "YAHOO" || items[0].Symbol != "SAAB-B.ST" {
		t.Fatalf("%+v", items[0])
	}
}

func TestNewsFeedFillsMissingSearchDatesWithoutDuplicates(t *testing.T) {
	srv := newsServer(t, `{"news":[{"title":"Saab raises outlook","link":"https://example.com/saab","publisher":"Reuters"},{"title":"Third","link":"https://example.com/third","publisher":"AP","providerPublishTime":1794000000000}]}`, http.StatusOK)
	defer srv.Close()
	p := newPlugin(&yahooapi.API{HTTP: httpx.New("test", 0), Base: srv.URL, FeedBase: srv.URL})
	items, err := p.News(context.Background(), sdk.NewsParams{Symbol: "SAAB-B.ST", Limit: 4})
	if err != nil || len(items) != 4 {
		t.Fatalf("items=%+v err=%v", items, err)
	}
	if items[0].Source != "Reuters" || items[0].Published != 1794079380 {
		t.Fatalf("a search item should keep its publisher and get the feed date: %+v", items[0])
	}
	if items[1].Published != 1794000000 {
		t.Fatalf("a millisecond timestamp should be read as seconds: %+v", items[1])
	}
}

func TestNewsKeepsSearchResultWhenFeedFails(t *testing.T) {
	srv := newsServer(t, `{"news":[{"title":"Only","link":"https://example.com/only"}]}`, http.StatusInternalServerError)
	defer srv.Close()
	p := newPlugin(&yahooapi.API{HTTP: httpx.New("test", 0), Base: srv.URL, FeedBase: srv.URL})
	items, err := p.News(context.Background(), sdk.NewsParams{Symbol: "SAAB-B.ST", Limit: 3})
	if err != nil || len(items) != 1 || items[0].Published != 0 {
		t.Fatalf("a failing feed must not hide the search result or invent a date: %+v %v", items, err)
	}
}

func TestResearchNewsStatesDatesExplicitly(t *testing.T) {
	srv := newsServer(t, `{"news":[]}`, http.StatusOK)
	defer srv.Close()
	p := newPlugin(&yahooapi.API{HTTP: httpx.New("test", 0), Base: srv.URL, FeedBase: srv.URL})
	res, err := p.Research(context.Background(), sdk.ResearchParams{Operation: "market_news", Symbol: "SAAB-B.ST"})
	if err != nil {
		t.Fatal(err)
	}
	rows := res.Data.(map[string]any)["news"].([]map[string]any)
	if len(rows) != 3 || rows[0]["published"] != "2026-11-07T19:23:00Z" || rows[0]["date_status"] != "published" || rows[2]["date_status"] != "unavailable" {
		t.Fatalf("%+v", rows)
	}
	if _, has := rows[2]["published"]; has {
		t.Fatal("an unknown date must be absent, not guessed")
	}
}
