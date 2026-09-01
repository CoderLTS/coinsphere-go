# ADR-0005: Real Trading Safety Gate

## Status

Accepted for the SDK v3 Quant/Binance split.

## Decision

`official.binance` contains the complete Spot and USD-M one-way market-order path, but live execution is disabled by default. Paper is the default execution mode. Live execution is allowed only when all of the following are true:

- `liveTradingEnabled` is explicitly true for the action.
- An operator has manually confirmed the account.
- Maximum order notional, maximum instrument notional, daily loss, daily order count, maximum slippage, and maximum quote age are all configured and valid.
- Credentials are read only through `sdk.SecretReader`.
- The Binance API key has withdrawal permission disabled.

Every request carries an idempotent `clientOrderId`. Existing orders are returned instead of submitting a duplicate. REST is used for submission, startup recovery, reconnect recovery, and reconciliation; User Data Stream events are used for realtime state updates when that stream is enabled.

Audit records include order intent identity, provider order identity, status transitions, fills, risk rejections, recovery, and reconciliation outcomes. Audit records must not include API keys, secrets, tokens, signed query strings, or raw exchange payloads.

## Rollback and disablement

To stop live execution, set `liveTradingEnabled` to false and deactivate affected workflows. Binance Paper remains available. In-flight orders are reconciled through REST before the account is considered recovered; no automatic reversal order is created.

This ADR does not authorize production trading. Enabling live execution requires an account-level manual release outside Core, CI, and Codex.
