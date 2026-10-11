#!/bin/sh
# Installs hashsteer as a systemd service, so that it mines from boot on.
#
#   sudo linux/install-service.sh           install, enable and start
#   sudo linux/install-service.sh remove    stop and remove again
#
# The service runs hashsteer from the directory this project lies in, as the user
# who called sudo. Configuration and data stay where they are.
set -eu

UNIT=/etc/systemd/system/hashsteer.service
HERE=$(cd "$(dirname "$0")" && pwd)
DIR=$(dirname "$HERE")

if [ "$(id -u)" -ne 0 ]; then
    echo "This changes system services: run it with sudo." >&2
    exit 1
fi
if ! command -v systemctl >/dev/null 2>&1; then
    echo "systemctl not found: this system does not use systemd." >&2
    exit 1
fi

if [ "${1:-install}" = "remove" ]; then
    systemctl disable --now hashsteer.service 2>/dev/null || true
    rm -f "$UNIT"
    systemctl daemon-reload
    echo "The hashsteer service is removed. Configuration and data in $DIR are untouched."
    exit 0
fi

# the user who called sudo, not root
RUN_AS=${SUDO_USER:-}
if [ -z "$RUN_AS" ] || [ "$RUN_AS" = "root" ]; then
    echo "Call this with sudo from the account that is to mine, not as root:" >&2
    echo "hashsteer downloads and runs miner programs and should not do that as root." >&2
    exit 1
fi
if [ ! -f "$DIR/hashsteer" ] && [ ! -f "$DIR/hashsteer.new" ]; then
    echo "No hashsteer in $DIR. Build it first:  go build -o hashsteer ./cmd/hashsteer" >&2
    exit 1
fi
case "$DIR" in
    *" "*|*"%"*)
        echo "The path $DIR holds a space or a percent sign, which a service file cannot carry. Move hashsteer." >&2
        exit 1
        ;;
esac

chmod +x "$DIR/start.sh"
sed -e "s|HASHSTEER_USER|$RUN_AS|g" -e "s|HASHSTEER_DIR|$DIR|g" "$HERE/hashsteer.service" > "$UNIT"
chmod 644 "$UNIT"
systemctl daemon-reload
systemctl enable --now hashsteer.service

cat <<EOF
hashsteer runs as a service now, as user $RUN_AS in $DIR, and starts with the machine.

  systemctl status hashsteer        is it running?
  journalctl -u hashsteer -f        what it logs
  sudo systemctl restart hashsteer  start it anew, e.g. after putting a new build as hashsteer.new
  sudo systemctl stop hashsteer     stop mining until the next boot
  sudo $0 remove
EOF
