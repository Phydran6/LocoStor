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
# Run inside a Debian/Ubuntu container, it only installs LocoStor there.
#
# Unattended use (inside the container): LOCOSTOR_HTTPS=1..5 (see below),
# LOCOSTOR_DOMAIN, LOCOSTOR_CERT, LOCOSTOR_KEY, and LOCOSTOR_PW_STDIN=1 with
# "username\npassword\n" on stdin.
set -eu

REPO="Phydran6/LocoStor"
RAW="https://raw.githubusercontent.com/$REPO/main"
BIN=/usr/local/bin/locostor
CONFIG=/etc/locostor/config.json
HTTPS_TIMEOUT=10

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

# ask_timeout seconds prompt -> $REPLY ("" when nothing was typed in time)
ask_timeout() {
  printf '%s' "$2" >/dev/tty
  if ! REPLY=$(timeout "$1" sh -c 'IFS= read -r l </dev/tty && printf %s "$l"' 2>/dev/null); then
    REPLY=""
    printf '\n' >/dev/tty
  fi
}

ask_secret() { # prompt -> $REPLY
  printf '%s' "$1" >/dev/tty
  stty -echo </dev/tty
  read -r REPLY </dev/tty || true
  stty echo </dev/tty
  printf '\n' >/dev/tty
}

valid_user() { printf '%s' "$1" | grep -Eq '^[A-Za-z0-9][A-Za-z0-9._@-]{0,63}$'; }
valid_domain() { printf '%s' "$1" | grep -Eq '^[A-Za-z0-9]([A-Za-z0-9-]{0,62}\.)+[A-Za-z]{2,63}$'; }

# ask_credentials -> ADMIN_USER, ADMIN_PW
ask_credentials() {
  say "Admin login for the web UI"
  while :; do
    ask "Username [admin]: "
    ADMIN_USER=${REPLY:-admin}
    valid_user "$ADMIN_USER" && break
    warn "letters, digits, '.', '_', '@' and '-' only"
  done
  while :; do
    ask_secret "Password (8+ characters): "
    ADMIN_PW=$REPLY
    ask_secret "Repeat password: "
    if [ "$ADMIN_PW" != "$REPLY" ]; then
      warn "passwords do not match"
    elif [ "${#ADMIN_PW}" -lt 8 ] || [ "${#ADMIN_PW}" -gt 72 ]; then
      warn "8 to 72 characters, please"
    else
      break
    fi
  done
}

# ask_https fresh|existing -> HTTPS_MODE ("" = keep current setting),
# DOMAIN, CERT, KEY. Continues with the default after HTTPS_TIMEOUT seconds.
ask_https() {
  HTTPS_MODE="" DOMAIN="" CERT="" KEY=""
  tty_ok || return 0
  if [ "$1" = fresh ]; then
    default="1" dtext="1"
  else
    default="" dtext="keep the current setting"
  fi
  say "How should the web UI be reached?"
  info "1) HTTP on port 8080 - e.g. behind a reverse proxy on another machine (default)"
  info "2) HTTPS on port 443 by LocoStor itself, self-signed certificate"
  info "3) HTTPS on port 443 by LocoStor itself, your own certificate files"
  info "4) Caddy in this container: Let's Encrypt for a public domain, else a local certificate"
  info "5) Nginx Proxy Manager in this container (Docker, admin UI on port 81)"
  ask_timeout "$HTTPS_TIMEOUT" "    Choice [1-5] - continues with $dtext in ${HTTPS_TIMEOUT}s: "
  case "$REPLY" in
    1 | 2 | 3 | 4 | 5) HTTPS_MODE=$REPLY ;;
    "") HTTPS_MODE=$default ;;
    *) warn "unknown choice '$REPLY' - using $dtext" && HTTPS_MODE=$default ;;
  esac
  case "$HTTPS_MODE" in
    3)
      while :; do
        ask "Certificate file (PEM, full chain): "
        CERT=$REPLY
        ask "Private key file (PEM): "
        KEY=$REPLY
        [ -r "$CERT" ] && [ -r "$KEY" ] && break
        warn "cannot read these files"
      done
      ;;
    4)
      while :; do
        ask "Public domain for Let's Encrypt (empty = local certificate): "
        DOMAIN=$REPLY
        [ -z "$DOMAIN" ] || valid_domain "$DOMAIN" && break
        warn "not a valid domain name"
      done
      ;;
  esac
}

