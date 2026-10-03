# Using your address with Tor

Every match VanityRig finds is saved as its own directory inside the output
folder (default `~/.vanityrig/keys`), named after the address:

```
~/.vanityrig/keys/cafexyz…abcd.onion/
├── hostname                # the full .onion address
├── hs_ed25519_public_key
└── hs_ed25519_secret_key   # keep this private — whoever has it owns the address
```

These are exactly the files Tor expects in a hidden service directory.

## 1. Copy the directory to where Tor can read it

```sh
sudo mkdir -p /var/lib/tor/my_service
sudo cp ~/.vanityrig/keys/<your-address>.onion/* /var/lib/tor/my_service/
sudo chown -R debian-tor:debian-tor /var/lib/tor/my_service   # user is "tor" on Arch/Fedora
sudo chmod 700 /var/lib/tor/my_service
sudo chmod 600 /var/lib/tor/my_service/*
```

Tor refuses to start if the directory permissions are too open.

## 2. Point Tor at it

Add to `/etc/tor/torrc`:

```
HiddenServiceDir /var/lib/tor/my_service/
HiddenServicePort 80 127.0.0.1:8080
```

This forwards port 80 of your `.onion` to a web server on local port 8080.

## 3. Restart Tor and check

```sh
sudo systemctl restart tor
sudo cat /var/lib/tor/my_service/hostname
```

The hostname printed must be the address VanityRig found. Open it in Tor
Browser to confirm the service is reachable.

## Keep the secret key safe

- Back up `hs_ed25519_secret_key` somewhere offline — if you lose it, the
  address is gone for good.
- Anyone with a copy can impersonate your service. Delete the matches you
  aren't going to use.
