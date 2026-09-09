# AUR packaging

`PKGBUILD` here builds `vanityrig-bin`, installing the prebuilt Linux binary
from the GitHub Release rather than compiling from source (faster install,
no Go toolchain needed).

This is *not* published to the AUR yet — that requires an AUR account, which
is a personal account only whoever maintains it can create. To publish:

1. Create an account at [aur.archlinux.org](https://aur.archlinux.org) and
   add an SSH key (Account settings → My SSH Public Keys).
2. Bump `pkgver` here to match the release you're publishing, then generate
   real checksums (replacing the `SKIP` placeholders):
   ```sh
   cd packaging/aur
   updpkgsums
   makepkg --printsrcinfo > .SRCINFO
   ```
3. Clone the (empty, auto-created on first push) AUR git repo and push:
   ```sh
   git clone ssh://aur@aur.archlinux.org/vanityrig-bin.git aur-vanityrig-bin
   cp PKGBUILD .SRCINFO aur-vanityrig-bin/
   cd aur-vanityrig-bin
   git add PKGBUILD .SRCINFO
   git commit -m "vanityrig-bin $(grep -oP 'pkgver=\K.*' PKGBUILD)"
   git push origin master
   ```
4. On every new release, repeat step 2-3 (bump `pkgver`, regenerate
   checksums/`.SRCINFO`, push). This can be automated later with a small CI
   job once someone's AUR SSH key is available as a repository secret.

Once published, users install with an AUR helper:

```sh
yay -S vanityrig-bin
# or
paru -S vanityrig-bin
```

(`pacman` itself only installs from the official repos — `yay`/`paru` are
the standard way Arch users install AUR packages, which is the effective
equivalent of `pacman -S` for anything not in `core`/`extra`.)
