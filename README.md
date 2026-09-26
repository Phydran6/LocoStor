<p align="center">
  <img src="docs/images/logo.png" alt="LocoStor" width="280">
</p>

<p align="center">
  Storage management for a Proxmox VE container – SMB, NFS, RAID and SMART in one small web UI.
</p>

![Dashboard](docs/images/dashboard.png)

## Features

- **SMB shares** – create, edit and remove Samba shares; manage SMB users
- **NFS exports** – manage NFS-Ganesha exports (NFSv3/v4, client lists, squash)
- **Existing shares** – shares from `smb.conf`, `ganesha.conf` and `/etc/exports` are detected and can be taken over
- **RAID status** – read-only view of the host's mdadm arrays incl. rebuild progress
- **SMART** – disk health via `smartctl`, USB disks via `-d sat`, sleeping disks are not woken up
- **Self-update** – checks GitHub Releases, one-click update (SHA-256 verified) and rollback
- **Secure login** – username + password, optional two-factor login (TOTP) with recovery codes
- **HTTPS** – built in, via Caddy or Nginx Proxy Manager, or behind your own reverse proxy – chosen during install
- Dark mode, responsive layout, single binary without dependencies

LocoStor writes its shares to `/etc/samba/locostor.conf` and
`/etc/ganesha/locostor.conf`, which are included from the main configs.
Everything else in those files stays as it is.

## Installation

Create a **privileged** Debian container in Proxmox – no further setup
needed. Then run this on the Proxmox **host** shell:

```sh
curl -fsSL https://raw.githubusercontent.com/Phydran6/LocoStor/main/scripts/install.sh | sh
```

The installer asks for the container ID and how the web UI should be reached
(HTTP or one of the HTTPS options below), mounts the RAID, passes the disks
through for SMART, installs LocoStor in the container and asks for the admin
username and password. It prints the address to open at the end.

Details, manual setup and troubleshooting: **[docs/proxmox.md](docs/proxmox.md)**.
Run inside a container, the same command installs LocoStor only there.

## HTTPS

During installation you choose how the web UI is reached. The question
continues with option 1 after 10 seconds; when you run the installer again,
the default is to keep the current setting.

| Option | What happens |
| --- | --- |
| 1 – HTTP on port 8080 | For a reverse proxy elsewhere, e.g. Nginx Proxy Manager in another VM. |
| 2 – HTTPS, self-signed | LocoStor serves HTTPS on 443 with its own certificate; ports 80 and 8080 redirect. |
| 3 – HTTPS, own certificate | Same, with certificate and key files you provide. |
| 4 – Caddy | Caddy in the container: Let's Encrypt for a public domain, else its local CA. LocoStor only listens on localhost. |
| 5 – Nginx Proxy Manager | NPM in Docker in the container (admin UI on port 81); nesting is enabled automatically. |

You can switch later without the installer:

```sh
locostor tls self-signed          # HTTPS on :443 with a new self-signed certificate
locostor tls files CERT KEY       # HTTPS with your own certificate
locostor tls off                  # plain HTTP on :8080
systemctl restart locostor
```

Behind a reverse proxy, forward `X-Forwarded-Proto` and `Host` so session
cookies are marked secure and the same-origin check passes (Nginx Proxy
Manager does this by default).

## Security

- Username + password (bcrypt), optional **two-factor login** with an
  authenticator app (TOTP, RFC 6238) and ten single-use recovery codes
  (stored hashed). Set it up under *Settings*.
- Failed logins are throttled per client and globally; sessions are random
  256-bit tokens, stored only as hashes, `HttpOnly` + `SameSite=Strict`.
- Every state-changing API call must be a same-origin JSON request (CSRF
  protection); strict Content-Security-Policy and security headers.
- Shares cannot point at system directories (`/`, `/etc`, `/root`, …), and
  smb.conf options that run commands (`preexec`, `* command`, `* script`, …)
  are refused.
- All external programs are started without a shell; every value written to
  a config file is validated.
- Updates are verified against `SHA256SUMS` and checked to run before they
  replace the current binary.

Recovery from the console (inside the container):

```sh
locostor passwd [-user NAME]   # new password (and username)
locostor mfa-reset             # turn off two-factor login
```

See [SECURITY.md](SECURITY.md) for reporting vulnerabilities.

## Configuration

`/etc/locostor/config.json` (mode 0600, created on first start):

| Key | Default | Description |
| --- | --- | --- |
| `listen` | `:8080` | Listen address |
| `username` | `admin` | Admin username |
| `password_hash` | – | bcrypt hash, set with `locostor passwd` |
| `totp_secret`, `recovery_codes` | – | Two-factor login, managed in the UI |
| `tls_cert`, `tls_key` | – | Serve HTTPS with these files |
| `http_redirect` | – | Plain HTTP addresses that redirect to HTTPS |
| `update_repo` | `Phydran6/LocoStor` | GitHub repo for self-updates |
| `data_dir` | `/var/lib/locostor` | State files (shares, exports, sessions) |
| `smart_devices` | – | Fixed disk list, e.g. `[{"device": "/dev/sda", "type": "sat"}]` |

Commands:

```
locostor [-config FILE] [-listen ADDR]   start the server
locostor passwd [-user NAME]             set the admin password (and username)
locostor mfa-reset                       turn off two-factor login
locostor tls self-signed|files|off       switch HTTPS mode
locostor version                         print the version
locostor -demo                           run with fake data (login admin / demo)
```

## Updates and rollback

The **Update** page shows the installed and latest version with release
notes. *Install update* runs in the background and the page follows it live:
download progress, checksum check, test run of the new binary, install and
restart. The running binary is kept as `locostor.previous`; *Roll back*
swaps the two again. Shares are not interrupted – only the web UI restarts,
and you stay logged in.

## Development

Requirements: Go 1.26+, Node.js 22+.

```sh
cd web && npm ci && npm run build && cd ..
go run ./cmd/locostor -demo            # http://localhost:8080, login admin / demo
```

Demo mode works on any OS and never touches the system. For frontend work run
`npm run dev` in `web/` in parallel (proxies `/api` to port 8080).

```
cmd/locostor/     entry point, CLI
internal/api      REST API + static file serving
internal/smb      Samba shares and users
internal/nfs      NFS-Ganesha exports
internal/raid     /proc/mdstat parser
internal/smart    smartctl JSON parser
internal/update   GitHub Releases self-update
internal/auth     login, TOTP, sessions, throttling
internal/tlsutil  self-signed certificates
internal/demo     fake backends for -demo
web/              Svelte 5 + Tailwind 4 frontend (embedded from web/dist)
deploy/           systemd unit
scripts/          installer
```

## Releases

Versions follow [Semantic Versioning](https://semver.org). Pushing a tag
`vX.Y.Z` runs the release workflow, which builds `linux/amd64` and
`linux/arm64` binaries and publishes them with the matching section of
[CHANGELOG.md](CHANGELOG.md) as release notes.

```sh
git tag v0.3.0 && git push origin v0.3.0
```

## License

[MIT](LICENSE)
