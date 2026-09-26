#!/bin/sh
# LocoStor installer.
#
# Run it on the Proxmox VE host to set up an existing container and install
# LocoStor into it - no manual preparation needed:
#
#   curl -fsSL https://raw.githubusercontent.com/Phydran6/LocoStor/main/scripts/install.sh | sh
#   curl -fsSL .../install.sh | sh -s -- 104      # container ID given up front
#
# The host part detects the mdadm arrays, bind-mounts their mount points,
# passes the member disks through for SMART and allows raw disk access.
#
# Run inside a Debian/Ubuntu container, it only installs LocoStor there.
set -eu

REPO="Phydran6/LocoStor"
RAW="https://raw.githubusercontent.com/$REPO/main"
BIN=/usr/local/bin/locostor
CONFIG=/etc/locostor/config.json

say() { printf '\033[1;33m==>\033[0m %s\n' "$*"; }
info() { printf '    %s\n' "$*"; }
warn() { printf '\033[1;33mwarning:\033[0m %s\n' "$*" >&2; }
die() {
  printf '\033[1;31merror:\033[0m %s\n' "$*" >&2
  exit 1
}

has() { command -v "$1" >/dev/null 2>&1; }

fetch() { # url dest
  if has curl; then
    curl -fsSL -o "$2" "$1"
  elif has wget; then
    wget -qO "$2" "$1"
  else
    die "neither curl nor wget is installed"
  fi
}

tty_ok() { [ -r /dev/tty ] && { : </dev/tty; } 2>/dev/null; }

ask() { # prompt -> $REPLY
  tty_ok || die "no terminal for input - pass the container ID: ... | sh -s -- <ID>"
  printf '%s' "$1" >/dev/tty
  read -r REPLY </dev/tty
}

ask_secret() { # prompt -> $REPLY
  printf '%s' "$1" >/dev/tty
  stty -echo </dev/tty
  read -r REPLY </dev/tty || true
  stty echo </dev/tty
  printf '\n' >/dev/tty
}

# ---------------------------------------------------------------------------
# Inside the container
# ---------------------------------------------------------------------------

install_container() {
  has apt-get || die "only Debian/Ubuntu (apt) is supported"

  case "$(uname -m)" in
    x86_64) ARCH=amd64 ;;
    aarch64 | arm64) ARCH=arm64 ;;
    *) die "unsupported architecture: $(uname -m)" ;;
  esac

  say "Installing packages (samba, nfs-ganesha, smartmontools)"
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -qq
  apt-get install -y -qq --no-install-recommends \
    samba nfs-ganesha nfs-ganesha-vfs smartmontools curl ca-certificates >/dev/null

  say "Downloading LocoStor (linux/$ARCH)"
  TMP=$(mktemp -d)
  trap 'rm -rf "$TMP"' EXIT
  URL="https://github.com/$REPO/releases/latest/download"
  fetch "$URL/locostor-linux-$ARCH" "$TMP/locostor-linux-$ARCH"
  fetch "$URL/SHA256SUMS" "$TMP/SHA256SUMS"
  (cd "$TMP" && grep " locostor-linux-$ARCH\$" SHA256SUMS | sha256sum -c --quiet -) || die "checksum mismatch"
  install -m 0755 "$TMP/locostor-linux-$ARCH" "$BIN"
  info "installed $("$BIN" version) to $BIN"

  # Shares with "guest ok" need guests mapped to the guest account.
  if [ -f /etc/samba/smb.conf ] && ! grep -qi '^[[:space:]]*map to guest' /etc/samba/smb.conf; then
    sed -i '/^\[global\]/a\   map to guest = bad user' /etc/samba/smb.conf
  fi

  say "Installing systemd service"
  cat >/etc/systemd/system/locostor.service <<'EOF'
