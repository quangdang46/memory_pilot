# Execution record — Phase 3 attribution & classification (2026-10-23)

- **Attribution (memory_pilot-evg)**: `internal/attribution` — identity tuple
  (pid, start_sec, start_usec, exe); deltas only across identical identities
  (TestIdentityChangeNoDelta: reborn pid contributes 0); positive growth and
  release tracked separately; coverage_ratio may exceed 1 and is NEVER
  normalized (TestCoverageNotNormalized asserts >1 and the honest note);
  compressor is a system-level contributor line ("belong to no process");
  large-but-stable section separate from growth ranking (TestLargeButStable).
- **Workload grouping (memory_pilot-nqu)**: nearest-anchor with sticky
  membership within a window; anchor table (iTerm2, Terminal, node, zsh, ...);
  ppid lineage pending platform exposure (recorded limitation).
- **Coverage accounting (memory_pilot-v7w)**: Attempted/Succeeded/Denied from
  the platform snapshot flow into every window; low coverage → classification
  refuses with an explicit reason (honest denial).
- **Classification (memory_pilot-e89)**: `internal/classify` — 5 buckets
  (workload_growth, cache_or_retrievable, subprocess_accumulation,
  leak_suspicion, unknown); every verdict carries mandatory basis;
  suspicion requires ALL criteria (sustained ≥20min, quiet ≥10min, no
  plateau, not cache-role, adequate coverage) and lists alternatives it
  could not exclude.
- **Leak suspicion / what-would-change-mind (memory_pilot-vim)**: rendered
  verdict includes confidence + basis; alternatives always enumerated for
  leak_suspicion (TestFiveBuckets).
- **Phrase test (G-P3)**: `classify.PhraseTest` forbids "is leaking",
  "the cause is", "safe to kill", 100%-share claims; RenderVerdict passes by
  construction; adversarial text correctly flagged (TestPhraseTest).
- **Schema round-trip (memory_pilot-zh5)**: `internal/wire` — typed Go
  mirrors of decision / action-result / forecast / pressure-state /
  attribution / classification / state-file; ALL 16 valid fixtures round-trip
  to canonical JSON (null-vs-absent normalized per schema nullability).
- **Test policy (memory_pilot-ttr)**: table-driven tests; fixtures from
  schemas/testdata; no network (ST-4 gate); no sleeps (clock injection
  everywhere; dwell tests use monotonic timestamps).
- **Test harness (memory_pilot-uqj)**: `bin/mpreplay -all` replays the corpus
  with per-trace hashes and fails on any mismatch (e2e harness for scenarios);
  corpus regen verified bit-identical.

All tests green; ST-1..ST-4 gates pass.
