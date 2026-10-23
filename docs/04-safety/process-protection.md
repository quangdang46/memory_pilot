# MP-SAFE-003 — Process & Session Protection

**Status:** Draft
**Version:** 0.1.0
**Note:** Because SI-1 removes all process-signaling capability, this document is about *protection of attention and reporting*, not protection from our own actions. That is a deliberate inversion of pt's design: pt must protect processes from its own actions; we have no actions to protect them from.

Two things still need protection:

1. **The user's judgment** — the product must not push the user toward killing a session.
2. **The sessions themselves** — some processes are so important that even *reporting* on them must be careful, and future providers must never touch them.

---

## 1. Protected classes

| Class | Examples | Rule |
|---|---|---|
| Coding-agent sessions | `claude`, `codex`, agent binaries and their trees | never a target of any action; if it is the top grower, report it factually and add a warning that it may be holding state |
| Terminals / IDEs | Terminal, iTerm2, VS Code, Cursor, JetBrains | reported, never targeted |
| System daemons | `launchd`, `WindowServer`, `kernel_task`, `mdworker`, `mDNSResponder` | reported as system context, not as candidates |
| The user's own marked group | any group the user protected via `mempilot policy set --protect` | excluded from action targeting; still reported (transparency) |

**Why report protected processes at all?** Because "the IDE is using 4 GB" is useful information. Excluding it from reports would be dishonest and would break attribution ("why does nothing add up?"). Protection applies to *targeting*, not to *mentioning*.

---

## 2. The "do not suggest killing" rule

Reports may say a group grew. They may say the growth is unexplained. They **must not** say "quit this app to fix it" as a generic recommendation. The permitted forms:

- `EmitAdvisory` for a *system-level* risk: "pressure is critical; closing unused applications would help" — no specific app named.
- An advisory naming a group only when the group is not protected **and** the finding is `leak_suspicion` — and even then, phrased as a user decision with stated cost.

Rationale: the machine cannot know whether a 3 GB agent is mid-refactor with unsaved state.

---

## 3. Session-lifetime awareness

A long-running session is not the same as an idle process. Signals we may use (read-only) to avoid mis-classifying:

- process age > threshold AND recent file activity in a workspace directory (if cheaply observable),
- terminal foreground/background state (not reliably available; do not depend on it),
- child process activity.

We use these to *soften* language ("this session has been idle for 40 min but has an active child process"), never to decide anything irreversible. There is nothing irreversible to decide.

---

## 4. Protection registry

```
[protection]
protect_groups = ["IDE:Code", "Terminal:iTerm2"]
protect_never_report_as_candidate = true     # default true
protect_additional = [/path/to/critical-binary]
```

- Patterns match against the group anchor's `exe_path` and `name`.
- A broken or unparsable pattern is a **config error** and the daemon refuses to start with a clear message (fail closed; a silently ignored protection rule is worse than no rule).
- `mempilot policy status` lists active protections and their source.

---

## 5. Interaction with future providers

If a provider is added that targets a subsystem (e.g. asking the compositor to release glyph caches), it must declare an exclusion list:

- provider must state the process classes it affects;
- the protection registry must be consulted *before* the provider runs;
- the provider must refuse if a protected group would be affected, and record the refusal.

This is enforced by the provider contract (`action-authorization.md` §2) — the `Preconditions` must include `no_protected_group_affected`.

---

## 6. Tests

| Test | Proves |
|---|---|
| T-PP-1 | An invalid protection pattern prevents daemon start with an actionable error |
| T-PP-2 | Protected group still appears in reports but with `targetable: false` |
| T-PP-3 | A provider declaring an excluded group refuses to run when that group is active |
| T-PP-4 | No advisory text contains an imperative to quit a named protected app (string test over a corpus of rendered advisories) |
| T-PP-5 | Protection survives restart and config reload |