# ---------------------------------------------------------------------------
# Inside the container
# ---------------------------------------------------------------------------

setup_caddy() {
  say "Configuring Caddy"
  if [ -n "$DOMAIN" ]; then
    printf '# Written by the LocoStor installer\n%s {\n\treverse_proxy 127.0.0.1:8080\n}\n' "$DOMAIN" >/etc/caddy/Caddyfile
  else
    printf '# Written by the LocoStor installer\nhttps:// {\n\ttls internal {\n\t\ton_demand\n\t}\n\treverse_proxy 127.0.0.1:8080\n}\n' >/etc/caddy/Caddyfile
  fi
  systemctl enable caddy >/dev/null 2>&1
  systemctl restart caddy
}

setup_npm() {
  say "Starting Nginx Proxy Manager"
  systemctl enable --now docker >/dev/null 2>&1 || true
  if ! docker info >/dev/null 2>&1; then
    warn "Docker does not run in this container. On the Proxmox host run: pct set <ID> -features nesting=1"
    warn "then restart the container and run this installer again."
    return 0
  fi
  if docker ps -a --format '{{.Names}}' | grep -qx nginx-proxy-manager; then
    docker start nginx-proxy-manager >/dev/null
    info "already installed"
    return 0
  fi
  mkdir -p /opt/nginx-proxy-manager/data /opt/nginx-proxy-manager/letsencrypt
  docker run -d --name nginx-proxy-manager --restart unless-stopped --network host \
    -v /opt/nginx-proxy-manager/data:/data \
    -v /opt/nginx-proxy-manager/letsencrypt:/etc/letsencrypt \
    jc21/nginx-proxy-manager:latest >/dev/null
}

apply_https() {
  case "$HTTPS_MODE" in
    "") ;;
    1) "$BIN" tls off -config "$CONFIG" -listen :8080 ;;
    2) "$BIN" tls self-signed -config "$CONFIG" ;;
    3) "$BIN" tls files "$CERT" "$KEY" -config "$CONFIG" ;;
    4)
      # Only Caddy talks to LocoStor; plain HTTP is not reachable from outside.
      "$BIN" tls off -config "$CONFIG" -listen 127.0.0.1:8080
      setup_caddy
      ;;
    5)
      "$BIN" tls off -config "$CONFIG" -listen :8080
      setup_npm
      ;;
  esac
}

print_url() {
  IP=$(hostname -I 2>/dev/null | awk '{print $1}')
  IP=${IP:-<container-ip>}
  case "$HTTPS_MODE" in
    4) say "Done. Open https://${DOMAIN:-$IP}" ;;
    5)
      say "Done. LocoStor: http://$IP:8080"
      info "Nginx Proxy Manager: http://$IP:81 - create the admin account there, then add a"
      info "proxy host for your domain pointing to 127.0.0.1:8080 and request a certificate."
      ;;
    *)
      port=$(sed -n 's/.*"listen": *"[^"]*:\([0-9]*\)".*/\1/p' "$CONFIG" 2>/dev/null)
      if grep -q '"tls_cert"' "$CONFIG" 2>/dev/null; then
        [ "$port" = 443 ] && port="" || port=":$port"
        say "Done. Open https://$IP$port"
        [ "$HTTPS_MODE" = 2 ] && info "The certificate is self-signed - confirm the browser warning once."
      else
        say "Done. Open http://$IP:${port:-8080}"
      fi
      ;;
  esac
}

