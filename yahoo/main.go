// Command yahoo is the FloweySales Yahoo Finance provider plugin.
package main

import (
	"context"
	_ "embed"
	"time"

	"github.com/Doldas/floweysales-plugins/internal/yahooapi"
	sdk "github.com/Doldas/floweysales/pluginsdk"
	"github.com/Doldas/floweysales/pluginsdk/research"
)

//go:embed plugin.json
var manifestJSON []byte

func main() { sdk.Main(newPlugin(yahooapi.New())) }

func newPlugin(api *yahooapi.API) sdk.Plugin {
	manifest, err := sdk.ParseManifest(manifestJSON)
	if err != nil {
		panic(err)
	}
	return sdk.Plugin{
		Manifest: manifest,
		Quote: func(ctx context.Context, p sdk.QuoteParams) (sdk.Asset, error) {
			return api.Quote(ctx, p.Symbol)
		},
		History: func(ctx context.Context, p sdk.HistoryParams) (sdk.HistorySeries, error) {
			return api.History(ctx, p.Symbol, p.Range)
		},
		Search: func(ctx context.Context, p sdk.SearchParams) ([]sdk.SearchResult, error) {
			return api.Search(ctx, p.Query)
		},
		Financials: func(ctx context.Context, p sdk.FinancialsParams) (sdk.FinancialsResponse, error) {
			return api.Financials(ctx, p.Symbol, p.FromYear, p.ToYear)
		},
		Events: func(ctx context.Context, p sdk.EventsParams) ([]sdk.CorporateEvent, error) {
			return api.Events(ctx, p.Symbol, p.From, p.To)
		},
		News: func(ctx context.Context, p sdk.NewsParams) ([]sdk.NewsItem, error) {
			return api.News(ctx, p.Symbol, p.Limit)
		},
		Research: func(ctx context.Context, p sdk.ResearchParams) (sdk.ResearchResult, error) {
			op, err := research.Find(manifest, p.Operation)
			if err != nil {
				return sdk.ResearchResult{}, err
			}
			q, err := research.Query(op, p, research.EquitySymbol)
			if err != nil {
				return sdk.ResearchResult{}, err
			}
			// market_news is the only operation: the same dated headlines as the news method.
			items, err := api.News(ctx, q.Get("symbol"), 8)
			if err != nil {
				return sdk.ResearchResult{}, err
			}
			rows := []map[string]any{}
			for _, it := range items {
				row := map[string]any{"title": it.Title, "url": it.URL, "source": it.Source, "date_status": "unavailable"}
				if it.Published > 0 {
					row["published"] = time.Unix(it.Published, 0).UTC().Format(time.RFC3339)
					row["date_status"] = "published"
				}
				rows = append(rows, row)
			}
			data := map[string]any{"news": rows}
			return research.Result("YAHOO", op, p.Symbol, yahooapi.DefaultBase+op.Path, data, ""), nil
		},
	}
}
