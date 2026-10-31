package main

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	sdk "github.com/Doldas/floweysales/pluginsdk"
	"github.com/Doldas/floweysales/pluginsdk/httpx"
	"github.com/Doldas/floweysales/pluginsdk/plugintest"
)

const csvBody = "KEY,FREQ,CURRENCY,CURRENCY_DENOM,EXR_TYPE,EXR_SUFFIX,TIME_PERIOD,OBS_VALUE\n" +
	"EXR.D.USD.EUR.SP00.A,D,USD,EUR,SP00,A,2026-10-29,1.10\n" +
	"EXR.D.USD.EUR.SP00.A,D,USD,EUR,SP00,A,2026-10-30,1.12\n" +
	"EXR.D.SEK.EUR.SP00.A,D,SEK,EUR,SP00,A,2026-10-29,11.00\n" +
	"EXR.D.SEK.EUR.SP00.A,D,SEK,EUR,SP00,A,2026-10-30,\n"

func plugin(t *testing.T) (sdk.Plugin, func()) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/service/data/EXR/D.") || r.URL.Query().Get("format") != "csvdata" {
			t.Errorf("unexpected request %s", r.URL)
		}
		_, _ = w.Write([]byte(csvBody))
	}))
	return newPlugin(&ecb{http: httpx.New("test", 0), base: srv.URL, now: func() time.Time { return time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC) }}), srv.Close
}

func TestEuroAndCrossRates(t *testing.T) {
	p, done := plugin(t)
	defer done()
	usd, err := p.History(context.Background(), sdk.HistoryParams{Symbol: "eur/usd", Range: "1M"})
	if err != nil || len(usd.Points) != 2 || usd.Currency != "USD" || usd.Points[1].Close != 1.12 {
		t.Fatalf("%+v %v", usd, err)
	}
	// USDSEK = SEK per EUR / USD per EUR, only on days with both fixings.
	cross, err := p.History(context.Background(), sdk.HistoryParams{Symbol: "USDSEK", Range: "1M"})
	if err != nil || len(cross.Points) != 1 || math.Abs(cross.Points[0].Close-10) > 1e-9 || cross.Currency != "SEK" {
		t.Fatalf("missing fixings must not produce a rate: %+v %v", cross, err)
	}
	q, err := p.Quote(context.Background(), sdk.QuoteParams{Symbol: "USD"})
	if err != nil || q.Symbol != "EURUSD" || q.Data["fixing_date"] != "2026-10-30" {
		t.Fatalf("%+v %v", q, err)
	}
	for _, bad := range []string{"EURXYZ", "USDUSD", "E", "BTCUSD"} {
		if _, err := p.History(context.Background(), sdk.HistoryParams{Symbol: bad, Range: "1M"}); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
}

func TestSearch(t *testing.T) {
	if hits := search("krona"); len(hits) != 2 || hits[0].Symbol != "EURISK" {
		t.Fatalf("%+v", hits)
	}
	if hits := search("usd/sek"); len(hits) != 1 || hits[0].Symbol != "USDSEK" {
		t.Fatalf("%+v", hits)
	}
}

func TestConformance(t *testing.T) {
	plugintest.Conformance(t, newPlugin(&ecb{http: httpx.New("test", 0), base: defaultBase, now: time.Now}))
}