[Unit]
Description=LocoStor storage management web UI
Documentation=https://github.com/Phydran6/LocoStor
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=/usr/local/bin/locostor -config /etc/locostor/config.json
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
EOF
  systemctl daemon-reload

  # The host installer sets the password itself (LOCOSTOR_NO_PASSWD=1).
  if [ -z "${LOCOSTOR_NO_PASSWD:-}" ] && ! grep -q password_hash "$CONFIG" 2>/dev/null; then
    if tty_ok; then
      say "Set the admin password for the web UI"
      until "$BIN" passwd -config "$CONFIG" </dev/tty; do :; done
    else
      warn "no terminal - an initial password is printed to: journalctl -u locostor"
    fi
  fi

  systemctl enable --now smbd nfs-ganesha >/dev/null 2>&1 || true
  systemctl enable locostor >/dev/null 2>&1
  systemctl restart locostor

  if [ -z "${LOCOSTOR_NO_PASSWD:-}" ]; then
    if [ ! -e /proc/mdstat ] || ! ls /dev/sd? /dev/nvme? >/dev/null 2>&1; then
      warn "no disks are visible in this container, so SMART will be empty."
      info "Run the same one-liner on the Proxmox host to pass them through."
    fi
    IP=$(hostname -I 2>/dev/null | awk '{print $1}')
    say "Done. Open http://${IP:-<container-ip>}:8080"
  fi
}

# ---------------------------------------------------------------------------
# On the Proxmox VE host
# ---------------------------------------------------------------------------

# next_index <prefix> <conf>: first unused mpN / devN key
next_index() {
  i=0
  while grep -q "^$1$i:" "$2"; do i=$((i + 1)); done
  echo "$i"
}

# ct_exec <id> <cmd...>
ct_exec() {
  id=$1
  shift
  pct exec "$id" -- "$@"
}

