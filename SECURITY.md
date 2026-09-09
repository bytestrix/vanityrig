# Security Policy

VanityRig generates Ed25519 key pairs that become the permanent identity of a
Tor hidden service. A bug that weakens key generation, leaks a secret key, or
produces an address different from the one actually searched for is a
security issue, not an ordinary bug — please report it privately.

## Supported versions

Only the latest tagged release is supported. There is no long-term support
branch at this stage of the project; always upgrade to the newest tag before
reporting an issue.

## Reporting a vulnerability

**Do not open a public GitHub issue for a security report.**

Email **support@bytestrix.com** with:

- A description of the issue and its impact (e.g. weak randomness, a key
  written somewhere it shouldn't be, a crash triggerable by attacker-controlled
  input).
- Steps to reproduce, or a proof of concept if you have one.
- The `vanityrig` version (or commit) and platform you found it on.

You should get an acknowledgment within a few days. We'll work with you on a
fix and a coordinated disclosure timeline before anything is made public —
typically via a GitHub Security Advisory once a patch is ready.

## Scope

In scope:
- The key-generation and matching logic in `internal/vanity` and
  `internal/engine`.
- How found keys are written to disk (permissions, partial writes, races).
- The feasibility/estimate math, if a flaw could cause a user to make an
  unsafe decision based on it (e.g. silently understating risk).

Out of scope:
- Vulnerabilities in `mkp224o` itself — report those upstream at
  [cathugger/mkp224o](https://github.com/cathugger/mkp224o).
- Anything requiring the attacker to already control the machine VanityRig
  runs on.
