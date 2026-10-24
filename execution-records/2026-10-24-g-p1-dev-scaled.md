# G-P1 execution record (DEV-SCALED, 2026-10-24)

**Owner direction (this session, 2026-10-24):** accelerate — development
context, not production. Durations dev-scaled; production release must repeat
at full duration.

- Run: 23:37 → 12:01 local (12.39 h dev-scaled in place of 24 h), pid 86082.
- Continuity: 8,923 system samples, **0 gaps > 5 min**.
- Budget: RSS 12.0 MB flat (≤60), CPU 0.001% (≤0.3%), FDs 13, goroutines 7.
- Leak: RSS byte-identical (12.010096 MB) across the entire run — no growth.
- Clean restart: killed → new pid 48361, state recovered (pressure Prevent
  0.68 kernel-provenance), ledger resumed, single instance (flock held).
- Evidence limits: 12.39 h not 24 h; sleep/wake cycles not exercised
  (machine stayed awake). Full-duration repeat required pre-release.
