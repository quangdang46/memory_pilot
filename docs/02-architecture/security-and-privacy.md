# MP-ARCH-006 — Security & Privacy

**Status:** Draft
**Version:** 0.1.0

The product observes process activity on the user's machine and writes it to disk. That is a meaningful privacy surface even though nothing leaves the machine. This document states the threat model, the data inventory, and the rules.

---

## 1. Threat model

| Threat | Adversary | Mitigation |
|---|---|---|
| Another local user reads our data | another logged-in user on the same Mac | data dir created `0700`; files `0600`; socket `0600` + peer-UID check |
| Another local app impersonates the CLI | a malicious local process running as a *different* user | peer credential check; the socket is in a `0700` directory |
| A malicious local process running as the *same* user | same-user malware | **out of scope** — same-user malware can do anything; we do not claim to defend against it |
| Data exfiltration | us | **no network code at all** (MP-REQ-076, ST-4). This is the strongest guarantee we can make |
| Command injection via config | a crafted config | strict TOML parsing, no shell, no `os/exec` (SI-1) |
| Path traversal via config/name | crafted config | store containment (SI-3, ST-11) |
| Ledger poisoned to mislead `explain` | local attacker | ledger is not authenticated; out of scope — it is a diagnostic, not an audit control |
| Privilege escalation | — | none: we run as the user, never root, never setuid |

---

## 2. Data collected (complete inventory)

| Category | Examples | Sensitivity | Retention |
|---|---|---|---|
| System memory metrics | free pages, compressor, swap, kernel level | low | 72 h hot / 30 d warm / 1 y cold |
| Disk headroom | free/total bytes | low | same |
| Process identity | pid, ppid, start time, name, exe path | **medium** — reveals installed software | 72 h hot at full detail; aggregated after |
| Process memory/CPU deltas | resident, CPU ns | low | 72 h → aggregated |
| Group labels | anchor application names ("iTerm2", "VS Code") | medium | as above |
| Events | state transitions, decisions, advisories | low | 30 d |
| Configuration | thresholds, protected patterns | low | until changed |
| **Not collected** | command lines of other users, environment, cwd, open files, window titles, URLs, network connections | — | — |

**Deliberate omission: `proc_pid_cwd`.** sbh collects it (for build-directory detection). We do not; it reveals project paths and provides no benefit for pressure attribution.

---

## 3. Rules

1. **Local only.** No socket to the network, no DNS, no telemetry upload. Enforced by a build gate.
2. **Minimum necessary.** Every collected field must name the algorithm that consumes it. If a field is unused, it is removed.
3. **Names are not hashed by default.** Hashing process names would make reports useless to the user (who is the only reader). The protection is filesystem permissions, not obscurity.
4. **Redaction on export.** Recorded traces and incident bundles replace home paths and usernames with stable placeholders before they leave the data directory (e.g. when committed as test fixtures).
5. **No secrets.** Config contains no credentials. If a future provider needs one, it is stored in the Keychain, not in our config.
6. **Deletion is user-controlled.** `mempilot service uninstall --purge` deletes our data dir; the user is told exactly what will be removed and given a `--keep-data` option.

---

## 4. Incident bundle

For sharing a problem (e.g. in a bug report), `mempilot explain --bundle` produces a single JSON file containing:

- the incident window at full resolution,
- config with absolute paths redacted,
- environment: macOS version, arch, page size, total RAM,
- **not**: full process lists beyond the incident groups, no paths, no usernames.

The bundle is generated locally and never uploaded by us. The user chooses to share it.

---

## 5. Out of scope (stated, not implied)

- Defending against same-user malware.
- Tamper-evident logging.
- Encrypting the ledger at rest (the OS's FileVault covers this; adding another layer would complicate crash recovery for little gain).
- Multi-user systems where a user wants their data hidden from an admin — an admin can read anything.

---

## 6. Tests

| Test | Proves |
|---|---|
| SEC-1 | Data dir and files have the expected modes |
| SEC-2 | Socket rejects a connection from another UID |
| SEC-3 | Build gate finds no network dial/listen |
| SEC-4 | Config with a path-traversal name cannot write outside the root |
| SEC-5 | Bundle export contains no absolute home paths or usernames (regex scan over a generated bundle) |
| SEC-6 | Uninstall `--purge` removes exactly the data root and nothing else |
