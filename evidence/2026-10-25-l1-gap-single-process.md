# The gap L1 does not cover yet: a single abandoned process

## Found on the live machine, 23:30

```
pid    ppid   MB     process
11265  50972  1017   rust-analyzer   (running 2 days 15 h, CPU 0.000 s/s)
21432  51011   859   rust-analyzer   (running 2 days 15 h, CPU 0.000 s/s)
37060  64222   821   rust-analyzer   (running  13 h,       CPU 0.000 s/s)
```

2.7 GB of memory held by three processes that have done no work in the
observation window. Their parents are `claude` sessions that may or may not
still be in use — that is exactly the judgement L1 exists to make.

## Why L1 does not report them

Two reasons, both structural rather than bugs:

1. **Attribution groups by terminal/agent ancestry.** `claude` is an anchor in
   `AnchorTable`, so every `rust-analyzer` it spawns is attributed to the
   `claude` group. The daemon sees one group called `claude`, not three
   processes — and a group's footprint is the sum of its members, which hides
   the case where one member is abandoned and the rest are working.

2. **`claude` is an agent, and agents are excluded by rule.** `Assess` returns
   `useful` for `IsAgent` immediately, because closing an agent destroys
   in-flight work. That rule is right for the agent and wrong for its abandoned
   children, which are not agents.

## What the design says

`process-relief-engine.md` §1.1 lists "parent identity gone" and "belongs to a
finished workload" as separate candidate signals, and §1.5 gate 3 says a
coding agent is never L2-eligible *"unless the class is explicitly one the user
authorized"*. Neither says an agent's children inherit its protection.

## What is needed

L1 has to assess **individual processes** as well as groups:

- a process whose CPU has been ~0 for ≥ 10 min, whose parent is alive but idle,
  and which holds ≥ 100 MB, is a `suspicious` candidate even inside a protected
  group;
- the group-level exclusion stays: the agent itself is never a candidate, and
  an `abandoned` verdict on a child still requires the parent-gone check.

This is not implemented. The engine as committed assesses `GroupFacts`, which
carries one footprint per group. The 2.7 GB above would be reported as
"`claude` holds N GB" — which is true, and useless, because the owner cannot
act on it.

## Also unverified

The daemon has written no attribution window since 22:56 (the current process
started 23:24). Everything above is measured from the process tree directly,
not from the daemon's own view — which is the same method that found defects
23–27.
