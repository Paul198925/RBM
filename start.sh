#!/bin/sh
# Starts mining with all devices on what pays best. Stop with Ctrl+C.
# Watch and configure it in the web interface: http://127.0.0.1:4000/
cd "$(dirname "$0")" || exit 1

# a newer build waits next to the running program until the next start
if [ -f rbm.new ]; then
    mv -f rbm.new rbm
fi

if [ ! -f rbm ]; then
    echo "rbm not found. Build it first:  go build -o rbm ./cmd/rbm" >&2
    exit 1
fi
chmod +x rbm

# rbm takes this script's place, so that Ctrl+C and the stop signal of a
# service reach it directly and it can stop its miners
exec ./rbm run "$@"
