#!/bin/sh
# Starts mining with all devices on what pays best. Stop with Ctrl+C.
# Watch and configure it in the web interface: http://127.0.0.1:4000/
cd "$(dirname "$0")" || exit 1

# a newer build waits next to the running program until the next start
if [ -f rbm.new ]; then
    mv -f rbm.new rbm
fi

# build the program from the source code next to this file, if that and Go
# are there
if [ ! -f rbm ]; then
    if [ ! -f cmd/rbm/main.go ]; then
        echo "rbm not found, and neither is its source code." >&2
        echo "Get the program from https://github.com/Paul198925/RBM/releases" >&2
        exit 1
    fi
    if ! command -v go >/dev/null 2>&1; then
        echo "rbm not found, and Go is not installed to build it." >&2
        echo "Install Go from https://go.dev/dl/ or get the program from" >&2
        echo "https://github.com/Paul198925/RBM/releases" >&2
        exit 1
    fi
    echo "rbm not found. Building it, this takes a minute the first time ..."
    if ! CGO_ENABLED=0 go build -o rbm ./cmd/rbm; then
        echo "The build failed." >&2
        rm -f rbm
        exit 1
    fi
    echo "Built rbm."
fi
chmod +x rbm

# rbm takes this script's place, so that Ctrl+C and the stop signal of a
# service reach it directly and it can stop its miners
exec ./rbm run "$@"
