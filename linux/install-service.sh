#!/bin/sh
# Installs rbm as a systemd service, so that it mines from boot on.
#
#   sudo linux/install-service.sh           install, enable and start
#   sudo linux/install-service.sh remove    stop and remove again
#
# The service runs rbm from the directory this project lies in, as the user
# who called sudo. Configuration and data stay where they are.
set -eu

UNIT=/etc/systemd/system/rbm.service
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
    systemctl disable --now rbm.service 2>/dev/null || true
    rm -f "$UNIT"
    systemctl daemon-reload
    echo "The rbm service is removed. Configuration and data in $DIR are untouched."
    exit 0
fi

# the user who called sudo, not root
RUN_AS=${SUDO_USER:-}
if [ -z "$RUN_AS" ] || [ "$RUN_AS" = "root" ]; then
    echo "Call this with sudo from the account that is to mine, not as root:" >&2
    echo "rbm downloads and runs miner programs and should not do that as root." >&2
    exit 1
fi
if [ ! -f "$DIR/rbm" ] && [ ! -f "$DIR/rbm.new" ]; then
    echo "No rbm in $DIR. Build it first:  go build -o rbm ./cmd/rbm" >&2
    exit 1
fi
case "$DIR" in
    *" "*|*"%"*)
        echo "The path $DIR holds a space or a percent sign, which a service file cannot carry. Move rbm." >&2
        exit 1
        ;;
esac

chmod +x "$DIR/start.sh"
sed -e "s|RBM_USER|$RUN_AS|g" -e "s|RBM_DIR|$DIR|g" "$HERE/rbm.service" > "$UNIT"
chmod 644 "$UNIT"
systemctl daemon-reload
systemctl enable --now rbm.service

cat <<EOF
rbm runs as a service now, as user $RUN_AS in $DIR, and starts with the machine.

  systemctl status rbm        is it running?
  journalctl -u rbm -f        what it logs
  sudo systemctl restart rbm  start it anew, e.g. after putting a new build as rbm.new
  sudo systemctl stop rbm     stop mining until the next boot
  sudo $0 remove
EOF