install_container() {
  has apt-get || die "only Debian/Ubuntu (apt) is supported"
  case "$(uname -m)" in
    x86_64) ARCH=amd64 ;;
    aarch64 | arm64) ARCH=arm64 ;;
    *) die "unsupported architecture: $(uname -m)" ;;
  esac

  FRESH=1
  grep -q '"password_hash"' "$CONFIG" 2>/dev/null && FRESH=""

  # --- all questions first, the rest runs unattended ---------------------
  if [ "${LOCOSTOR_HTTPS+set}" = set ]; then
    HTTPS_MODE=$LOCOSTOR_HTTPS DOMAIN=${LOCOSTOR_DOMAIN:-} CERT=${LOCOSTOR_CERT:-} KEY=${LOCOSTOR_KEY:-}
  else
    ask_https "$([ -n "$FRESH" ] && echo fresh || echo existing)"
  fi
  case "$HTTPS_MODE" in "" | 1 | 2 | 3 | 4 | 5) ;; *) die "LOCOSTOR_HTTPS must be 1-5" ;; esac
  [ -z "$DOMAIN" ] || valid_domain "$DOMAIN" || die "invalid domain: $DOMAIN"

  ADMIN_USER="" ADMIN_PW=""
  if [ -n "$FRESH" ]; then
    if [ -n "${LOCOSTOR_PW_STDIN:-}" ]; then
      IFS= read -r ADMIN_USER || true
      IFS= read -r ADMIN_PW || true
    elif tty_ok; then
      ask_credentials
    fi
  fi

  # --- packages ------------------------------------------------------------
  extra=""
  [ "$HTTPS_MODE" = 4 ] && extra="caddy"
  [ "$HTTPS_MODE" = 5 ] && extra="docker.io"
  say "Installing packages (samba, nfs-ganesha, smartmontools${extra:+, $extra})"
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -qq
  # shellcheck disable=SC2086
  apt-get install -y -qq --no-install-recommends \
    samba nfs-ganesha nfs-ganesha-vfs smartmontools curl ca-certificates $extra >/dev/null </dev/null

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
UMask=0077

