# VanityRig

Find a custom Tor `.onion` address — one that starts with, ends with, or
contains a word you choose — with an honest cost estimate before it runs.

```
$ vanityrig borderland

Feasibility check: "borderland" (prefix match)

  Search space:    32^10 = 2^50 = 1,125,899,906,842,624 possibilities
  Your throughput: 22.20 million keys/sec

  Expected time to find:
    P50 (coin flip):    1.1 years
    mean:               1.6 years
    P90 (90% by then):  3.7 years

  Verdict: HARD — years on this hardware, but rentable compute can close the gap

  Your options — pick whichever suits you:

    search for               word appears       typical time    word
    ────────────────────────────────────────────────────────────────────
    borderland               start              1.1 years       full  (what you asked for)
  → borderland               anywhere           9.0 days        full  ← recommended
    borderla                 start (shortened)  9.5 hours       shortened
    border                   start (shortened)  34 seconds      shortened

    Recommended: borderland (anywhere) — same full word, much faster.
    That is only a suggestion; any option above is yours to run.

Start the search now? [Y/n]:
```

## Install

Requires [Go](https://go.dev/dl/) 1.21+.

```sh
go install github.com/bytestrix/vanityrig/cmd/vanityrig@latest
```

`go install` puts the binary in `$(go env GOPATH)/bin`, which needs to be on
your `PATH`. If `vanityrig -h` doesn't work after installing, that's why —
add it once and it's fixed for good:

```sh
echo 'export PATH="$PATH:$(go env GOPATH)/bin"' >> ~/.zshrc   # or ~/.bashrc
source ~/.zshrc
```

Confirm it worked:

```sh
vanityrig -h
```

Building from source instead of installing:

```sh
git clone https://github.com/bytestrix/vanityrig.git
cd vanityrig
go build -o bin/vanityrig ./cmd/vanityrig
```

Optional: install [`mkp224o`](https://github.com/cathugger/mkp224o) and put it
on your `PATH`. VanityRig uses it automatically for prefix searches — it's
about 180x faster per core than VanityRig's own engine for that mode. Suffix
and anywhere searches always run on VanityRig's built-in engine, since
`mkp224o` can't do those at all.

## Usage

```sh
vanityrig <word> [word...] [flags]
```

That's the whole interface. Give it a word, it tells you the real cost, and
asks before it starts:

```sh
vanityrig borderx                        # check it, then decide
vanityrig borderland -match anywhere     # match anywhere in the address, not just the start
vanityrig borderx bordery -stop-after 3  # OR search, stop after 3 total matches
vanityrig borderx -check                 # just the numbers, don't offer to run it
vanityrig borderx -y                     # skip the prompt, start immediately
```

| Flag | Default | Meaning |
|---|---|---|
| `-match` | `prefix` | `prefix`, `suffix`, or `anywhere` |
| `-threads` | all cores | CPU threads to use |
| `-out` | `~/.vanityrig/keys` | where found keys are saved |
| `-stop-after` | `0` (never) | stop once this many matches are found |
| `-rate` | `22.2M` | assumed combined keys/sec, used for the estimate |
| `-budget` | `24h` | longest search you'd accept, for alternatives |
| `-check` | off | show the estimate and exit — don't offer to search |
| `-y` | off | skip the confirmation prompt |
| `-plain` | off | plain log lines instead of the live dashboard |

Press `q`, `Esc`, or `Ctrl-C` to stop a running search. Progress is saved
continuously, so running the same word again continues from where it left off
instead of starting over. A match is written to disk **before** it's
announced, so a crash between finding and saving can't lose it.

Exit codes: `0` achievable (however long the odds), `1` unsatisfiable (well
formed, but no matching address exists — reported in full, with a working
alternative), `2` malformed input.

## Why VanityRig

- **Matches anywhere in the address, not just the start.** A 10-character
  word has 47 possible positions in a 56-character address — matching any of
  them is often 10-50x faster than pinning it to the front. No other
  generator supports this.
- **Catches impossible searches before you waste time on them.** The last two
  characters of every v3 address are constrained by the protocol — some
  words can never appear at the end. VanityRig tells you the exact rule you
  hit and offers a fix, instead of searching forever for something that
  can't exist.
- **Shows its work.** Every estimate prints the exponent form, the exact
  count, the assumed rate, and the resulting time side by side, so a wrong
  number is visible on its face rather than hidden behind a verdict.
- **Presents trade-offs, not corrections.** The word you asked for always
  appears in the comparison table, ranked alongside faster or shorter
  alternatives — you decide, it doesn't decide for you.
- **Has its own search engine**, so suffix and anywhere-position matching
  actually run instead of being advertised and silently unsupported. Keys
  are byte-identical to `mkp224o`'s output, verified in the test suite.

## Contributing

Issues and pull requests are welcome. [PROJECT.md](PROJECT.md) has the full
design writeup — engine research, the data model, and the reasoning behind
every decision above — and is the place to start for anything nontrivial.

## License

[MIT](LICENSE)
