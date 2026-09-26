#!/bin/sh
# LocoStor installer - run as root inside a privileged Debian/Ubuntu LXC:
#   curl -fsSL https://raw.githubusercontent.com/Phydran6/LocoStor/main/scripts/install.sh | sh
set -eu

REPO="Phydran6/LocoStor"
BIN=/usr/local/bin/locostor
UNIT=/etc/systemd/system/locostor.service
CONFIG=/etc/locostor/config.json

say() { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
die() { printf '\033[1;31merror:\033[0m %s\n' "$*" >&2; exit 1; }

[ "$(id -u)" -eq 0 ] || die "please run as root"
command -v apt-get >/dev/null || die "only Debian/Ubuntu (apt) is supported"

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
curl -fsSL -o "$TMP/locostor-linux-$ARCH" "$URL/locostor-linux-$ARCH"
curl -fsSL -o "$TMP/SHA256SUMS" "$URL/SHA256SUMS"
(cd "$TMP" && grep " locostor-linux-$ARCH\$" SHA256SUMS | sha256sum -c --quiet -) || die "checksum mismatch"
install -m 0755 "$TMP/locostor-linux-$ARCH" "$BIN"
say "Installed $($BIN version) to $BIN"

# Guests (for shares with "guest ok") are mapped to the guest account.
if [ -f /etc/samba/smb.conf ] && ! grep -qi '^[[:space:]]*map to guest' /etc/samba/smb.conf; then
  sed -i '/^\[global\]/a\   map to guest = bad user' /etc/samba/smb.conf
fi

say "Installing systemd service"
curl -fsSL -o "$UNIT" "https://raw.githubusercontent.com/$REPO/main/deploy/locostor.service"
systemctl daemon-reload

if ! grep -q password_hash "$CONFIG" 2>/dev/null; then
  if [ -r /dev/tty ]; then
    say "Set the admin password for the web UI"
    until "$BIN" passwd -config "$CONFIG" </dev/tty; do :; done
  else
    say "No terminal - an initial password will be printed to: journalctl -u locostor"
  fi
fi

systemctl enable --now smbd nfs-ganesha >/dev/null 2>&1 || true
systemctl enable locostor >/dev/null 2>&1
systemctl restart locostor

IP=$(hostname -I 2>/dev/null | awk '{print $1}')
say "Done. Open http://${IP:-<container-ip>}:8080"
