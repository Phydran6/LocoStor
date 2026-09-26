# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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

[Unreleased]: https://github.com/Phydran6/LocoStor/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/Phydran6/LocoStor/releases/tag/v0.1.0
