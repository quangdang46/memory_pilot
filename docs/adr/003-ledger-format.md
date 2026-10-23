# ADR-003 — Ledger format: JSONL first, SQLite only if proven necessary

**Status:** Proposed
**Date:** 2026-10-23
**Related:** MP-REQ-060, MP-REQ-061, MP-REQ-064

---

## Context

The ledger must support:

- append at ~1–10 records/s (events + aggregated samples);
- crash tolerance (kill -9 at any moment);
- query by time range and by correlation id (`decision_id`, incident id);
- retention and rotation;
- `explain` reconstructing a full narrative;
- bounded disk usage.

sbh uses **both**: SQLite (WAL) for queryable stats/aggregation and JSONL for append-only, crash-safe records, with a dual logger that degrades if one fails (`src/logger/dual.rs`). pt uses **Parquet + Arrow** for telemetry, and JSON/TOON for CLI output.

---

## Options

**A. JSONL only.** Append-only, trivially crash-safe (truncated last line is discarded), grep-able, readable. Query = scan the relevant segments; with 72 h of aggregated samples (~26k lines/series, a few MB), a linear scan is ~tens of ms. Aggregation over 30 days of *events* is small (hundreds of thousands of lines at most).

**B. SQLite only.** Fast queries, but a hot write path with WAL, a second failure mode, and a file that can be corrupted by a kill at the wrong moment (rare but real). Also adds a dependency and complicates the "no user data deletion" story (VACUUM, journal files).

**C. Both.** sbh's proven pattern — but it doubles the write path and requires reconciliation logic. sbh pays that cost because it needs interactive stats over large logs.

**D. Parquet.** Overkill; requires batching (not crash-safe for the tail); wrong tool for an append stream.

---

## Decision

**JSONL only in v1**, with a layered design that leaves room for an index:

1. **Write path**: a single writer goroutine, one JSON object per line, buffered, `fsync` every 5 s or 64 records.
2. **Segments**: `ledger-000123.jsonl`, rotated at 8 MiB; a `segment_end` record written before rotation; retention enforced by a slow hourly sweep.
3. **Query path**: a small in-memory index of `(segment, first_ts, last_ts, record_count)` rebuilt at startup by reading segment headers/footers (cheap: read first and last line of each segment).
4. **If a query proves too slow** (measured, not assumed), add a **sidecar SQLite index** that is *derivable from* JSONL and can be deleted without data loss. The JSONL stays the source of truth.

This mirrors sbh's principle (JSONL as the durable record) without paying the dual-write cost before there is evidence it is needed.

---

## Evidence

- Estimated volume: at 10 records/s for 24 h = 864k lines ≈ 200 MB uncompressed at ~230 B/line. That is **too much**, so aggregation is mandatory: samples are aggregated (1 per 10 s) and events are rare. Realistic: ~9k sample lines/day/series + ~1k events/day ⇒ a few MB/day.
- **E1:** measure `explain --at <ts>` scan time over 7 days of realistic segments. Target < 200 ms.
- **E2:** measure fsync cadence impact on sampling jitter.

## Consequences

**+** One writer, one failure mode, trivially crash-safe, human-inspectable, no new dependency.
**−** No ad-hoc SQL; aggregation must be done by our code; a 30-day event scan could get slow if we are careless with record size.

## Revisit triggers

E1 > 1 s for a common query, or the event volume grows by an order of magnitude.
