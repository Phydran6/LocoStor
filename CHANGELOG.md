# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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

[Unreleased]: https://github.com/Phydran6/LocoStor/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/Phydran6/LocoStor/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/Phydran6/LocoStor/releases/tag/v0.1.0
