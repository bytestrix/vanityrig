# VanityRig

**A distributed vanity .onion address search platform — CLI + live dashboard + multi-host orchestration, built on top of (and eventually beyond) `mkp224o`.**

Born out of building the Borderland dark-web chat app: we needed a `border...onion` vanity address, ran `mkp224o` across 3 machines by hand (systemd services, ad-hoc SSH polling, a bash dashboard script), and realized there is no existing open-source tool that does this properly. This document captures everything about the project so far: motivation, competitive landscape, full capability wishlist, technical design, and the faster-engine research that should shape the roadmap.

---

## 1. The problem

`mkp224o` (https://github.com/cathugger/mkp224o) is the standard tool for generating Tor v3 vanity `.onion` addresses. It is fast, minimal, and command-line only. That minimalism is also its limitation:

- No cumulative "keys tried" counter — only instantaneous `calc/sec` snapshots that reset on restart.
- No concept of multiple machines. Running it on 3 hosts to search faster means you build all the orchestration, log aggregation, and progress math yourself (which is literally what we did today, by hand, over an hour, in a live chat).
- No match management. Every hit just gets dumped as a folder (`hostname`, `hs_ed25519_secret_key`, `hs_ed25519_public_key`) with no catalog, no "which of these did I actually deploy," no lifecycle.
- No bridge to actually going live — finding the key and deploying it to Tor's `HiddenServiceDir` are two totally disconnected manual steps.
- Changing the search prefix loses no historical progress mathematically (any prior random trial is still a valid statistical sample toward a new target), but the tool gives you zero visibility into that — you have to reason about it yourself.
- CPU/thread control is a flag at launch time, not a live, adjustable knob.

### What we found already exists (researched, not assumed)

| Project | What it is | Gap |
|---|---|---|
| [cathugger/mkp224o](https://github.com/cathugger/mkp224o) | The reference CLI generator. Fast, single-machine, no live UI. | This is the thing being wrapped. |
| [nthpyrodev/mkp224o-gui-frontend](https://github.com/nthpyrodev/mkp224o-gui-frontend) | Python/CustomTkinter GUI wrapper. Prefix input, thread slider, output folder picker. | No live counter, no match catalog, no multi-host, still requires compiling mkp224o yourself. |
| [StormyCloudInc/Vanity-Generator](https://github.com/StormyCloudInc/Vanity-Generator) | Cross-platform desktop app (Gio UI), CPU **and GPU** (Metal/OpenCL) acceleration, live speed/checked-count/ETA, supports both I2P and Tor v3. | Most polished single-machine tool found. No multi-host aggregation. Not built on mkp224o (own engine). |
| [offset/onion-vanity-address](https://github.com/offset/onion-vanity-address) | CLI, curve-symmetry-optimized search, faster than mkp224o. | CLI only, single machine, no UI/orchestration layer at all. |
| [AlexanderYastrebov/onion-vanity-address](https://github.com/AlexanderYastrebov/onion-vanity-address) | Same technique, Go implementation. Benchmarked at **128M attempts/sec** on an 8-core/16-thread Ryzen 7 (AVX-512). | Same — CLI only, no orchestration/UI. |
| [koceg/v3onion](https://github.com/koceg/v3onion) | Minimal CLI alternative. | No UI, no multi-host. |

**The conclusion that justifies this project**: nobody has built the orchestration + live-dashboard + match-lifecycle layer. That's the actual gap, not the raw search engine (which is a solved, if fragmented, problem).

---

## 2. The full engine landscape (researched, not assumed — every known v3 vanity technique)

VanityRig's entire value proposition beyond "another wrapper" is that it should support **every viable search technique** as a pluggable, benchmarkable engine, and pick the best one automatically per host based on detected hardware (CPU features, GPU presence, OS). Catalogued on 2026-09-08:

| Engine | Approach | Hardware | Language | Status / notes |
|---|---|---|---|---|
| [cathugger/mkp224o](https://github.com/cathugger/mkp224o) | Full per-candidate Ed25519 point arithmetic (both coordinates) | CPU only | C | The reference implementation. Battle-tested, what we used today (~18.5M/s on 16 cores). Baseline to beat. |
| [AlexanderYastrebov/onion-vanity-address](https://github.com/AlexanderYastrebov/onion-vanity-address) (lib: `vanity25519`) | **Curve coordinate symmetry** — computes only the y-coordinate per candidate, batches field inversions via the **Montgomery trick** (one inversion per batch, not per candidate). Amortized cost: 5 field multiplications + 2 additions/candidate. | CPU only, AVX-512 aware | **Go** | Fastest pure-CPU option found: **128.3M attempts/sec** on an 8c/16t Ryzen 7 (AVX-512). Being a Go *library* (not just CLI) is huge for us — importable directly, no subprocess overhead. **Top candidate for VanityRig's native fast-path engine.** |
| [offset/onion-vanity-address](https://github.com/offset/onion-vanity-address) | Same curve-symmetry technique as above | CPU only | — | Independent implementation of the same idea; worth benchmarking against Yastrebov's for whichever is more mature/maintained. |
| [chrisch88dev/onionloom](https://github.com/chrisch88dev/onionloom) ("**Onionloom**") | GPU via **Vulkan/DX12/Metal, auto-detected** (not CUDA-locked like older GPU tools) + a custom **paired-coordinate CPU engine with simultaneous field division** for the CPU fallback path. Every GPU result is **reverified on CPU** so a bad driver can't produce an invalid key. | GPU (cross-platform) + fast CPU fallback | Rust | **Brand new (posted to the Tor Project forum Aug 2026), built specifically because existing GPU forks were CUDA-locked or barely beat CPU.** Cross-platform GPU support is unique among everything found — this is the **top candidate for VanityRig's GPU engine**, and packaged in the AUR (`onionloom-git`), suggesting real adoption already starting. |
| [dr-bonez/tor-v3-vanity](https://github.com/dr-bonez/tor-v3-vanity) | GPU via CUDA specifically, auto-tunes threads/blocks | NVIDIA GPU only | Rust | Explicitly flagged by its own author as "brand new and hasn't been thoroughly vetted." CUDA-only limits it to NVIDIA hardware — Onionloom's cross-platform approach is strictly more useful for us. |
| [StormyCloudInc/Vanity-Generator](https://github.com/StormyCloudInc/Vanity-Generator) | GPU via Metal (macOS) / OpenCL (Windows/Linux) + parallel CPU fallback | GPU + CPU, cross-platform | Go (Gio UI) | Most polished **end-user desktop app** found — real-time speed/checked-count/ETA UI, dark mode, auto-update. Also supports I2P addresses, not just Tor. Good reference for VanityRig's own UI, even though its engine isn't the fastest. |
| [lachesis/scallion](https://github.com/lachesis/scallion) | RSA key-gen on CPU + SHA1 hashing on GPU (OpenCL) | GPU (SHA1) + CPU (RSA) | — | **Historical only — does not support Tor v3 addresses at all** (v2/RSA-era tool). Not usable for this project; listed for completeness. |
| [koceg/v3onion](https://github.com/koceg/v3onion) | Minimal CPU search | CPU only | — | Basic alternative CLI, no notable optimization; low priority. |
| [dotcypress/granex](https://github.com/dotcypress/granex) | Ed25519 CPU search | CPU only | Rust | Marked **[WIP]** by its own author, only 153 SLoC. Immature; low priority. |
| [rdkr/oniongen-go](https://github.com/rdkr/oniongen-go) | Ed25519 CPU search | CPU only | Go | Lightweight Go alternative — same language as our planned architecture, could be a useful reference implementation even if not adopted directly. |

### Performance headline
Our own `mkp224o` run today: **~18.5M attempts/sec on 16 cores**. The curve-symmetry CPU engines report **~128M attempts/sec on 8 cores/16 threads** (AVX-512) — roughly a **5-7x per-core speedup**. GPU engines (Onionloom, StormyCloud, dr-bonez) go further still, limited mainly by GPU hardware availability rather than algorithmic ceiling.

**Implication for VanityRig**: don't hardcode `mkp224o`. Build a **pluggable engine interface** from day one:
1. `mkp224oEngine` — subprocess wrapper (fallback/compat, works everywhere, zero extra dependencies) — **v0 default**, since it's what's proven and already deployed
2. `symmetryEngine` — native Go, importing `vanity25519`-style curve-symmetry logic directly (no subprocess) — **fast CPU path**, likely v1
3. `onionloomEngine` — subprocess wrapper around Onionloom for GPU-equipped hosts (cross-platform via Vulkan/DX12/Metal, with its CPU-reverify safety net already built in) — **fast GPU path**, evaluate for v1/v2
4. Engine auto-selection per host: detect AVX-512 → prefer symmetry engine; detect a usable GPU → prefer Onionloom; otherwise fall back to `mkp224o`

---

## 3. What we manually proved works today (the MVP feature set, already battle-tested)

Everything below was built ad-hoc as bash + systemd during the Borderland vanity search and should become native VanityRig features:

1. **Systemd-managed background search** per host, auto-restart on crash (`Restart=always`), low-priority scheduling (`Nice=19`, `CPUWeight=1`, `IOWeight=1`) so it doesn't interfere with interactive work.
2. **Cumulative keys-tried counter**, computed by integrating `calc/sec × Δelapsed` over the log, correctly handling process-restart resets (elapsed dropping back near 0 starts a new segment).
3. **Statistical progress-to-target percentage**, computed against `32^(prefix length)` per active filter, clearly labeled with what the percentage is *of* (this took real iteration to get right — early versions were "unclear," per direct user feedback).
4. **Multi-host aggregation**: SSH-polled dashboard pulling live stats + logs + match folders from N hosts (local + `bytestrix` + `bytestrix-mini` in our case) into one combined view, refreshed every few seconds.
5. **Current-filter vs. stale-filter match separation**: when the prefix changes mid-search, old matches from a previous filter remain on disk (they're free byproducts) but must be clearly separated from matches satisfying the *current* target — conflating them caused real user confusion in this session.
6. **Live health verification**, not just trust: cross-checking `ps` CPU-time deltas, `pcpu%`, disk space, and log succ/sec spikes against the actual match-folder count to distinguish "genuinely stalled/broken" from "just unlucky variance" (Poisson math on expected-vs-actual match count).
7. **Zero-coordination distributed search correctness**: proof that running fully independent random searches on N machines needs no partitioning — birthday-collision probability between machines is astronomically negligible (~10⁻⁵⁶ for the trial counts involved), so naive parallel independence is already optimal.

---

## 4. Full capability wishlist (brainstormed for genuine usefulness, not just parity)

### Core search
- [ ] Pluggable engine backend: `mkp224o` (default/compat) and curve-symmetry engine (fast path), selectable per host based on detected CPU features (AVX-512 etc.)
- [ ] GPU backend (OpenCL/Metal) as a third engine option for supported hardware
- [ ] **Match position modes: prefix / suffix / anywhere** — see §4b, a genuine differentiator since `mkp224o` is prefix-only
- [ ] Multi-filter support with **live per-filter breakdown** (not just aggregate `succ/sec`) — mkp224o doesn't expose which filter matched without inspecting the resulting address; VanityRig should track this natively
- [ ] Filter validation before launch (reject invalid base32 chars, warn on filters long enough to be practically infeasible, given current hardware — i.e. surface the "13.75 hours" / "620 years" style estimate *before* the user commits, not after)
- [ ] Passphrase-based deterministic key search (mkp224o `-p`/`-P`/`--skipnear`/`--warnnear` equivalent) for regenerable/memorable keys

### Orchestration (the actual gap in the ecosystem)
- [ ] Host registry: add/remove machines (local + SSH remotes), auto-detect core count and CPU features, auto-deploy the chosen engine binary + service unit
- [ ] Central live dashboard aggregating all registered hosts — combined keys/sec, combined cumulative trials, combined progress %, per-host breakdown, one clear "MATCH FOUND" surface
- [ ] Resource controls per host: thread/core count, nice/ionice priority, pause/resume without losing search state, scheduled run windows (e.g. only overnight)
- [ ] Auto-stop conditions: stop after N matches found (exactly the "keep collecting until 5" workflow from this session), stop after a time budget, stop after a probability threshold reached

### Match lifecycle (currently nonexistent anywhere)
- [ ] Catalog every match found: address, host, timestamp, matched filter, key file locations
- [ ] Mark one match as "selected"/"in use" to prevent accidental redeployment of an abandoned key
- [ ] Secure handling: never print secret keys to terminal/logs, enforce `0600` permissions, optional passphrase-encrypted at-rest storage for `hs_ed25519_secret_key` (this is literally the permanent identity of the site — losing or leaking it is catastrophic and irreversible)
- [ ] One-click / one-command **deploy to Tor**: copy the chosen key pair into the correct `HiddenServiceDir`, patch `torrc` if needed, restart Tor, verify the service comes up — bridging vanity search directly to going live (no existing tool does this)

### Visibility & trust (things that mattered a lot in this session)
- [ ] Explicit, labeled statistics — every number shown should say what it's a percentage *of* and over what time window (direct lesson from user confusion mid-session)
- [ ] Built-in health verification (the CPU-time-delta / disk-space / succ-vs-match-count cross-checks done manually today) surfaced automatically when a search goes quiet longer than statistically expected, with a plain-language "this is unlucky, not broken" or "this looks actually stuck" verdict
- [ ] Notifications on match found: desktop notification, webhook (Slack/Discord/generic), email — so users don't have to babysit a terminal

### Feasibility Advisor (promoted to a core feature — see §4a below, not just a nice-to-have)

### Packaging & distribution
- [ ] Single static binary (Go is the natural choice: easy cross-compilation, good concurrency for orchestration, can import `vanity25519`-style libraries directly instead of shelling out, easy to scp to remote hosts exactly like `mkp224o` itself)
- [ ] CLI-first (`vanityrig init`, `vanityrig host add <ssh-alias>`, `vanityrig search start --prefix border --hosts all`, `vanityrig status`, `vanityrig matches list`, `vanityrig deploy <address>`, `vanityrig stop`)
- [ ] Optional TUI (terminal dashboard, e.g. bubbletea-style) as the "watch it live" experience — a proper version of the bash `watch` dashboard hacked together today
- [ ] Optional web GUI later, for non-technical users, mirroring StormyCloud's desktop-app approach but adding the multi-host layer it lacks

---

## 4a. The Feasibility Advisor (core feature — the gap we personally hit)

**Origin story**: during the Borderland search, we picked `borderland` (10 chars) as a "bonus" filter almost casually, with no estimate of what it would cost. Then, mid-session, the assistant computed the search space **wrong by a factor of 1,024** — using 2^60 (which is `32^12`) instead of the correct `32^10 = 2^50 ≈ 1.126 quadrillion` — and on the strength of that bad number declared the target permanently infeasible, talked the user out of a perfectly sound plan to rent AWS compute for a day, and shipped the wrong constant into the live dashboard, where it under-reported real progress by 1000x for hours.

The correct numbers: `borderland` is **~1.1 years median on 3 modest machines**, **~52 days on one large AWS instance**, and **~62% likely to be found by ~50 large instances in a single day** (roughly $5-12k on Spot) — i.e. an ordinary budget decision, not an impossibility.

**Two lessons, both of which are requirements for this feature:**
1. Nobody should have to do this arithmetic by hand before committing compute — the tool must do it, up front. That was the original motivation.
2. **The tool must show its work.** A feasibility verdict that says only "infeasible, don't bother" is worse than useless when the underlying arithmetic is wrong — it's confidently wrong in a way the user cannot check. Every estimate the Advisor prints must show the exponent form (`32^10 = 2^50`), the raw count, the assumed rate, and the derived time, so a wrong constant is visible on its face rather than hidden behind a conclusion. (One genuine fact survived the error: unlike Bitcoin mining, **no ASICs exist for Ed25519 vanity search** — there's no economic incentive to build them — so scaling is bounded by general-purpose CPU/GPU economics. That bounds cost; it does not make mid-length prefixes impossible.)

**This must be a blocking, first-run experience in VanityRig — not a warning buried in a log.**

### What it does

1. **Before any search starts**, compute the exact expected trial count (`32^len(prefix)`) and, using the registered hosts' known/benchmarked throughput, the **expected wall-clock time** — not just a mean, but percentiles (the process is exponential/Poisson, so P50/P90/P99 tell a much more honest story than an average alone, exactly as discussed in this session).
2. **Classify the request** against configurable thresholds (defaults suggested below) and respond accordingly:
   - **Trivial** (expected < 1 minute): just run it.
   - **Reasonable** (expected minutes to hours): show the estimate, run it.
   - **Expensive** (expected hours to weeks): show the estimate clearly, ask for explicit confirmation before starting.
   - **Infeasible** (expected time exceeds a sane human horizon even with rentable compute): **do not start silently**. Show the real numbers and what hardware *would* close the gap, so the user sees the shape of the wall rather than just "no". Note the ASIC point where relevant — *"unlike cryptocurrency mining, no specialized hardware exists for this workload"* — as a statement about **cost ceilings**, not impossibility.
3. **Always price the cloud option, don't dismiss it.** For anything in the "expensive" band, compute what a burst of rented compute actually buys: instance type, count, hours, estimated spend, and resulting probability of success. `borderland` is the reference case — ~62% success for one day of ~50 large instances is a budget decision a user is entitled to make, and the tool nearly talked its user out of it by getting the arithmetic wrong.
4. **Always offer real alternatives, computed, not just suggested in the abstract**:
   - The largest prefix of the requested word that *is* feasible within a chosen time budget
   - Vowel-dropped / shortened variants that stay recognizable but shrink the space (e.g. `borderland` → `bdrland` / `bordrlnd`), each with its own real estimate
5. **Never silently downgrade or refuse** — the user can always override and run anyway (it's their compute), having seen the real numbers first. Informed consent, not gatekeeping.

### Present options, recommend one, decide nothing

Speed is not the only axis a user cares about, and treating it as the only one is a design error the first implementation made twice: it labelled the list "**Cheaper** alternatives" (wrong — tail completion costs exactly as much as a prefix, it buys *placement*) and it omitted the requested search from the list entirely, so the output read as a correction rather than a comparison.

The rule: **lay out the choices, mark a recommendation with a one-line reason, and get out of the way.**

- The search the user asked for appears **in the table**, labelled as theirs, even when impossible. They are comparing, not being overruled.
- The axes shown are the ones people actually weigh: **where the word lands**, **how long it takes**, and **whether the whole word survives**. Cost alone would push everyone toward truncation.
- Ranking prefers **keeping the full word** over raw speed. A vanity address exists to be recognisable; an option that shortens the word is a worse outcome even when it finishes sooner.
- A recommendation is offered only when something genuinely stands out (keeps the word, at least 3× faster). Otherwise none is shown rather than one being manufactured.
- Every recommendation closes with the same idea: *any option above is yours to run.*

Worked example — the user asks for `borderland` as a suffix, which is impossible:

```
  Your options — pick whichever suits you:

    search for               word appears       typical time    word
    ────────────────────────────────────────────────────────────────────
    borderland               end                impossible      full  (what you asked for)
    borderland{ad,id,qd,yd}  end                1.1 years       full
  → borderland               anywhere           9.0 days        full  ← recommended

    Recommended: borderland (anywhere) — the search you asked for is
    impossible, and this keeps your whole word.
    That is only a suggestion; any option above is yours to run.
```

Someone who wants the word at the *end* badly enough to spend 1.1 years can see that option and take it. That is a legitimate preference, not a mistake to be corrected.

### Where the tool may and may not stand in the user's way

The first implementation violated rule 5 immediately: `estimate` refused to print a report at all for an unsatisfiable pattern. Refusing to *inform* is indefensible for an informational command, and it conflated two very different situations. The rule that replaced it:

| Situation | Example | Behaviour | Exit |
|---|---|---|---|
| **Malformed** — cannot be parsed | `border1`, 57 characters | Reject. There is no estimate to give for a typo. | 2 |
| **Unsatisfiable** — well-formed, zero matches exist | `border` as a suffix | **Report in full**, state that the matching set is empty, explain which protocol rule it breaks, and offer a workable alternative. | 1 |
| **Astronomically unlikely** — matches exist, odds are tiny | a full 56-character pattern (~10⁶⁷ years) | **Allow without friction.** Print the numbers and get out of the way. | 0 |

The middle and bottom rows are routinely conflated and must not be. "Astronomically unlikely" is beaten by more hardware; "unsatisfiable" is not beaten by anything, because the set of matching addresses is empty rather than merely small. Only the first row is the user's mistake; the other two are findings.

When `search start` lands it follows the same principle: an unsatisfiable pattern warns and requires an explicit `--force`, rather than being blocked outright. A user with unusual hardware and unusual motives is entitled to run a hopeless search having been told exactly how hopeless it is.

### Worked example (corrected numbers — this is the reference test case)

Note the format: exponent form, raw count, assumed rate, and derived time are all shown. That is deliberate — it is what makes a wrong constant catchable by the reader.

```
$ vanityrig search start --prefix borderland

⚠ Feasibility check for "borderland" (10 characters)

  Search space:   32^10 = 2^50 = 1,125,899,906,842,624 possibilities
  Your hardware:  3 hosts, ~22.2M keys/sec combined  (measured)
  Progress so far: 408.7B tried = 0.0363% of expected need

  Expected time on your current hardware:
    P50 (coin flip):     ~1.1 years
    mean:                ~1.6 years
    P90 (90% by then):   ~3.7 years

  Rented compute options:
    1x c7a.metal-48xl (192 cores, ~250M/s)   ~52 days      ~$12,500
    50x same, for 24 hours                    61.7% chance  ~$11,600
                                              (Spot: ~40% of that)

  Cheaper alternatives at your current hardware:
    borderx    (7 chars)  32^7 = 2^35   ~26 min    [found this session]
    borderla   (8 chars)  32^8 = 2^40   ~14 hours
    bordrlnd   (8 chars)  32^8 = 2^40   ~14 hours  [vowel-dropped]

  Start the search on your 3 registered hosts? [Y/n]
```

### Accuracy requirements (non-negotiable — these are what make the verdict trustworthy)

An advisor that is confidently wrong is worse than no advisor, because the user cannot check it and will act on it. The 1024x error described above shipped into a live dashboard and changed a real decision. Accuracy is therefore a set of enforced properties, not an intention:

1. **Never hardcode a search space. Ever.** The error was a typed-out constant (`1152921504606846976`) that nobody could eyeball as wrong. Always compute `space = 32**len(pattern)` at runtime from the pattern itself.
2. **Work in bits internally, not decimal.** Difficulty is `5 × len(pattern)` bits — `borderland` is 50 bits, `borderx` is 35 bits. A 50 vs. 60 slip is glaring; a `1.126e15` vs. `1.153e18` slip is not. Convert to decimal only for display.
3. **Assert invariants on every estimate** — cheap, and each one independently catches the exact bug that occurred:
   - `space(L) == 2**(5*L)` (would have caught it instantly)
   - `space(L+1) == 32 * space(L)` — monotonic, exact ratio
   - `anywhere_candidates(L) < prefix_candidates(L)` for any L < 56
   - a suffix pattern is only accepted if it ends in `d` (see §4b)
4. **Ship a known-good test table** and run it in CI: `32^6 = 2^30`, `32^7 = 2^35`, `32^8 = 2^40`, `32^10 = 2^50`. Three of these were right in this session and one was wrong — a table test makes that impossible to miss.
5. **Use the mode-specific measured rate.** Prefix, suffix and anywhere have materially different per-candidate costs (§4b). Estimating an `anywhere` search with a prefix-derived rate is a second, independent way to be confidently wrong.
6. **Continuously self-check against ground truth.** The tool knows how many keys it has actually tried and how many matches it has actually found. If observed match rate diverges from predicted by more than a plausible margin over a long enough window, say so — either the model is wrong or the search is broken, and both are worth surfacing rather than silently believing the model.
7. **Show the work** (restated from above, because it is the last line of defence): print exponent form, raw count, assumed rate and derived time together, so a wrong number is visible to the user even when every internal check has failed.

### Design notes
- Percentile math: for an exponential process with rate λ = combined_keys_per_sec / target_space, time-to-P-percent = -ln(1-P)/λ. Cheap to compute, should be a small shared utility used everywhere time estimates appear (pre-search advisor, live dashboard, mid-search re-estimates).
- Hardware throughput should be **measured**, not assumed — a quick calibration run (a few seconds) per registered host on first setup, refreshed periodically, rather than trusting a static "vCPU count × guessed rate" table.
- The "vowel-dropped variant" generator is a nice, cheap, genuinely useful bit of UX — a small deterministic function that proposes shortened spellings of a target word and reports feasibility for each, so the user gets real options instead of just a rejection.
- This feature alone would have saved real time and (had we gone the AWS route) real money this session — it's a strong candidate for the very first thing VanityRig ships, even before multi-host orchestration.

---

## 4b. Match position modes: prefix / suffix / anywhere

`mkp224o` matches **prefixes only** (confirmed from its full option list — there is no position flag). Every other tool surveyed in §2 is the same. Supporting suffix and anywhere-in-the-address matching is therefore a real differentiator, and — more importantly — **"anywhere" is dramatically cheaper than a prefix**, which most users would never guess.

### The math (10-char pattern, 56-char address)

A v3 address is exactly 56 base32 characters, so a 10-character pattern has **47 possible starting positions** (`56 - 10 + 1`). Accepting a hit at any of them multiplies your per-candidate probability by ~47:

| Mode | Expected candidates | Time at 22.2M/s (3 hosts) |
|---|---|---|
| Prefix (position 0) | 1.126 × 10¹⁵ | ~1.6 years |
| Anywhere (47 positions) | 2.40 × 10¹³ | **~12–125 days** (see cost caveat) |

The spread reflects per-candidate cost, not probability — even at a pessimistic 10× slowdown, "anywhere" beats prefix by roughly 5×; at realistic implementation efficiency it's closer to 15-45×.

### Why suffix/anywhere cost more per candidate (and why mkp224o skipped them)

Prefix matching is uniquely cheap: the leading base32 characters map directly onto the leading raw bytes of the public key, so an engine can compare bytes without base32-encoding **and without computing the SHA3-256 checksum at all** — `mkp224o` defers the checksum until after a prefix already hit. The other modes lose both optimizations:

- **Suffix** — the trailing characters *are* the checksum bytes, so SHA3-256 must be computed for every candidate. This is precisely the work the prefix fast-path exists to avoid.
- **Anywhere** — base32 packs 5 bits per character, so only every 8th character offset (40 bits = 5 bytes) is byte-aligned. At the other offsets there is no byte-comparison shortcut; the engine must encode and substring-scan.

Budget roughly 3-10× slower per candidate for these modes. Worth measuring per engine rather than assuming, and worth surfacing in the Feasibility Advisor's estimate (the advisor must use the *mode-specific* measured rate, not the prefix rate, or it will badly under-estimate).

### The suffix constraints every implementation must validate

The version byte (`0x03`) is fixed, and 280 bits divides evenly into 56 characters with no padding, so that byte lands wholly inside the final two characters and pins **both** of them. Verified analytically and empirically against 200,000 generated addresses:

- **Character 55 (last) is always `d`** — the low 5 bits of `0x03` are `00011` = 3 = `d`.
- **Character 54 is always one of `a`, `i`, `q`, `y`**, uniformly 25% each — it carries 2 free checksum bits followed by the three high bits of `0x03`, which are zero.
- Character 53 and earlier are unconstrained (all 32 values observed).

> **Correction.** An earlier draft of this section claimed "`borderland` qualifies as a suffix because it ends in `d`". That is **wrong**: `borderland` has `n` in the penultimate position, and `n` is not in `{a,i,q,y}`, so it can never be a suffix. The mistake was caught only when the validator was implemented and tested — reasoning carefully about the rule was not sufficient to get it right. That makes two claims in this document to survive review and then die on contact with a test, which is the strongest available argument for §4a's accuracy requirements.

**Difficulty of a valid suffix** is cheaper than the equivalent prefix:

```
suffix_bits(L) = 5*(L-2) + 2     for L >= 2
suffix_bits(1) = 0               the bare pattern "d" always matches
```

That is an 8-bit (256×) discount versus a prefix of the same length: the last character is free, and the penultimate costs 2 bits instead of 5.

Consequences the tool **must** enforce:
- A suffix not ending in `d` is **impossible**, not merely slow — `border` can never be a suffix. Reject at validation time with the explanation, rather than letting the user search forever for something that cannot exist.
- A suffix whose penultimate character is outside `{a,i,q,y}` is equally impossible — this is the rule that catches `borderland`.
- Offer the repair when rejecting: `anywhere` mode has no such constraint and is cheaper anyway.
- Keep **impossible** and **infeasible** strictly distinct. Only a protocol violation is impossible; a 20-character prefix is merely astronomical.

### Tail completion: rescuing a rejected suffix

A word that cannot end an address can still sit **immediately before** the two characters the protocol forces there. `borderland` can never finish an address, but `...borderlandid` can — and reads almost identically to a human.

When a suffix is rejected for the tail rule, the advisor offers this automatically, and it must offer **all four completions as a single OR search**:

```
borderlandad   borderlandid   borderlandqd   borderlandyd
```

Any one of them gives the user the same outcome, so searching all four together is **exactly 4× faster** than committing to one — an easy and expensive mistake to make, since picking a single ending looks equivalent. Measured costs at 22.2M keys/sec:

| Approach | Bits | P50 |
|---|---|---|
| One ending (`…borderlandad`) | 52 | 4.5 years |
| **All four together** | **50** | **1.1 years** |
| Prefix `borderland…` for comparison | 50 | 1.1 years |

The equality with prefix is not a coincidence: both pin the same ten characters at unconstrained positions, so both cost 50 bits. Tail completion buys placement, not discount — the discount comes from `anywhere` (45 bits, ~9 days), which remains the cheapest way to get a long word into an address.

### Implementation hazard: probability underflow

Combining per-position or per-pattern chances as `1 - Π(1-p)` **silently collapses to a hard zero** once `p` falls below float64 epsilon (~2.2e-16), which happens at 11+ characters. The tool then reports a merely-slow search as *impossible* — a categorically stronger and completely wrong claim, and precisely the distinction the previous bullet demands. This bug occurred twice during implementation (anywhere-mode, then multi-pattern) before being centralised into a single `unionProbability` helper accumulating via `log1p`/`expm1`. Do not re-inline it.

### UX note

Position mode is a branding decision as much as a technical one, and the tool should say so plainly: a prefix is what people actually read, remember and type-check; a word buried at character 31 is nearly invisible in practice. Recommend prefix for a public-facing identity, and offer `anywhere` as the pragmatic option when the user wants a longer/more meaningful word and cares more about the word being present than being first.

### CLI shape

```
vanityrig search start --pattern borderland --match prefix    # default
vanityrig search start --pattern borderland --match suffix    # validates trailing 'd'
vanityrig search start --pattern borderland --match anywhere  # ~47x cheaper here
```

---

## 5. Suggested architecture (starting point, not final)

- **Language**: Go — single static binaries, strong concurrency primitives for polling N hosts and multiple local worker threads, can vendor/import Ed25519 curve-symmetry logic directly (avoids reimplementing crypto from scratch; `AlexanderYastrebov/vanity25519` is a Go *library*, not just a CLI, which is exactly what's needed here).
- **Engine abstraction**: a small interface (`Engine.Search(filters []string, threads int) <-chan Match`), with auto-selection per host based on detected hardware, implemented by (see full catalog in §2):
  - `mkp224oEngine` — shells out to the existing binary, parses its log format (already reverse-engineered this session: `>calc/sec:%f, succ/sec:%f, rest/sec:%f, elapsed:%f`) — always-available fallback
  - `symmetryEngine` — native Go, importing `vanity25519`-style curve-symmetry logic directly (fastest CPU path, no subprocess overhead, ~5-7x mkp224o per-core)
  - `onionloomEngine` — shells out to Onionloom for GPU-equipped hosts (cross-platform Vulkan/DX12/Metal, CPU-reverified results) — fastest overall when a GPU is present
  - `gpuFallbackEngine` — StormyCloud's OpenCL/Metal approach as a secondary GPU option if Onionloom is unavailable/unsupported on a given host
- **Orchestration layer**: SSH-based remote execution (matches what we already have working — no need for a custom agent daemon initially; that can come later if SSH overhead becomes a bottleneck)
- **State/storage**: local SQLite — full schema in §5b
- **Dashboard**: start as a terminal UI (fast to build, matches the actual workflow used today), web UI is a v2 concern

---

## 5a. Why "generate directly from the desired text" is not on the roadmap (settled, not open)

This came up naturally while building this doc: if brute-force search is the bottleneck, why not construct a keypair that produces the desired address directly, instead of generating-and-checking millions of random ones? Worth answering definitively here so it never gets re-investigated as a "maybe someday" optimization.

**It's impossible in a mathematically strict sense, not just impractical.** A v3 address is `base32(public_key || checksum || version)`, and the public key is derived as `private_key × G` (a fixed base point on Curve25519), via a one-way group operation. Going backward — "here's the public key I want, what private key produces it?" — is the **discrete logarithm problem**, which is the actual hardness assumption Ed25519's entire security model rests on. If there were a way to do this, it would not be a vanity-address shortcut; it would be a practical break of Curve25519 itself, with implications far beyond Tor (TLS, SSH, Signal, and most cryptocurrency wallets rest on the same hardness assumption).

This also answers the "prefix vs. postfix" question the same way: the whole 56-character address is one deterministic encoding of a single public key, so there's no way to fix one end and let the rest "fall out" independently — both ends come from the same one-way computation.

**Practical implication for VanityRig**: there is no smarter algorithm to discover here. The entire achievable optimization space is "make generate-and-check faster per attempt" (curve-symmetry math, GPU parallelism, batching) — which is exactly what §2's engine catalog is about — combined with the Feasibility Advisor (§4a) making sure users understand what's actually achievable *before* spending compute on an infeasible target. Both of those are real, buildable improvements; "skip the search" is not.

---

---

## 5b. Data model

Nothing exists yet — VanityRig is greenfield, so **every table below is to be created**. SQLite, single file, no server. The schema is driven by concrete limitations we hit using raw `mkp224o` this session (noted per table).

```sql
-- Registered machines available to run searches.
CREATE TABLE hosts (
  id            INTEGER PRIMARY KEY,
  name          TEXT UNIQUE NOT NULL,      -- 'bytestrix-mini'
  ssh_alias     TEXT,                      -- NULL = local machine
  cores         INTEGER,
  cpu_features  TEXT,                      -- 'avx512,aes' — drives engine auto-select
  has_gpu       INTEGER DEFAULT 0,
  gpu_api       TEXT,                      -- 'vulkan' | 'metal' | 'cuda' | NULL
  instance_hint TEXT,                      -- 't3.micro' — flags burstable/throttled hosts
  status        TEXT,                      -- 'ready' | 'unreachable' | 'disabled'
  added_at      INTEGER, last_seen INTEGER
);
-- Why: bytestrix-mini ran 7x slower than its CPU model suggested because it was a
-- throttled t3.micro. Core count alone is not predictive; record what we learn.

-- Measured throughput. Never assumed, never derived from core count.
CREATE TABLE engine_benchmarks (
  id         INTEGER PRIMARY KEY,
  host_id    INTEGER REFERENCES hosts(id),
  engine     TEXT,                         -- 'mkp224o' | 'symmetry' | 'onionloom'
  match_mode TEXT,                         -- 'prefix' | 'suffix' | 'anywhere'
  keys_per_sec REAL,
  measured_at  INTEGER,
  UNIQUE(host_id, engine, match_mode)
);
-- Why: §4b — the three modes have materially different per-candidate costs.
-- Estimating an 'anywhere' search with a prefix-derived rate is accuracy req #5's
-- failure mode.

-- One search campaign.
CREATE TABLE searches (
  id            INTEGER PRIMARY KEY,
  patterns      TEXT NOT NULL,             -- JSON array; multi-filter is OR
  match_mode    TEXT NOT NULL,
  space_bits    INTEGER NOT NULL,          -- 5 * len(pattern), COMPUTED not typed
  stop_condition TEXT,                     -- 'manual' | 'after_n:5' | 'after:24h'
  status        TEXT,                      -- 'running' | 'stopped' | 'completed'
  created_at INTEGER, started_at INTEGER, stopped_at INTEGER
);
-- Why: space_bits stored in BITS per accuracy req #2 — a wrong 50-vs-60 is visible
-- in the row; a wrong 1.15e18 is not. This column is the audit trail for §4a's error.

CREATE TABLE search_hosts (
  search_id INTEGER REFERENCES searches(id),
  host_id   INTEGER REFERENCES hosts(id),
  threads   INTEGER, nice INTEGER DEFAULT 19,
  engine    TEXT, status TEXT,
  PRIMARY KEY (search_id, host_id)
);

-- Durable throughput samples — the fix for mkp224o's biggest gap.
CREATE TABLE progress_samples (
  id INTEGER PRIMARY KEY,
  search_id INTEGER REFERENCES searches(id),
  host_id   INTEGER REFERENCES hosts(id),
  ts INTEGER, keys_per_sec REAL,
  cumulative_keys INTEGER                   -- running total, survives restarts
);
-- Why: mkp224o only reports instantaneous calc/sec and resets on every restart. We
-- reconstructed cumulative totals by integrating rate over log timestamps and
-- handling reset boundaries by hand. Persist it properly instead.

-- The match catalog. Nothing like this exists in any surveyed tool.
CREATE TABLE matches (
  id INTEGER PRIMARY KEY,
  search_id INTEGER REFERENCES searches(id),
  host_id   INTEGER REFERENCES hosts(id),
  address   TEXT UNIQUE NOT NULL,
  matched_pattern TEXT, match_mode TEXT, match_position INTEGER,
  found_at INTEGER,
  keys_tried_at_find INTEGER,               -- feeds the self-check in accuracy req #6
  key_path  TEXT,                           -- local catalog path, perms 0700/0600
  secret_encrypted INTEGER DEFAULT 0,
  status TEXT                               -- 'new'|'selected'|'deployed'|'archived'
);
-- Why: we accumulated 17 key folders across 3 machines with no record of which came
-- from which filter, which was chosen, or which were safe to delete. 'status' is what
-- prevents deleting a live identity or redeploying an abandoned one.

CREATE TABLE deployments (
  id INTEGER PRIMARY KEY,
  match_id INTEGER REFERENCES matches(id),
  target_host_id INTEGER REFERENCES hosts(id),
  hidden_service_dir TEXT,
  torrc_backup_path  TEXT,
  previous_address   TEXT,                  -- what this replaced, for rollback
  deployed_at INTEGER, verified_at INTEGER,
  status TEXT                               -- 'deployed'|'verified'|'rolled_back'
);
-- Why: deploying borderx6srnn... replaced a live address. We backed up by hand,
-- verified by hand, and deleted the backup on request. Record it.

-- Audit log of every feasibility verdict shown to the user.
CREATE TABLE estimates (
  id INTEGER PRIMARY KEY,
  pattern TEXT, match_mode TEXT, space_bits INTEGER,
  assumed_rate REAL, predicted_p50_sec REAL,
  verdict TEXT, user_choice TEXT, shown_at INTEGER
);
-- Why: accuracy req #6. Predictions become checkable against matches.found_at, so a
-- systematically wrong model is detectable instead of permanently believed.
```

---

## 5c. Process / worker model

Concurrency plan for the Go orchestrator. "Worker" = a long-lived goroutine on the controller unless stated otherwise.

**Controller-side, long-lived:**

| Worker | Count | Responsibility |
|---|---|---|
| **Coordinator** | 1 | Owns search lifecycle; evaluates stop conditions; spawns and reaps everything below |
| **Host worker** | 1 per host | Owns that host's connection and engine process — start, stop, apply threads/nice, restart on crash |
| **Stat parser** | 1 per host | Reads engine stdout, normalizes vendor-specific formats into one sample type |
| **Match watcher** | 1 per host | Watches the output dir; on a new key, hands it to Match ingest *before* anything is printed |
| **Progress aggregator** | 1 | Sole consumer of all samples; maintains combined cumulative counter, writes `progress_samples`, recomputes live percentiles |
| **Match ingest** | 1 | **Sole writer of key material.** Copies to catalog, sets 0700/0600, optional encryption, inserts `matches`. Single owner = no races on secret keys |
| **Health monitor** | 1 | Periodic sanity checks: CPU time advancing, disk space, observed-vs-predicted match rate. Emits "unlucky, not broken" vs. "actually stuck" |
| **Notifier** | 1 | Consumes match/health events → desktop notification, webhook, email |
| **TUI renderer** | 1 | Read-only over aggregator state. Never touches the DB or key files |

**Remote-side:** the **engine process** itself (`mkp224o` / `symmetry` / `onionloom`), one per host, under systemd with `Nice=19`, `CPUWeight=1`, `IOWeight=1`, `Restart=always`.

**One-shot workers:**

| Worker | Trigger | Responsibility |
|---|---|---|
| **Calibrator** | Host registration, then periodically | Short benchmark per engine per mode → `engine_benchmarks` |
| **Feasibility advisor** | Before a search starts | §4a — computes, validates invariants, prints, logs to `estimates` |
| **Deployer** | `vanityrig deploy <address>` | Backup → install key → restart Tor → verify → record in `deployments`; rollback on failure |

**Design rules that fall out of this:**
- Match ingest is the only writer of secret key material — nothing else touches those files.
- Match watcher persists the key *before* any notification or log line, so a crash mid-announcement can never lose a found key.
- TUI is strictly read-only, so the dashboard can never corrupt a running search (the bash version this session shared a directory with live search output, and briefly reported its own script file as a match).
- Host workers are independent and share no state; a dead or unreachable host degrades throughput and nothing else.

---

## 6. Open questions / decisions still needed

- Rewrite the search core natively in Go (using the curve-symmetry math) vs. wrap `mkp224o` as a subprocess for v0 and swap engines later? (Recommendation: wrap first, ship something real, swap the engine once the orchestration layer is solid — don't block on a crypto reimplementation.)
- How much of the "deploy to Tor" step should be automated vs. just clearly documented, given how security-sensitive touching `torrc` and hidden service directories is on a live server.
- Distribution model: personal tool, or something released publicly given the genuine gap found in the ecosystem?

---

*Document created 2026-09-08 during the Borderland `.onion` vanity address search. Source conversation covered: mkp224o field semantics, multi-host distributed search correctness (birthday-collision math), statistical progress tracking, and this competitive research.*
