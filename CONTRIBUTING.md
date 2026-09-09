# Contributing to VanityRig

Thanks for considering it. This project is small enough that most
contributions are welcome without much ceremony — but a few things make the
process smoother for both of us.

## Before you start

- **Small fix or clear bug?** Just open a pull request.
- **Anything bigger** (a new engine, a new command, a behavior change) —
  please open an issue first to discuss the approach. It's a much smaller
  loss of time than writing a PR that gets rearchitected in review.
- Check open issues and pull requests first so you're not duplicating work.

## Development setup

Requires Go 1.21+.

```sh
git clone https://github.com/bytestrix/vanityrig.git
cd vanityrig
go build -o bin/vanityrig ./cmd/vanityrig
go test ./...
```

Optionally install [`mkp224o`](https://github.com/cathugger/mkp224o) on your
`PATH` if you're touching the engine that wraps it.

## Before opening a pull request

```sh
go vet ./...
go test ./...
```

Both must pass. There's no separate linter configured beyond `go vet` at the
moment — if you add one, say so in your PR description.

## Code style

- Keep the existing tone: comments explain *why*, not *what* — see the
  existing code for the pattern. Don't add a comment that just restates the
  line below it.
- No unnecessary abstractions. If three call sites do something similar,
  that's fine; don't introduce an interface or a config struct for it unless
  a fourth one actually shows up.
- New behavior needs a test. This project treats its own feasibility math and
  protocol constraints as things a regression could silently get wrong (see
  the accuracy-focused tests in `internal/vanity`) — new logic should hold
  itself to the same standard.

## Reporting bugs

Use the bug report issue template. Include the exact command you ran and
what happened — for a CLI tool that's almost always enough to reproduce it.

Found a security issue (weak key generation, a leaked secret, anything that
could compromise a generated identity)? Please don't file a public issue —
see [SECURITY.md](SECURITY.md) instead.

## Code of Conduct

This project follows the [Code of Conduct](CODE_OF_CONDUCT.md). By
participating, you're expected to uphold it.

## Where to start

Look for issues labeled **good first issue**. The engine abstraction
(`internal/engine`) and the feasibility math (`internal/vanity`) are the two
areas with the most room for well-scoped, self-contained contributions —
adding a new search engine or refining an estimate are both good entry
points that don't require understanding the whole codebase first.