install_host() {
  CT=${1:-}
  if [ -z "$CT" ]; then
    say "Containers on this host"
    pct list
    ask "Container ID to install LocoStor into: "
    CT=$REPLY
  fi
  case "$CT" in '' | *[!0-9]*) die "invalid container ID: $CT" ;; esac
  CONF=/etc/pve/lxc/$CT.conf
  [ -f "$CONF" ] || die "container $CT does not exist"

  if grep -q '^unprivileged: *1' "$CONF"; then
    die "container $CT is unprivileged. LocoStor needs direct disk access - create a container with \"Unprivileged container\" unticked and run this again."
  fi
  case "$(sed -n 's/^ostype: *//p' "$CONF")" in
    debian | ubuntu) ;;
    *) die "container $CT must run Debian or Ubuntu" ;;
  esac

  # --- detect what to pass through --------------------------------------
  MOUNTS=""
  DEVS=""
  for md in $(awk '/^md[0-9]+ : active/ {print $1}' /proc/mdstat 2>/dev/null); do
    DEVS="$DEVS /dev/$md"
    for mp in $(findmnt -rn -S "/dev/$md" -o TARGET 2>/dev/null); do
      MOUNTS="$MOUNTS $mp"
    done
    for slave in /sys/block/"$md"/slaves/*; do
      [ -e "$slave" ] || continue
      name=$(basename "$slave")
      parent=$(lsblk -no PKNAME "/dev/$name" 2>/dev/null | head -n1)
      DEVS="$DEVS /dev/${parent:-$name}"
    done
  done
  DEVS=$(printf '%s\n' $DEVS | awk 'NF && !seen[$0]++')
  MOUNTS=$(printf '%s\n' $MOUNTS | awk 'NF && !seen[$0]++')

  if [ -z "$MOUNTS" ]; then
    warn "no mounted mdadm array found on this host."
    ask "Host path to share (e.g. /mnt/data, empty = skip): "
    [ -n "$REPLY" ] && MOUNTS=$REPLY
  fi
  for mp in $MOUNTS; do
    [ -d "$mp" ] || die "$mp does not exist on the host"
  done

  # --- plan ----------------------------------------------------------------
  NEW_MOUNTS=""
  for mp in $MOUNTS; do
    grep -Eq "^mp[0-9]+:.*[ ,]mp=$mp(,|$)" "$CONF" || NEW_MOUNTS="$NEW_MOUNTS $mp"
  done
  NEW_DEVS=""
  for dev in $DEVS; do
    grep -Eq "^dev[0-9]+: *(path=)?$dev(,|$)" "$CONF" || NEW_DEVS="$NEW_DEVS $dev"
  done
  NEED_CAP=1
  sed '/^\[/,$d' "$CONF" | grep -q '^lxc.cap.drop: *$' && NEED_CAP=""

  say "Container $CT ($(sed -n 's/^hostname: *//p' "$CONF"))"
  if [ -n "$NEW_MOUNTS$NEW_DEVS$NEED_CAP" ]; then
    for mp in $NEW_MOUNTS; do info "mount  $mp -> $mp"; done
    for dev in $NEW_DEVS; do info "disk   $dev (for RAID status and SMART)"; done
    [ -n "$NEED_CAP" ] && info "allow  raw disk access (CAP_SYS_RAWIO, needed by smartctl)"
    RUNNING=""
    pct status "$CT" | grep -q running && RUNNING=1
    [ -n "$RUNNING" ] && info "The container will be restarted."
    ask "Apply these changes? [Y/n] "
    case "$REPLY" in n* | N*) die "aborted" ;; esac

    [ -n "$RUNNING" ] && {
      say "Stopping container $CT"
      pct shutdown "$CT" --timeout 60 || pct stop "$CT"
    }
    for mp in $NEW_MOUNTS; do
      pct set "$CT" "-mp$(next_index mp "$CONF")" "$mp,mp=$mp"
    done
    for dev in $NEW_DEVS; do
      pct set "$CT" "-dev$(next_index dev "$CONF")" "$dev" ||
        die "could not pass $dev through (needs Proxmox VE 8.1 or newer)"
    done
    if [ -n "$NEED_CAP" ]; then
      # Raw lxc.* keys must go before the first [snapshot] section.
      tmp=$(mktemp)
      awk 'BEGIN { add = "lxc.cap.drop:\nlxc.cap.drop: mac_admin mac_override sys_time sys_module" }
        /^\[/ && !done { print add; done = 1 }
        { print }
        END { if (!done) print add }' "$CONF" >"$tmp"
      cat "$tmp" >"$CONF"
      rm -f "$tmp"
    fi
  else
    info "already set up"
  fi

  if ! pct status "$CT" | grep -q running; then
    say "Starting container $CT"
    pct start "$CT"
  fi
  say "Waiting for the network in container $CT"
  n=0
  until ct_exec "$CT" sh -c 'ip -4 route | grep -q default' 2>/dev/null; do
    n=$((n + 1))
    [ "$n" -lt 60 ] || die "container $CT has no network"
    sleep 1
  done

  # --- install inside ------------------------------------------------------
  HAD_PW=""
  ct_exec "$CT" grep -q password_hash "$CONFIG" 2>/dev/null && HAD_PW=1
  tmp=$(mktemp)
  fetch "$RAW/scripts/install.sh" "$tmp"
  pct push "$CT" "$tmp" /root/locostor-install.sh
  rm -f "$tmp"
  ct_exec "$CT" env LOCOSTOR_NO_PASSWD=1 sh /root/locostor-install.sh
  ct_exec "$CT" rm -f /root/locostor-install.sh

  # First install: replace the generated initial password with a chosen one.
  if [ -z "$HAD_PW" ] && ! tty_ok; then
    warn "no terminal - see the initial password with: pct exec $CT -- journalctl -u locostor"
  elif [ -z "$HAD_PW" ]; then
    say "Set the admin password for the web UI"
    while :; do
      ask_secret "New admin password: "
      pw=$REPLY
      ask_secret "Repeat password: "
      if [ "$pw" != "$REPLY" ]; then
        warn "passwords do not match"
      elif [ "${#pw}" -lt 8 ]; then
        warn "at least 8 characters, please"
      else
        break
      fi
    done
    printf '%s\n' "$pw" | ct_exec "$CT" "$BIN" passwd -config "$CONFIG" >/dev/null 2>&1 ||
      die "could not set the password"
    ct_exec "$CT" systemctl restart locostor
  fi

  IP=$(ct_exec "$CT" hostname -I 2>/dev/null | awk '{print $1}')
  say "Done. Open http://${IP:-<container-ip>}:8080"
}

# ---------------------------------------------------------------------------

[ "$(id -u)" -eq 0 ] || die "please run as root"

if has pct && [ -d /etc/pve ]; then
  install_host "$@"
else
  install_container
fi
