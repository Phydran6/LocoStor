# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.4.0] - 2026-10-04

### Added

- Shares of the Proxmox host itself: SMB shares (smb.conf), Samba users and kernel NFS exports (`/etc/exports`, `/etc/exports.d`) of the host are shown and can be added, edited and removed from the web UI
- Host changes are made in place – only the affected `[section]` or export line is rewritten, comments and everything else stay; the host keeps owning its config, so manual edits keep working
- Every host change is checked first (`testparm`, `exportfs -ra`), backed up (last 5 kept) and rolled back if the service rejects it
- `locostor host-agent`: small service on the Proxmox host, reachable only through a Unix socket bind-mounted into the container; the installer sets it up when run on the host
- "Proxmox host / This container" tabs on the SMB, SMB users and NFS pages; dashboard counts host shares and shows host services
- Update page shows the host agent version and can update it

### Changed

- Detected sections end at their last setting, so comments above the next section stay where they are

## [0.3.0] - 2026-09-26

### Added

- Login with username and password
- Two-factor login with authenticator apps (TOTP) and ten single-use recovery codes
- Settings: change username, set up / turn off two-factor login, new recovery codes
- `locostor passwd -user NAME`, `locostor mfa-reset` and `locostor tls self-signed|files|off`
- Installer asks how the web UI is reached (continues with HTTP on 8080 after 10 seconds): plain HTTP for an external reverse proxy, built-in HTTPS with a self-signed or own certificate, Caddy, or Nginx Proxy Manager in the container
- Built-in HTTPS with redirect from ports 80 and 8080
- Live update progress: steps, download progress and log; the page reloads once the new version runs
- About page with links to the repository, documentation, changelog, releases and issues
- SECURITY.md

### Changed

- Detection of existing SMB shares asks Samba (`testparm`), so shares from includes, the registry and `net usershare` are found; NFS detection reads `%dir`, relative `%include`, `/etc/exports.d` and no longer gives up on a file with a syntax error
- Share pages show which sources were searched and any problems found
- Sessions survive restarts, so an update no longer logs you out
- Logo and icons get versioned file names, so browsers never show stale ones
- SMB user passwords need at least 8 characters

### Security

- Brute-force protection per client and globally; timing-safe login checks
- Session tokens stored hashed; cookies `Secure` behind HTTPS proxies
- Content-Security-Policy, HSTS (with HTTPS), COOP/CORP, Permissions-Policy, `no-store` for API responses
- CSRF protection extended to the login and checked against the `Origin` header
- Shares may no longer point at system directories (`/`, `/etc`, `/root`, `/proc`, …), also via symlinks
- smb.conf options that run commands (`preexec`, `* command`, `* script`, `magic script`, …) are refused
- smartctl device names are validated; update downloads are size-limited
- HTTP server timeouts; TLS 1.2 minimum

## [0.2.0] - 2026-09-26

### Added

- One-line installer on the Proxmox host: detects mdadm arrays, bind-mounts them, passes member disks through for SMART, allows raw disk access and installs LocoStor into an existing container
- Detection of existing shares in `smb.conf` (and its includes), `ganesha.conf` and `/etc/exports`, with one-click takeover (original file is backed up)
- Additional smb.conf options per share ("More options")
- Hidden SMB share names ending in `$`
- Logo, favicon and app icons

### Changed

- New look based on the logo: gold accent, blue-black dark theme, calmer dashboard
- Installer works without `curl` (falls back to `wget`) and writes the systemd unit itself

## [0.1.0] - 2026-09-26

### Added

- SMB share management (Samba) with validation via `testparm` and live reload
- SMB user management (add, change password, remove)
- NFS export management (NFS-Ganesha) with client lists, access, squash and NFSv3/v4
- Read-only RAID status from `/proc/mdstat` and sysfs, including rebuild progress
- SMART overview and attribute details via `smartctl` JSON, USB disks via `-d sat`, standby disks are not woken up
- Self-update from GitHub Releases with SHA-256 verification and one-click rollback
- Dashboard with storage usage, services, RAID and disk health
- Password login, dark mode, responsive layout
- Demo mode (`-demo`) with fake data
- Installer script, systemd unit and Proxmox setup guide
- GitHub Actions for CI and releases (linux/amd64, linux/arm64)

[Unreleased]: https://github.com/Phydran6/LocoStor/compare/v0.4.0...HEAD
[0.4.0]: https://github.com/Phydran6/LocoStor/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/Phydran6/LocoStor/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/Phydran6/LocoStor/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/Phydran6/LocoStor/releases/tag/v0.1.0
