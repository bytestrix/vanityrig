# VanityRig

VanityRig is a command-line tool that finds custom Tor `.onion` addresses —
e.g. an address starting with `myproject...` instead of a random string —
and tells you honestly how long that will take **before** you start.

It's built on top of [`mkp224o`](https://github.com/cathugger/mkp224o), the
standard tool for this, and fixes the things that tool doesn't do: an
up-front, accurate cost/time estimate, a live progress dashboard, and support
for matching a word anywhere in the address (not just at the start), which is
often dramatically faster than people expect.

See [PROJECT.md](PROJECT.md) for the full design writeup, the competitive
research behind this project, and the roadmap.

## Status

**Working today:**
- `estimate` — tells you if a pattern is realistic, and what it will cost, before you spend any compute on it
- `search` — runs the search with a live terminal dashboard, resumable, with prefix/suffix/anywhere matching

**Not built yet** (designed in [PROJECT.md](PROJECT.md), contributions welcome):
- Multi-host orchestration (running a search across several machines from one place)
- A match catalog / database of everything you've found
- One-command deploy of a found key to a live Tor hidden service
- GPU-accelerated search engines

This is a genuinely useful single-machine tool right now. The multi-host
orchestration layer — the bigger idea in PROJECT.md — is still on the roadmap.

## Requirements

