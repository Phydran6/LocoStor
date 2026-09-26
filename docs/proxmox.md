# Proxmox VE setup

LocoStor runs inside a **privileged** LXC container. The RAID array lives on
the Proxmox host and is bind-mounted into the container; the disks are passed
through read-only so `smartctl` can query them.

Replace `105` with your container ID and `/mnt/raid` with your mount point.

## 1. Create the container

Create a Debian 12 (or newer) container in the Proxmox UI and **untick
"Unprivileged container"**. 1 CPU core, 512 MB RAM and 4 GB disk are plenty.

## 2. Bind-mount the RAID

On the host, the array must be mounted (e.g. `/dev/md0` on `/mnt/raid` via
`/etc/fstab`). Then:

```sh
pct set 105 -mp0 /mnt/raid,mp=/mnt/raid
```

RAID status is read from `/proc/mdstat` and `/sys/block/md*`, which the host
kernel exposes to the container automatically – nothing else is needed for
the RAID page.

## 3. Pass disks through for SMART (optional)

Add to `/etc/pve/lxc/105.conf` on the host, one `lxc.mount.entry` per disk:

```ini
# SATA / SAS / USB disks (/dev/sdX, block major 8)
lxc.cgroup2.devices.allow: b 8:* r
lxc.mount.entry: /dev/sda dev/sda none bind,optional,create=file
lxc.mount.entry: /dev/sdb dev/sdb none bind,optional,create=file

# SMART pass-through needs CAP_SYS_RAWIO, which Proxmox drops by default.
lxc.cap.drop:
lxc.cap.drop: mac_admin mac_override sys_time sys_module
```

For NVMe drives, check the character device major with `ls -l /dev/nvme0`
(e.g. `crw------- 1 root root 241, 0 …`) and add:

```ini
lxc.cgroup2.devices.allow: c 241:* r
lxc.mount.entry: /dev/nvme0 dev/nvme0 none bind,optional,create=file
```

Restart the container (`pct reboot 105`) afterwards.

**USB disks:** most USB-SATA bridges need `-d sat`. LocoStor retries with
`-d sat` automatically; if a disk still shows no data, pin the type in
`/etc/locostor/config.json`:

```json
"smart_devices": [
  { "device": "/dev/sda", "type": "sat" },
  { "device": "/dev/sdb", "type": "sat" }
]
```

When `smart_devices` is set, only the listed disks are queried.

## 4. Install LocoStor

Inside the container (`pct enter 105`):

```sh
apt-get update && apt-get install -y curl
curl -fsSL https://raw.githubusercontent.com/Phydran6/LocoStor/main/scripts/install.sh | sh
```

The script installs Samba, NFS-Ganesha and smartmontools, downloads the
latest release, asks for an admin password and starts the service. Then open
`http://<container-ip>:8080`.

## Troubleshooting

| Problem | Fix |
| --- | --- |
| `nfs-ganesha` fails to start | Check `journalctl -u nfs-ganesha`. With AppArmor issues add `lxc.apparmor.profile: unconfined` to the container config. |
| NFSv3 clients cannot mount | NFSv3 needs `rpcbind`: `apt-get install rpcbind`. NFSv4 works without it. |
| SMART shows "Permission denied" | Check the `lxc.cap.drop` lines and the device allow rules above. |
| Forgot the admin password | `locostor passwd` inside the container. |
| Logs | `journalctl -u locostor -f` |
