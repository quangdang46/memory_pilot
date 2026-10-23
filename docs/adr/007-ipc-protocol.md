# ADR-007 — CLI ↔ daemon IPC

**Status:** Proposed
**Date:** 2026-10-23
**Related:** MP-REQ-069

---

## Context

The CLI needs to read state and request policy changes from the daemon. Requirements:

- local only (no network — MP-REQ-076);
- user-scoped permissions (only the owning user's CLI may connect);
- small payloads (status, reports, explain);
- robust to a dead/wedged daemon;
- stable enough to version;
- no heavy dependency.

---

## Options

**A. Unix domain socket, newline-delimited JSON (JSONL) request/response.** sbh uses a socket with a line protocol, concurrency and request-rate limits, and a 64 KiB line cap (`control.rs` constants: 8 concurrent connections, 10 req/s, 64 KiB line, 5 s IO timeout). Proven pattern.

**B. gRPC over Unix socket.** Schema-typed and versioned, but pulls in protobuf/HTTP2 for a local, single-user, low-rate API. Disproportionate.

**C. A state file the CLI reads directly.** sbh does this too (`state.json` heartbeat). Great for `status` (no daemon needed if stale-checked), useless for actions.

**D. HTTP over localhost TCP.** Rejected: opens a network surface, invites other local users/processes, violates the spirit of MP-REQ-076.

---

## Decision

**A + C combined:**

1. **Read-only status**: the daemon writes an atomically-replaced `state.json` (and a `metrics.prom`-style text file if useful) at a fixed cadence (start: 5 s, stale after 30 s). `mempilot status` prefers the socket but falls back to `state.json` with an explicit "(cached, daemon may be unresponsive)" marker. This means `status` works even when the daemon is wedged — a doctor-friendly property sbh also has.
2. **Queries and mutations**: JSONL over `AF_UNIX`, `SOCK_STREAM`, at `~/Library/Application Support/mempilot/control.sock` with mode `0600`, plus `SO_PEERCRED`/`LOCAL_PEERCRED` UID check.
3. **Protocol**: one JSON object per line. Request `{v:1, id, op, args}`; response `{v:1, id, ok, data|error}`. Ops are a closed set. Limits mirror sbh: 8 connections, 10 req/s, 64 KiB line, 5 s timeout.
4. **Versioning**: `v` in every message; the daemon rejects unknown major with a clear error naming supported versions.
5. **Mutations** (policy set, promote/demote) are the only writes from the CLI; they are validated by the daemon, never trusted from the client.

---

## Evidence

`[Verified: sbh]` this shape works on macOS in production — socket path limits (`MAX_SOCKET_PATH_BYTES = 100`), request rate limiting, IO timeouts, and a state file heartbeat are all in sbh's code. Copy the constraints; they were learned the hard way.

`[Open]`: `LOCAL_PEERCRED` availability in Go on darwin (`golang.org/x/sys/unix` exposes it; verify).

## Consequences

**+** No network surface; works when the daemon is wedged (state file); cheap.
**−** Two read paths (`status` from socket vs. file) that must agree; a hand-rolled protocol that must be documented and versioned.

## Revisit triggers

The op set grows enough to need real schema tooling, or a second client (e.g. a menu-bar app) appears.
