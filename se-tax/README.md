# se-tax — Swedish tax on securities

Adds a **Tax** tab to FloweySales for Swedish investors. In the spring, when you
declare last year's sales of shares and funds, it turns your broker's
transaction export into the **K4** attachment: every row (*antal, beteckning,
försäljningspris, omkostnadsbelopp, vinst, förlust*), the amounts for boxes
7.4 / 8.3 / 7.5 / 8.4, a tax estimate, and the **BLANKETTER.SRU** + **INFO.SRU**
files that Skatteverket's service can import.

It is a plugin with the `tools` capability and no market-data provider. Install
it from Settings → Plugins, or `floweysales_backend plugins install se-tax`.

## Tools

| Tool | What it does |
|---|---|
| **K4 report** | Cost basis for every sale in the income year, K4 pages (nine section A rows and seven section D rows per page), INK1 amounts, tax estimate, CSV exports and the SRU files |
| **Holdings & cost basis** | What you still own with total and average cost (*genomsnittligt omkostnadsbelopp*); exports an opening-balance CSV so next year you do not need the old history |
| **What if I sell?** | Cost basis, gain and the change in this year's tax for planned sales, including the effect of losses |
| **Tax estimate** | A calculator from totals: 30% tax, the 70% rule, the tax reduction on a deficit |
| **Review import** | The accounts in your files (to leave out ISK/KF/IPS), transaction types and the type guessed for each instrument |
| **Guide & rules** | The rules applied, what is not covered, and a CSV template for other brokers |

## Input

* **Avanza** transaction export (`Datum;Konto;Typ av transaktion;Värdepapper/beskrivning;Antal;Kurs;Belopp;…;ISIN`).
  Several files can be added; give the whole history so the average cost is right.
* **Generic CSV** (`date,type,name,isin,class,quantity,amount_sek,fee_sek,account`) for other brokers, opening balances
  (`type=opening`, from the Holdings tool) and manual corrections. A template is in the Guide tool.

Files are processed in memory for the one request. Nothing is stored or logged by
the plugin or the backend, and the personal number is only written into the SRU file.

## What it computes

* **Average cost** (*genomsnittsmetoden*) per security, with the purchase fee in the cost and the sale fee deducted
  from the proceeds; a split changes units, not total cost.
* **20% flat rate** (*schablonmetoden*) for listed shares, taken per sale when it is better than the average cost
  (or never, with "Average cost only"). Funds always use average cost.
* **K4 sections**: A for listed shares and funds (INK1 7.4 / 8.3), D for crypto and unlisted securities (7.5 / 8.4).
  Amounts are whole kronor; quantities in section A are whole units (fractions are rounded and flagged).
* **Estimate** of Skatteverket's offsetting (*kvittning*) and 70% rule (*kvotering*): losses on listed shares and
  equity/mixed funds offset gains in full, other losses count at 70%; tax is 30%, and a deficit gives a tax
  reduction of 30% up to 100 000 kr and 30 000 kr + 21% of the excess. Skatteverket does the real calculation.

## Not covered

Options, futures, CFDs, warrants and currency trading (K4 sections B/C), deferred gains, spin-offs and mergers,
gifts and inheritances, qualified shares. The tool says so in the Guide and warns when a sale cannot be
matched to a purchase history or shares arrived by transfer without a cost.

## SRU files

`BLANKETTER.SRU` holds one `K4-<year>P4` block per page with the field codes from Skatteverket's K4 field table
(SKV 2104; the 2025P4 and 2026P4 tables are identical), ISO-8859-1, CR+LF. `INFO.SRU` carries the sender
(personal number, name, postal code and town). Do not rename the files.
Supported income years are listed in `supportedYears`; before adding the next year, compare Skatteverket's
`K4_SKV2104` field table for the new period with the codes in `sru.go`.

## Development

```sh
go test -race ./se-tax
UPDATE_MANIFEST=1 go test ./se-tax   # regenerate plugin.json from manifest.go
```

`manifest.go` is the source of the tools and form fields; `plugin.json` is generated from it and a test fails if they drift.
