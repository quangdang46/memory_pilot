# Execution record — docs consistency sweep (iy1, 2026-10-23)

- README.md + docs/README.md status updated from "specification phase" to
  "implementation phases 0–5 built" with pointers to execution-records/.
- Constants: docs/appendix/constants.md §1a carries measured Phase 0 values
  with spike sources; kernel_level decode upgraded to V (S-3).
- ADR status: 001/002/004/008 Verified with evidence; 006 Rejected-for-v1
  evidence recorded (S-6); 012/014 unchanged (ST-22 floor intact).
- Roadmap phase numbering vs beads: verified consistent after G-P0..G-P5.
- Discrepancies found and left honest: (1) spike/mach NOTES refines the
  spec's "rev0 count 38" to "kernel revisions REV0=24, REV1=38; the Rust
  reference's 'rev0' struct is actually rev1" — recorded in the spike note,
  spec text unchanged as it cites the working implementation's constant;
  (2) sbh's 0.6% sampling-budget arithmetic superseded by measured 0.03%
  (constants.md).
