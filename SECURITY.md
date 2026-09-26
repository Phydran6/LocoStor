# Security policy

LocoStor runs as root and manages file shares, so security reports are
taken seriously.

## Reporting a vulnerability

Please **do not open a public issue**. Use GitHub's private reporting
instead: *Security → Report a vulnerability* in this repository.

Include the affected version (`locostor version`), what an attacker can do
and how to reproduce it. You will get an answer within a few days; fixes are
released as a new patch version and mentioned in the changelog.

## Supported versions

Only the latest release receives security fixes. Update from the web UI
(*Update*) or by running the installer again.

## Hardening tips

- Turn on two-factor login under *Settings*.
- Do not expose the web UI to the internet; use a VPN or restrict access in
  your reverse proxy.
- Use HTTPS (see the README) when the UI is reached over an untrusted network.
