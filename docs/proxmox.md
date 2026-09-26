# Proxmox VE setup

LocoStor runs inside a **privileged** Debian or Ubuntu container. The RAID
array stays on the Proxmox host and is bind-mounted into the container; the
disks are passed through so `smartctl` can read them.

The installer does all of that for you.

## 1. Create a container

In the Proxmox UI: **Create CT**, use a Debian 12/13 template and **untick
"Unprivileged container"**. 1 core, 512 MB RAM and 4 GB disk are plenty.
Nothing else needs to be configured.

## 2. Run the installer on the host

Open the shell of the Proxmox **host** (not the container) and run:

```sh
curl -fsSL https://raw.githubusercontent.com/Phydran6/LocoStor/main/scripts/install.sh | sh
```

It asks for the container ID (or pass it: `… | sh -s -- 105`), then:

1. finds the active mdadm arrays, their mount points and member disks,
2. shows what it is going to change and asks for confirmation,
3. adds the mount points (`mpN`) and disks (`devN`) to the container,
4. allows raw disk access for SMART (`lxc.cap.drop` override),
5. restarts the container and installs LocoStor inside,
6. asks for the admin password.

Then open `http://<container-ip>:8080`.

Running the installer again is safe: existing settings are kept and
LocoStor is updated to the latest release.

### Without RAID

If no mounted mdadm array is found, the installer asks for a host path to
share instead (e.g. `/mnt/data`).

## Existing shares

Shares you set up by hand before – in `smb.conf`, `ganesha.conf` or
`/etc/exports` – are listed under **Other shares on this system**. Click
**Take over** to manage one with LocoStor; the original file is backed up as
`<file>.locostor-<date>` first.

## Manual setup

If you prefer to configure the container yourself, add this to
`/etc/pve/lxc/<ID>.conf` on the host (Proxmox VE 8.1+):

```ini
mp0: /mnt/raid,mp=/mnt/raid
dev0: /dev/md0
dev1: /dev/sda
dev2: /dev/sdb
lxc.cap.drop:
lxc.cap.drop: mac_admin mac_override sys_time sys_module
```

and run the same one-liner **inside** the container.

**USB disks** usually need `-d sat`, which LocoStor tries automatically. You
can also pin the list in `/etc/locostor/config.json`:

```json
"smart_devices": [{ "device": "/dev/sda", "type": "sat" }]
```

## Troubleshooting

| Problem | Fix |
| --- | --- |
| "container is unprivileged" | Create a new container with *Unprivileged container* unticked. |
| `nfs-ganesha` fails to start | `journalctl -u nfs-ganesha`. With AppArmor errors add `lxc.apparmor.profile: unconfined` to the container config. |
| NFSv3 clients cannot mount | NFSv3 needs `rpcbind`: `apt-get install rpcbind`. NFSv4 works without it. |
| Disk letters changed after a reboot | Run the installer on the host again. |
| Forgot the admin password | `pct exec <ID> -- locostor passwd` |
| Logs | `pct exec <ID> -- journalctl -u locostor -f` |
