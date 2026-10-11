#!/bin/sh
# Starts mining with all devices on what pays best. Stop with Ctrl+C.
# Watch and configure it in the web interface: http://127.0.0.1:4000/
cd "$(dirname "$0")" || exit 1

# go_install_hint prints how Go is installed on this system. It is not done
# from here: that needs root, and which Go to take is the user's choice.
go_install_hint() {
    if command -v apt-get >/dev/null 2>&1; then
        echo "  sudo apt-get install golang-go"
    elif command -v dnf >/dev/null 2>&1; then
        echo "  sudo dnf install golang"
    elif command -v pacman >/dev/null 2>&1; then
        echo "  sudo pacman -S go"
    elif command -v zypper >/dev/null 2>&1; then
        echo "  sudo zypper install go"
    else
        echo "  see https://go.dev/doc/install"
        return
    fi
    echo "A Go from 1.21 on is enough: it fetches the version rbm needs by itself."
    echo "Older ones are too old; take Go from https://go.dev/dl/ then."
}

# build the program from the source code next to this file, if that and Go
# are there
if [ ! -f rbm ] && [ ! -f rbm.new ]; then
    if [ ! -f cmd/rbm/main.go ]; then
        echo "rbm not found, and neither is its source code." >&2
        echo "Get the program from https://github.com/Paul198925/RBM/releases" >&2
        exit 1
    fi
    if ! command -v go >/dev/null 2>&1; then
        {
            echo "rbm not found, and Go is not installed to build it. Install Go with:"
            go_install_hint
            echo "and start this again, or get the program from"
            echo "https://github.com/Paul198925/RBM/releases"
        } >&2
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

# tells rbm that it is started again when it ends to finish an update
export RBM_RESTART=1

while :; do
    # a newer build waits next to the running program until the next start
    if [ -f rbm.new ]; then
        mv -f rbm.new rbm
    fi
    chmod +x rbm
    ./rbm run "$@"
    code=$?
    # 75 is rbm asking to be started again, with the update it has fetched
    if [ "$code" -ne 75 ]; then
        exit "$code"
    fi
done