[Install]
WantedBy=multi-user.target
EOF
  systemctl daemon-reload

  if [ -n "$ADMIN_PW" ]; then
    printf '%s\n' "$ADMIN_PW" | "$BIN" passwd -config "$CONFIG" -user "$ADMIN_USER" >/dev/null 2>&1 ||
      die "could not save the admin login"
    info "admin login saved for user '$ADMIN_USER'"
  elif [ -n "$FRESH" ]; then
    warn "no terminal - a random password is printed to: journalctl -u locostor"
  fi

  apply_https

  systemctl enable --now smbd nfs-ganesha >/dev/null 2>&1 || true
  systemctl enable locostor >/dev/null 2>&1
  systemctl restart locostor

  if [ -z "${LOCOSTOR_HOST:-}" ] && ! ls /dev/sd? /dev/nvme? >/dev/null 2>&1; then
    warn "no disks are visible in this container, so SMART will be empty."
    info "Run the same one-liner on the Proxmox host to pass them through."
  fi
  print_url
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
  main_conf() { sed '/^\[/,$d' "$CONF"; } # without snapshot sections

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
    case "$mp" in /*) ;; *) die "$mp is not an absolute path" ;; esac
    [ -d "$mp" ] || die "$mp does not exist on the host"
  done

  # --- HTTPS (asked now, so container features can change with the rest) --
  RUNNING=""
  pct status "$CT" | grep -q running && RUNNING=1
  FRESH=1
  [ -n "$RUNNING" ] && ct_exec "$CT" grep -q '"password_hash"' "$CONFIG" 2>/dev/null && FRESH=""
  ask_https "$([ -n "$FRESH" ] && echo fresh || echo existing)"
  if [ "$HTTPS_MODE" = 3 ]; then
    # The files are on the host; they are copied into the container.
    HOST_CERT=$CERT HOST_KEY=$KEY
    CERT=/etc/locostor/custom.crt KEY=/etc/locostor/custom.key
  fi

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
  main_conf | grep -q '^lxc.cap.drop: *$' && NEED_CAP=""
  FEATURES=$(main_conf | sed -n 's/^features: *//p')
  NEED_NESTING=""
  if [ "$HTTPS_MODE" = 5 ] && ! printf '%s' "$FEATURES" | grep -q 'nesting=1'; then
    NEED_NESTING=1
  fi

  say "Container $CT ($(main_conf | sed -n 's/^hostname: *//p'))"
  if [ -n "$NEW_MOUNTS$NEW_DEVS$NEED_CAP$NEED_NESTING" ]; then
    for mp in $NEW_MOUNTS; do info "mount  $mp -> $mp"; done
    for dev in $NEW_DEVS; do info "disk   $dev (for RAID status and SMART)"; done
    [ -n "$NEED_CAP" ] && info "allow  raw disk access (CAP_SYS_RAWIO, needed by smartctl)"
    [ -n "$NEED_NESTING" ] && info "enable nesting (needed by Docker for Nginx Proxy Manager)"
    [ -n "$RUNNING" ] && info "The container will be restarted."
    ask "Apply these changes? [Y/n] "
    case "$REPLY" in n* | N*) die "aborted" ;; esac

    if [ -n "$RUNNING" ]; then
      say "Stopping container $CT"
      pct shutdown "$CT" --timeout 60 || pct stop "$CT"
    fi
    for mp in $NEW_MOUNTS; do
      pct set "$CT" "-mp$(next_index mp "$CONF")" "$mp,mp=$mp"
    done
    for dev in $NEW_DEVS; do
      pct set "$CT" "-dev$(next_index dev "$CONF")" "$dev" ||
        die "could not pass $dev through (needs Proxmox VE 8.1 or newer)"
    done
    if [ -n "$NEED_NESTING" ]; then
      pct set "$CT" -features "${FEATURES:+$FEATURES,}nesting=1"
    fi
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

  # --- credentials, then the unattended install inside --------------------
  FRESH=1
  ct_exec "$CT" grep -q '"password_hash"' "$CONFIG" 2>/dev/null && FRESH=""
  ADMIN_USER="" ADMIN_PW=""
  if [ -n "$FRESH" ]; then
    if tty_ok; then
      ask_credentials
    else
      warn "no terminal - a random password is printed to: pct exec $CT -- journalctl -u locostor"
    fi
  fi

  tmp=$(mktemp)
  fetch "$RAW/scripts/install.sh" "$tmp"
  pct push "$CT" "$tmp" /root/locostor-install.sh
  rm -f "$tmp"
  if [ "$HTTPS_MODE" = 3 ]; then
    ct_exec "$CT" mkdir -p /etc/locostor
    pct push "$CT" "$HOST_CERT" "$CERT" --perms 0644
    pct push "$CT" "$HOST_KEY" "$KEY" --perms 0600
  fi

  set -- env LOCOSTOR_HOST=1 LOCOSTOR_HTTPS="$HTTPS_MODE" LOCOSTOR_DOMAIN="$DOMAIN" LOCOSTOR_CERT="$CERT" LOCOSTOR_KEY="$KEY"
  if [ -n "$ADMIN_PW" ]; then
    printf '%s\n%s\n' "$ADMIN_USER" "$ADMIN_PW" | ct_exec "$CT" "$@" LOCOSTOR_PW_STDIN=1 sh /root/locostor-install.sh
  else
    ct_exec "$CT" "$@" sh /root/locostor-install.sh </dev/null
  fi
  ct_exec "$CT" rm -f /root/locostor-install.sh
}

# ---------------------------------------------------------------------------

[ "$(id -u)" -eq 0 ] || die "please run as root"

if has pct && [ -d /etc/pve ]; then
  install_host "$@"
else
  install_container
fi