- Go 1.21+ to build
- Optional but recommended: [`mkp224o`](https://github.com/cathugger/mkp224o)
  on your `PATH` for prefix searches — it's roughly 180x faster than
  VanityRig's built-in engine for that mode. Suffix and anywhere searches
  always use VanityRig's own engine, since mkp224o can't do those at all.

## Build

```sh
go build -o bin/vanityrig ./cmd/vanityrig
go test ./...
```

Two dependencies (bubbletea and lipgloss, for the dashboard). Everything else —
including the built-in search engine and all the address maths — is standard library.

## Usage

```sh
vanityrig estimate <pattern>   # how long would this take?
vanityrig search   <pattern>   # go and find it
```

### search

Runs the search and shows a live dashboard. One command — there is no daemon to
start or session to attach to.

```
VanityRig  searching for a .onion address
────────────────────────────────────────────────────────────────────────────────
  looking for   borderzz  (at the start of the address)
  engine        mkp224o  · using mkp224o (fast prefix engine)

  status        ● running
  speed         19.05M/sec
  keys tried    71.49M  over 4s
  odds so far   0.0%  chance of a hit by now
  typical wait  11h 6m  (50/50 point)
                ░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░

  no matches yet

  press q to stop  ·  progress is saved, you can resume later
```

| Flag | Default | Meaning |
|---|---|---|
| `-match` | `prefix` | `prefix`, `suffix` or `anywhere` |
| `-threads` | all cores | CPU threads to use |
| `-out` | `~/.vanityrig/keys` | where found keys are saved |
| `-stop-after` | `0` (never) | stop once this many are found |
| `-plain` | off | plain lines instead of the dashboard |

Press `q`, `Esc` or `Ctrl-C` to stop. Progress is saved continuously, so re-running
the same search continues from where it left off rather than starting over.

When a match is found the key is written to disk **before** it is announced, so a
crash between finding and saving cannot lose it.

### estimate

```sh
vanityrig estimate <pattern> [pattern...] [flags]
```

| Flag | Default | Meaning |
|---|---|---|
| `-match` | `prefix` | where the pattern may appear: `prefix`, `suffix`, `anywhere` |
| `-rate` | `22200000` | your combined throughput in keys/sec across all machines |
| `-budget` | `24h` | longest search you would accept; filters the suggested alternatives |

Multiple patterns are an OR — a match on any one of them wins, which is proportionally faster than waiting for one specific word.

### Example

```
$ vanityrig estimate borderland

Feasibility check: "borderland" (prefix match)

  Search space:    32^10 = 2^50 = 1,125,899,906,842,624 possibilities
  Your throughput: 22.20 million keys/sec

  Expected time to find:
    P50 (coin flip):    1.1 years
    mean:               1.6 years
    P90 (90% by then):  3.7 years

  Verdict: HARD — years on this hardware, but rentable compute can close the gap

  Rented compute (approximate on-demand list prices; Spot is typically
  much cheaper, and rates should be replaced by a calibration run):
    c7a.16xlarge   x109   for 24h  ->  50% chance   ~$8,607
    c7a.48xlarge   x37    for 24h  ->  51% chance   ~$8,756

  Your options — pick whichever suits you:

    search for               word appears       typical time    word
    ────────────────────────────────────────────────────────────────────
    borderland               start              1.1 years       full  (what you asked for)
  → borderland               anywhere           9.0 days        full  ← recommended
    borderla                 start (shortened)  9.5 hours       shortened
    border                   start (shortened)  34 seconds      shortened

    Recommended: borderland (anywhere) — same full word, much faster.
    That is only a suggestion; any option above is yours to run.
```

Exit codes:

| Code | Meaning |
|---|---|
| `0` | Achievable — however long the odds. A 56-character pattern needing 10⁶⁷ years still exits 0; the tool prints the numbers and gets out of your way. |
| `1` | Unsatisfiable — well-formed, but zero matching addresses exist (e.g. a suffix breaking the trailing-character rule). Still reports in full and suggests a way forward. |
| `2` | Malformed input or usage error — a typo, not a finding. |

The tool never blocks a long-shot search. "Astronomically unlikely" and "impossible" are different claims, and only the second one is immune to more hardware.

## What it knows that most tools don't

**It has its own search engine, so every mode actually runs.** `mkp224o` is used
when available because it is roughly 180x faster per core, but it can only match
prefixes. Suffix and anywhere searches run on a built-in Go engine instead, and the
tool says which one it picked and why. Keys it writes are byte-identical to
mkp224o's, verified against a real key pair in the test suite.

**Match position changes the cost dramatically.** A 10-character pattern has 47 possible placements in a 56-character address, so `anywhere` is roughly 45× cheaper than `prefix` — turning `borderland` from about 1.1 years into about 9 days on the same hardware. `mkp224o` and every other surveyed generator support prefixes only.

**Some suffixes are impossible, not just slow.** The fixed version byte pins the last two characters of every v3 address: character 55 is always `d`, and character 54 is always one of `a`/`i`/`q`/`y`. So `border` can never be a suffix, and neither can `borderland` (its penultimate `n` is disallowed). The tool tells you this up front — naming the rule you hit and offering a mode that works — instead of letting you search forever for something that does not exist. Valid suffixes get an 8-bit (256×) discount versus the same-length prefix, since the final character is free.

**It presents options rather than corrections.** Speed is not the only thing people care about — where the word lands and whether all of it survives usually matter more. The search you asked for always appears in the table alongside the alternatives, ranked to prefer keeping your whole word over finishing sooner. A recommendation is marked with a one-line reason, and only when something genuinely stands out. Wanting the word at the end badly enough to spend a year on it is a legitimate preference, not a mistake to be corrected.

**A word that cannot end an address can still sit just before the ending.** `borderland` can never finish an address, but `...borderlandid` can. The tool offers all four legal completions as one OR search, because any of them satisfies you equally and searching them together is exactly 4x faster than picking one.

**Estimates show their work.** Every report prints the exponent form, the exact count, the assumed rate and the derived percentiles together. This is deliberate: the search that motivated this project ran for hours against a hand-computed target that was wrong by 1024× (`2^60` used where `2^50` was correct), and the wrong number was invisible because only the conclusion was ever shown. The test suite pins the known-good constants and asserts the invariants that would have caught it.

**Impossible and infeasible are different claims.** A protocol violation is impossible; a 20-character prefix is merely astronomical. Reporting the latter as the former is a real hazard — naive probability math underflows to zero past 11 characters — and the tool is tested against it.

## Contributing

The biggest open pieces are multi-host orchestration, the match catalog, and
deploy-to-Tor — all designed in detail in [PROJECT.md](PROJECT.md) §4-§5,
including the SQLite schema and worker model. Issues and PRs welcome,
especially on those.

## License

[MIT](LICENSE)
