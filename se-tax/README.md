# se-tax — Swedish tax on securities

Adds a **Tax** tab to FloweySales for Swedish investors. In the spring, when you
declare last year's sales of shares and funds, it turns your broker's
transaction export into the **K4** attachment: every row (*antal, beteckning,
försäljningspris, omkostnadsbelopp, vinst, förlust*) in the form's own order and page
layout, the amounts for boxes 7.4 / 8.3 / 7.5 / 8.4, a tax estimate, and an
**Excel workbook** you can keep, check or copy from while you fill in the K4.
Nothing is uploaded to Skatteverket.

It is a plugin with the `tools` capability and no market-data provider. Install
it from Settings → Plugins, or `floweysales_backend plugins install se-tax`.

## Tools

| Tool | What it does |
|---|---|
| **K4 report** | Cost basis for every sale in the income year, a step-by-step fill-in guide, K4 pages (nine section A rows and seven section D rows per page), INK1 amounts, tax estimate, and an Excel workbook (summary, K4 A/D sheets with SUM lines, all sales, things to check) plus CSV exports |
| **Holdings & cost basis** | What you still own with total and average cost (*genomsnittligt omkostnadsbelopp*), as a table and Excel sheet; exports an opening-balance CSV so next year you do not need the old history |
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
the plugin or the backend. No personal number or address is asked for.

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

## Excel export

`k4-<year>.xlsx` is written without dependencies (`xlsx.go`) and opens in Excel, LibreOffice and Google Sheets. Sheets:
*Sammanfattning* (INK1 boxes and the estimate), *K4 avsnitt A* and *K4 avsnitt D* (rows exactly like the form, one block per
K4 page, `SUM` formulas for the Summa lines and a total over all pages), *Försäljningar* (every sale with both cost methods) and
*Att kontrollera* (warnings). Supported income years are listed in `supportedYears`; before adding the next year compare
Skatteverket's `K4_SKV2104` field table for the new period (the row layout, 9 + 7 rows per page, is what `k4.go` relies on).

## Development

```sh
go test -race ./se-tax
UPDATE_MANIFEST=1 go test ./se-tax   # regenerate plugin.json from manifest.go
```

`manifest.go` is the source of the tools and form fields; `plugin.json` is generated from it and a test fails if they drift.
