<p align="center">
  <img src="docs/banner.svg" alt="VanityRig — Tor v3 vanity .onion generator with prefix/suffix/anywhere matching" width="100%" />
</p>

<p align="center">
  <a href="https://pkg.go.dev/github.com/bytestrix/vanityrig"><img src="https://pkg.go.dev/badge/github.com/bytestrix/vanityrig.svg" alt="Go Reference"></a>
  <a href="https://goreportcard.com/report/github.com/bytestrix/vanityrig"><img src="https://goreportcard.com/badge/github.com/bytestrix/vanityrig" alt="Go Report Card"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/License-MIT-blue.svg" alt="License: MIT"></a>
</p>

<p align="center">
  <a href="#quick-start">Quick start</a> ·
  <a href="#usage">Usage</a> ·
  <a href="#why-vanityrig">Why VanityRig</a> ·
  <a href="#contributing">Contributing</a>
</p>

---

## What is it?

A Tor v3 `.onion` address is 56 random-looking characters. VanityRig finds
one containing a word you choose — at the start, at the end, or anywhere in
the middle — and tells you honestly what that will cost **before** it spends
any CPU time on it.

It's built on [`mkp224o`](https://github.com/cathugger/mkp224o), the
standard generator for this, and adds the three things that tool doesn't
have: an accurate up-front time/cost estimate, a live progress dashboard, and
the ability to match a word anywhere in the address rather than only at the
start — which is often dramatically faster than people expect.

---

## Quick start

**1. Install**

```sh
go install github.com/bytestrix/vanityrig/cmd/vanityrig@latest
```

Requires [Go](https://go.dev/dl/) 1.21+. This installs to `$(go env
GOPATH)/bin` — if `vanityrig -h` doesn't run afterward, that directory isn't
on your `PATH` yet:

```sh
echo 'export PATH="$PATH:$(go env GOPATH)/bin"' >> ~/.zshrc   # or ~/.bashrc
source ~/.zshrc
```

**2. Run it**

```sh
vanityrig borderx
```

That's the whole flow: it prints the search space, the expected time, cheaper
alternatives, and asks before it starts.

<details>
<summary>Other ways to install</summary>

Build from source:

```sh
git clone https://github.com/bytestrix/vanityrig.git
cd vanityrig
go build -o bin/vanityrig ./cmd/vanityrig
go test ./...
```

Optional: install [`mkp224o`](https://github.com/cathugger/mkp224o) and put
it on your `PATH`. VanityRig uses it automatically for prefix searches — it's
about 180x faster per core than VanityRig's own engine for that mode. Suffix
and anywhere searches always run on VanityRig's built-in engine, since
`mkp224o` can't do those at all.

</details>

---

## Usage

```sh
vanityrig <word> [word...] [flags]
```

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

**Exit codes:** `0` achievable (however long the odds), `1` unsatisfiable
(well formed, but no matching address exists — reported in full, with a
working alternative), `2` malformed input.

---

## Why VanityRig

- **Matches anywhere in the address, not just the start.** A 10-character
  word has 47 possible positions in a 56-character address — matching any of
  them is often 10-50x faster than pinning it to the front. No other
  generator supports this.
- **Catches impossible searches before you waste time on them.** The last two
  characters of every v3 address are constrained by the protocol — some
  words can never appear at the end. VanityRig names the exact rule you hit
  and offers a fix, instead of searching forever for something that can't
  exist.
- **Shows its work.** Every estimate prints the exponent form, the exact
  count, the assumed rate, and the resulting time side by side, so a wrong
  number is visible on its face rather than hidden behind a verdict.
- **Presents trade-offs, not corrections.** The word you asked for always
  appears in the comparison table, ranked alongside faster or shorter
  alternatives — you decide, it doesn't decide for you.
- **Has its own search engine**, so suffix and anywhere-position matching
  actually run instead of being advertised and silently unsupported. Keys
  are byte-identical to `mkp224o`'s output, verified in the test suite.

---

## Contributing

Issues and pull requests are welcome. [PROJECT.md](PROJECT.md) has the full
design writeup — engine research, the data model, and the reasoning behind
every decision above — and is the place to start for anything nontrivial.

## License

[MIT](LICENSE)
