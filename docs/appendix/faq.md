# FAQ — the questions this product must answer honestly

These are the questions a user (or a reviewer) will ask. The answers are the product's real specification, compressed.

---

### "Will this stop the 'out of application memory' dialog?"

**Sometimes.** It will make the machine reach that state far less often by catching the trend early and telling you what is growing. It cannot guarantee it: if your workload genuinely needs more memory than 16 GB, no user-space daemon can prevent it. What the product guarantees is that you will have known it was coming, with numbers, instead of being surprised.

### "Why not just kill the app that's using the most memory?"

Because in this workload the app using the most memory is usually the one doing your work. Killing it is the outcome you asked us to prevent. Memory Pilot has no ability to kill anything, by design — not a setting, no `--force`.

### "Why not free the cache / run purge?"

Because free RAM is not the goal; avoiding pressure is. macOS already reclaims cache when it needs to. Purging ahead of time trades a benefit the OS would have taken anyway for a cost (caches must be re-read from disk), and on a 256 GB machine that is already swapping, that cost lands on the SSD. We may add a purge-based provider later *if* a measurement shows a net benefit. (ADR-006)

### "Can't you move memory to the SSD and bring it back later?"

Not another app's live memory. Its heap is full of pointers only it can rewrite. Any tool that claims to do this transparently is either doing something unsafe or something narrower than it sounds. What we *can* do is predict early and ask an app to release its own caches if it offers a way. (ADR-011)

### "So what does it actually do?"

1. Measures pressure the way macOS defines it, not as a RAM percentage.
2. Forecasts when it will become dangerous, with a stated confidence.
3. Tells you which workload is growing and by how much, and what it cannot rule out.
4. Shrinks its own footprint when the machine is under pressure.
5. Records everything so you can reconstruct any incident afterwards.

### "Why should I trust the forecast?"

You shouldn't, blindly. Every forecast carries a confidence and a range, and the daemon tracks how often those ranges were right (calibration). If the calibration degrades, the daemon says so and stops acting on it. A forecast without a confidence is a bug, not a feature.

### "What is it doing with my process names?"

Keeping them locally, in a file only you can read, for as long as they are useful for diagnosis. Nothing leaves the machine — the binary contains no network code at all.

### "Does it need root / Full Disk Access?"

No. It runs as a normal user LaunchAgent. Full Disk Access is not needed for memory telemetry and the `doctor` command says so explicitly.

### "How much does it cost me?"

Target: under 60 MB RSS and 0.3 % CPU when idle; it tightens both when the machine is under pressure. The `status` command always shows its own budget usage so you can check.

### "What if it's wrong?"

You get the evidence: `mempilot explain` shows what it saw, what it predicted, what it considered, what it did, and what changed. If it predicted a crisis that did not happen, the ledger records the miss, and the calibration report tells you the false-alarm rate.

### "Will it slow down my machine when I'm already in trouble?"

The opposite is the requirement. Under pressure it samples less, logs less, and does less. If it ever makes things worse, that is a bug of the highest severity.

### "What's the difference from Activity Monitor?"

Activity Monitor shows you a snapshot; that is not the hard part. The hard parts are: deciding whether what you see is dangerous, knowing which workload caused the change, and knowing what not to worry about. Memory Pilot is built for those, and always shows "large but stable" processes separately so you can stop worrying about them.

### "Why Go and not Rust, when the reference tools are Rust?"

Build speed and iteration matter for a project this size, and the difficult parts (what to measure, what a forecast means, what may be done) do not depend on the language. The native macOS calls are isolated behind one interface; if a specific component needs tighter control later, that is a measured decision, not a rewrite. (ADR-000)

### "Why is there so much documentation?"

Because the owner asked for the specification to be the main investment, and because almost every wrong turn in this problem space comes from a sentence that sounded obviously true — "kill the biggest process", "free the cache", "move memory to SSD", "RSS means a leak". Writing the refutations down once is cheaper than rediscovering them in code.
