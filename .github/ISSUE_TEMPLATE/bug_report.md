---
name: Bug report
about: Something doesn't work the way it should
title: ''
labels: bug
assignees: ''
---

**Describe the bug**
A clear and concise description of what went wrong.

**Command run**
```
vanityrig ...
```

**Expected behavior**
What you expected to happen instead.

**Actual behavior**
What actually happened — include the exact output/error text if there was one.

**Environment**
- VanityRig version: `vanityrig -h` first line, or the git commit
- OS/architecture: e.g. Linux x86_64, macOS arm64
- Go version (if built from source): `go version`
- `mkp224o` installed? (yes/no, and version if yes)

**Additional context**
Anything else that might help — a pattern that triggers it reliably, whether
it happens every time or intermittently, etc.

Found a security issue instead (weak keys, a leaked secret, crash on
untrusted input)? Please don't file it here — see
[SECURITY.md](../../SECURITY.md).
