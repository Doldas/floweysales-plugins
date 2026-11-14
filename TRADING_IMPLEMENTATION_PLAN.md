# Trading plugin platform — implementation plan

Status: **approved for design, offline contracts and paper/sandbox testing only**. Live-money execution remains **blocked** by [FloweySales #8](https://github.com/Doldas/floweysales/issues/8) until a separate, documented security/regulatory/broker-terms decision and explicit authorization. This plan is not that authorization.

Tracking:
- Plugin architecture: [floweysales-plugins #14](https://github.com/Doldas/floweysales-plugins/issues/14)
- Avanza pilot: [floweysales-plugins #15](https://github.com/Doldas/floweysales-plugins/issues/15)
- Core adapter/SDK: [floweysales #444](https://github.com/Doldas/floweysales/issues/444)
- Universal provider architecture: [floweysales #421](https://github.com/Doldas/floweysales/issues/421)
- Broker safety and licensing gate: [floweysales #8](https://github.com/Doldas/floweysales/issues/8)

## 1. Product contract and boundaries

**One Trading Host plugin** should provide the trading experience, paper trade coordination, account selector, strategy binding, risk-control UX and standard domain operations.

**Many independent Broker Extension plugins** implement the shared broker API. They can be installed, upgraded, enabled or disabled separately. A broker may implement read-only account discovery, sandbox/paper operations, or eventually a reviewed live capability; capability flags do not themselves confer execution authority.

**FloweySales core** retains its existing generic plugin manager, user authentication/authorization, secret vault, tenant isolation and permission boundary. It exposes stable versioned broker services/capability discovery and performs privileged, auditable routing. No IBKR/Saxo/Alpaca/Kraken/OANDA/IG/Avanza-specific branching in the core or generic trading UI.

**No unmanaged sub-plugin executables:** Broker extensions are normal first-class plugins managed by FloweySales, not arbitrarily spawned by the Trading Host. The host communicates through a core-mediated, authorized broker-extension registry. Never give one broker plugin another broker's secrets or account tokens.

**1 → N brokers:** one user can connect multiple brokers and multiple accounts under one broker. Each order/strategy must explicitly bind to exactly one user-owned broker account. Viewing consolidated positions may aggregate read-only metadata; execution must **never** auto-fail-over to another broker or silently split orders.

**A collection/watchlist is not an account.** Market Collections may be a strategy scope but must not be mistaken for holdings or imply order authority.

## 2. Confirmed existing foundation

In `floweysales`:
- `pluginsdk/` and `internal/plugins/`: protocol v1, installed-plugin lifecycle, manifest and conformance.
- `internal/trading/`: currently contains pure paper simulation, order validation, risk limits and matching; **must not be deleted before migration**.
- `internal/server/orders.go`, `paper_approvals.go` and `trading_settings.go`: current API, approvals, audit trail and persistence.
- `internal/storage/`: paper order history. Keep migrations backward-compatible and verifiable.
- `#421`: generic providers and user-scoped instance routing are under active design.

In `floweysales-plugins`:
- Existing `avanza/` is an **unofficial experimental, read-only market-data plugin**.
- Existing `binance/` is **public market data only**, not a trading connector.
- `plugin.json`, Go SDK `plugintest.Conformance`, install/store/release scripts and platform CI can be reused.
- Current `go.mod` uses a tagged FloweySales pluginsdk; do not assume a new trading capability is compatible with existing protocol v1 without negotiating a versioned extension.

Inventory all actual current code before implementing changes. Do not duplicate or prematurely extract working components.

## 3. Capability and data contract

Create typed, versioned contracts in **core SDK** and corresponding broker extension conformance tests. Suggested capabilities (independently declared, no implied permission):

| Capability | Minimum shape |
|---|---|
| `broker.accounts.list` | account handle, broker ID, supported environment, asset classes, currency, connection health |
| `broker.balances.read` | available/settled cash by currency, as-of timestamp, permission/provenance |
| `broker.positions.read` | canonical instrument/listing identity, quantity, currency, cost basis if available, as-of |
| `broker.order.preview` | sanitized proposed order, broker rules, estimated fees, local policy decision |
| `broker.orders.read` | order ID, client correlation ID, status, status timestamp, fills |
| `broker.fills.read` | fills, remaining quantity, execution price, fees, timestamps |
| `broker.order.submit` | explicitly gated high-risk operation; no live permission at initial release |
| `broker.order.cancel` | explicitly gated high-risk operation |
| `broker.order.replace` | optional, gated high-risk operation |

Data types: `BrokerDescriptor`, `BrokerAccountRef`, `BrokerConnection`, `OrderIntent`, `OrderPreview`, `OrderState`, `Fill`, `BrokerError`, `BrokerCapability`, `CapabilityConstraints`, `InstrumentListingRef`, `RiskDecision`.

Requirements:
- Distinguish `paper`, `sandbox` and `live` in every account and order.
- Explicit provider + broker account + instrument listing + market/venue + currency + quantity + side + order type + TIF.
- Avoid unsafe float arithmetic for money/quantity; use decimal/fixed-precision representations and explicit rounding per venue.
- Robust order state machine including pending, accepted, rejected, partially filled, filled, cancel requested/cancelled, unknown/needs reconciliation.
- UTC audit timestamps; market session, locale/currency and exchange timezone are independently represented.
- Durable idempotency key, account-bound request fingerprint and request correlation ID.
- Bounded timeout and typed error codes: missing credentials, permission denied, rate limited, provider down, unsupported capability, validation error, unknown completion.
- No retry of ambiguous submit requests before broker reconciliation and idempotency check.
- No reliance on a market-data quote as guaranteed executable price.

## 4. Delivery stages (small, independently reviewable PRs)

### P0 — Design + core/plugin compatibility (CORE #444; PLUGINS #14)
- Inventory current paper/order functionality, API consumers, flow nodes, database tables and existing SDK.
- Agree on versioned broker extension descriptors/capabilities and core broker-service discovery.
- Add one cross-repo contract fixture and interoperability matrix. Resolve whether SDK v1 can be extended compatibly or needs a negotiated v2. **Never silently break existing plugins.**
- Define ownership: core owns auth/permissions/secrets/plugin lifecycle; trading host owns business-facing orchestration; broker extensions own broker-specific adapter logic.
- Document threat model and trust boundaries, including malicious plugins and local process privileges.

**Done when:** two independently installed synthetic broker descriptors register; incompatible versions fail safely; user A cannot enumerate user B's accounts.

### P1 — Trading Host + Paper Broker extension (PLUGINS #14)
- Build `trading/` host as optional installable plugin; build `paper-broker/` as separate installable broker extension.
- Use a core-mediated registry, user-scoped broker instances and explicit account binding.
- Port/test current paper matching behavior and risk policy, **preserving old history and existing APIs through an adapter**.
- Test restart/upgrade, open orders, outstanding approvals, audit trail, partial fills and cash/position reconciliation.
- No existing real-money broker interaction.

**Done when:** existing paper trading remains usable after upgrade and can be disabled independently without deleting saved history; another synthetic broker can coexist with the Paper Broker.

### P2 — User account management + UI (CORE #444; PLUGINS #14)
- Trading UI includes broker-extension catalog, installed broker providers, one-to-many account connections, account modes, health, capability support, risk policy and budget.
- Separate **Settings → Plugins** (installation) from **Trading → Broker Connections** (per-user connection setup). SYS_ADMIN may manage allowed plugins/system policy, but never use or expose arbitrary users' brokerage secrets.
- Add explicit broker account selector to order preview, manual ticket and strategy binding.
- Read-only aggregation across accounts is opt-in and labels broker, currency, freshness and limitations. No cross-account netting by default.
- Empty/no-broker/no-permission/provider-unavailable states should remain navigable.

**Done when:** UI connects/disconnects two independent test brokers; a saved strategy references one specific paper account; no other account can be silently selected on failure.

### P3 — Individual broker adapters (one independent PR each)
Start with read-only/sandbox adapters and **verify current API terms and account eligibility individually**:

1. **Alpaca:** paper/sandbox first, accessible integration and order lifecycle test fixtures.
2. **IBKR:** separate account and session integration model; determine approval needs and supported APIs.
3. **Saxo:** OpenAPI and simulated environment; licensing and third-party distribution assessment.
4. **Kraken:** sandbox/spot-only where supported; enforce explicit trading permissions.
5. **OANDA:** practice account and regional product constraints.
6. **IG:** demo environment, session/rate limits and derivative risk warning.
7. **Avanza:** extend existing plugin with an optional broker-extension surface; **read-only/mock first** pending authoritative permission for authenticated order APIs. See #15.

Each must declare coverage, region, instrument types, supported order types, permissions, sandbox/live availability, use restrictions, credentials, as-of and risk limitations. Unavailable/unapproved live capabilities remain disabled; do not invent an official API.

**Done when:** each implementation has contract tests and documented access requirements; a broker that cannot legally support live orders still works as read-only/paper where appropriate.

### P4 — Risk, audit and fail-safe verification (CORE #8, #444)
- Fail closed for unknown broker/account, stale quotes, missing price currency, unsupported product, lost login, permission mismatch, missing capability.
- Per-account and cross-account user limits: max notional per order, daily notional, risk budget, sector/asset exposure, leverage ban by default, market-hours checks and time-limited approvals.
- Hard kill switch enforced by core policy, not relying only on the adapter.
- Explicit manual approval for every proposed live order during any pilot, with immutable audit evidence.
- Handle duplicate submits, partial fills, lost responses, reconnection, cancel races and position reconciliation.
- No AI/Flow Studio privilege escalation from recommendation to live execution.

**Done when:** fault injection and cross-account isolation tests prove no unauthorized order can be sent; red-team threat review documented.

### P5 — Optional limited-capital Avanza pilot (BLOCKED until approvals)
Maintainer may prepare **one dedicated account with a fixed investment balance**, but funding is not itself authorization for automated order APIs.

Pilot prerequisites: verified Avanza authorization/contractual right for this integration; regional financial/regulatory review; explicit user consent; independent security approval; documented rollback/kill switch and incident response; written release of CORE #8 gate.

Additional restrictions: named account allowlist; no borrowing, margin, shorting or derivatives; per-order, daily, per-symbol and cumulative risk caps **strictly lower than funded balance**; manual order confirmation; log broker request IDs without logging credentials; reconciliation before retry; limited trading hours. Run a staged dress rehearsal with synthetic orders first.

**Done when:** pilot approval exists in writing, audit trail and incident controls are verified, and an operator can halt all new orders. If approval cannot be obtained, keep the Avanza extension read-only/paper and support external handoff instead.

## 5. Test/CI matrix

| Test | Core | Trading Host | Broker extensions | Browser |
|---|---|---|---|---|
| Manifest/version conformance | Yes | Yes | Yes | — |
| Two brokers + multiple account instances | Yes | Yes | Yes | Yes |
| User A / User B secret isolation | Yes | Yes | Yes | Yes |
| Paper orders, partial fills and cancellation | Yes | Yes | Paper + mock | Yes |
| Fail closed with plugin missing/disabled | Yes | Yes | Yes | Yes |
| Retry/unknown completion/reconciliation | Yes | Yes | Synthetic adapters | — |
| Migration from existing paper history | Yes | Yes | Paper | Yes |
| Source/currency/market semantics | Yes | Yes | Yes | Yes |
| Live-money order placement | **Never in CI** | **Never in CI** | **Never in CI** | **Never in CI** |

Normal CI uses fake credentials and deterministic fixtures; production tokens/BankID/session tokens never enter PR builds. Dedicated live-account read-only probes require opt-in, strict account scope and protected environment. Staging tests must be non-funded and use documented demo/sandbox endpoints.

## 6. Release and compatibility

1. Publish contract/SDK tags and golden fixtures.
2. Build and test host, paper extension and synthetic broker independently.
3. Ship under disabled feature flag (no live brokerage connectivity).
4. Introduce UI and persistence migrations, with rollback/upgrade tests.
5. Package broker adapters as independent plugin releases in store with signed/checksummed assets and install compatibility metadata.
6. Enable paper experience; observe audit, performance and provider health.
7. Review real-money rollout **only** after #8 approval and broker-specific access decisions.

Do not delete `internal/trading`, `internal/server/orders.go` or paper database tables until parity and migrations are verified. A thin transition adapter is preferable to a risky rewrite.

## 7. Claude execution instruction

Work in small PRs across both repositories. Start at **P0**, produce a code-level dependency and compatibility inventory, implement the extension descriptor/conformance layer, then a minimal Trading Host + Paper Broker proof of concept.

For each PR supply: issue links, affected contracts, migrations, tests, test results, safety limitations and next work item. Never claim a broker is integrated until its authenticated supported capabilities have been validated. Never use live-money order endpoints or real credentials based solely on this roadmap.

**Definition of success for first milestone:** the Trading Host discovers and selects two independent **paper/mock** broker extensions under one user, preserves per-account separation, and survives one extension being disabled — without enabling real-money order execution.
