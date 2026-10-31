# FloweySales plugins

The official market-data, news, macro and FX provider plugins for
[FloweySales](https://github.com/Doldas/floweysales), published to the plugin
store at **https://doldas.github.io/floweysales-plugins/index.json** (the
backend's default `PLUGIN_STORE_URL`).

| Plugin | Provider | Source | Capabilities | Notes |
|---|---|---|---|---|
| [`yahoo`](yahoo/plugin.json) | `YAHOO` | Yahoo Finance public web interfaces (unofficial) | quote, history, search, financials, research, news | Install terms must be accepted |
| [`binance`](binance/plugin.json) | `BINANCE` | Binance public REST API (official) | quote, history, search, financials (none for crypto), research | No API key |
| [`finnhub`](finnhub/plugin.json) | `FINNHUB` | Finnhub API (official) | quote, history, search, financials, research, news | Needs `FINNHUB_API_KEY` |
| [`avanza`](avanza/plugin.json) | `AVANZA` | Avanza public web endpoints (unofficial, experimental) | quote, history, search, financials, research, forum | Install terms must be accepted; Yahoo history fallback is labelled `YAHOO` |
| [`fred`](fred/plugin.json) | `FRED` | Federal Reserve Bank of St. Louis FRED API (official) | quote, history, search | Macro series as symbols (`DGS10`, `T10Y2Y`, `CPIAUCSL`, `UNRATE`); needs a free `FRED_API_KEY` |
| [`ecb`](ecb/plugin.json) | `ECB` | European Central Bank data API (official) | quote, history, search | Euro reference rates and derived crosses (`EURUSD`, `USDSEK`); no key |

Each plugin's `plugin.json` is authoritative for its research operations,
poll interval and settings.

The `news` capability feeds the **News headlines** node in Flow Studio; FRED
and ECB series work in every watchlist, technical and screen node, so a flow
can watch the 10-year yield or USD/SEK next to its stocks.

## Installing

In FloweySales: **Settings → Plugins** (SYS_ADMIN), or on the server:

```sh
floweysales_backend plugins install yahoo --accept-terms
floweysales_backend plugins install binance
```

## How this repository relates to FloweySales

FloweySales owns the interfaces: the wire protocol, the manifest, the store
index format and the Go SDK
[`github.com/Doldas/floweysales/pluginsdk`](https://github.com/Doldas/floweysales/tree/main/pluginsdk),
documented in
[docs/plugins.md](https://github.com/Doldas/floweysales/blob/main/docs/plugins.md).
This repository only implements plugins on top of them. Every plugin runs
`plugintest.Conformance` from the SDK in its tests.

## Development

Go (see `go.mod`) and Node.js 24+.

```sh
go vet ./... && go test -race ./...
scripts/build-plugins.sh                 # this platform → dist/plugins + index.json
PLUGIN_STORE_URL=file://$PWD/dist/plugins/index.json floweysales_backend plugins store
floweysales_backend plugins install --file dist/plugins/yahoo-1.0.0/floweysales-plugin-yahoo-linux-amd64 --accept-terms
```

Provider rules from FloweySales apply: missing figures stay absent (never
`0`), currency is never guessed, a fallback source is named in `provider`,
research is read-only and allowlisted, and upstream terms, authentication
limits and rate limits are respected.

## Releasing

1. Bump `version` in `<id>/plugin.json` and merge to `master`.
2. Push the tag `<id>-v<version>` (for example `yahoo-v1.1.0`).

The **Release** workflow tests everything, builds linux/amd64, linux/arm64,
windows/amd64, darwin/amd64 and darwin/arm64 binaries, publishes a GitHub
release with them, `plugin.json` and `SHA256SUMS`, and dispatches the
**Store index** workflow, which regenerates `index.json` from all releases
and deploys it to GitHub Pages. FloweySales installations see the new
version on their next store check.

To update the SDK: `go get github.com/Doldas/floweysales/pluginsdk@<version>`.
