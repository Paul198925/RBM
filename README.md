# rbm

rbm mines with the most profitable combination of miner program, algorithm, coin and pool, on NVIDIA graphics cards and CPUs. It measures what each miner program does on your hardware, asks the pools what they pay, and switches when something else earns more. Everything is watched and configured in a web interface.

It is written in Go, as one program without dependencies to install, for Windows and Linux. The idea and the look of the interface come from [RainbowMiner](https://github.com/RainbowMiner/RainbowMiner).

> **State of things.** rbm is young. It is developed and run on Windows with two NVIDIA cards at zpool.ca. Much of what is listed below was built from the documentation of the miner programs and pools and has not been run yet; the section [What has been run](#what-has-been-run) says what has.

## What it does

- **Benchmarks by mining.** A miner program is measured while it really mines, for three minutes by default. The figures are kept, so a restart does not measure again.
- **Profit switching.** Each round (one minute by default) it compares what every measured combination would earn, less electricity if you set a price, and switches when another one earns enough more. Prices count as an hourly average, and a started miner is left alone for a while, so it does not chase every spike.
- **Device groups.** Cards of the same model mine together with one miner program; different models choose for themselves. The CPU can mine too and is off by default.
- **Watchdog.** A miner that exits or reports no hashrate is restarted. One that keeps failing is taken out for a while, and can be let back in from the interface.
- **Memory guard.** A miner whose algorithm needs more memory than is free is not started. This keeps two miners from building a large DAG at the same time.
- **Miner programs are downloaded for you**, and each download is checked against a known SHA-256 before it is unpacked.
- **Web interface.** Dashboard, devices, pools and what they pay, balances and payouts, benchmarks, activity of the last days, watchdog, logs, and all settings.

## Pools

| pool | pays | coins |
|---|---|---|
| [zpool.ca](https://zpool.ca) | everything in one coin of your choice | whatever its algorithms mine; single coins can be chosen or left to the pool |
| [2Miners](https://2miners.com) | the coin that is mined | ERG, ETC, ETHW, NEXA, RVN, XNA |
| [WoolyPooly](https://woolypooly.com) | the coin that is mined | ERG, ETC, ETHW, FIRO, MEWC, NEXA, RVN, RXD, XNA |

Wallets are kept in one list, one address per coin, and every pool uses the ones it can. 2Miners and WoolyPooly mine the coins that have a wallet; zpool pays to the wallet of its payout coin.

zpool reports what it pays itself. For the other two the figure is worked out from the coin's price (from CoinGecko), its block reward and the hashrate of its network, less the pool's fee. That is an expectation; what arrives depends on the pool's luck.

## Miner programs

| program | version | for | algorithms | systems |
|---|---|---|---|---|
| BzMiner | 100.45 | NVIDIA | 4 | Windows, Linux |
| ccminer-tpfuemp | 2026.09.2 | NVIDIA | 54 | Windows, Linux |
| cpuminer-opt | 26.1 | CPU | 23 | Windows |
| GMiner | 3.44 | NVIDIA | 7 | Windows |
| lolMiner | 1.98a | NVIDIA | 4 | Windows, Linux |
| Rigel | 1.23.2 | NVIDIA | 6 | Windows, Linux |
| SRBMiner-Multi | 3.7.5 | NVIDIA | 17 | Windows, Linux |
| SRBMiner-Multi | 3.7.5 | CPU | 21 | Windows, Linux |
| T-Rex | 0.26.8 | NVIDIA | 5 | Windows, Linux |
| T-Rex | 0.19.14 | NVIDIA | 9 | Windows, Linux |
| WildRig Multi | 0.51.4 | NVIDIA | 15 | Windows, Linux |
| XMRig | 6.26.0 | CPU | 2 | Windows, Linux |

Together they cover what zpool.ca offers, except the algorithms that ASICs or FPGAs mine (sha256, scrypt, x11 and the like), where a graphics card earns next to nothing. Where several programs mine the same algorithm, all are measured and the fastest is used. Most of them are closed source and take a developer fee, which rbm counts in.

A miner program is described by a YAML file in [`internal/miner/definitions`](internal/miner/definitions): where to download it, its command line and its algorithms. Adding an algorithm to a program is a few lines there.

## Install

Get the archive for your system from the [releases](https://github.com/Paul198925/RBM/releases), or [build it](#build).

The first start creates `config/` and `data/` next to the program. Without a wallet it waits: open <http://127.0.0.1:4000/> and add one under **Setup Wallets**.

### Windows

Unpack the archive and start `start.bat`.

Antivirus programs, Windows Defender among them, treat miner programs as unwanted software and remove them. rbm keeps the ones it downloads in `data\bin`; they only run if that folder is exempt from scanning.

### Linux

```sh
tar -xzf rbm-v0.2.0-linux-amd64.tar.gz
cd rbm-v0.2.0-linux-amd64
sh start.sh
```

To mine from boot on, as a systemd service that runs as your user:

```sh
sudo sh linux/install-service.sh
```

`sudo sh linux/install-service.sh remove` takes the service out again. `journalctl -u rbm -f` shows what it logs.

### Needs

The NVIDIA driver, with `nvidia-smi` working. `rbm devices` lists what is found. AMD and Intel cards are not supported.

## Use

```
rbm [-config dir] <command>

  run         mine with all devices on what pays best, until Ctrl+C
  devices     list the GPUs and the CPU found on this machine
  pools       list what the enabled pools currently pay
  currencies  list the coins zpool.ca can pay out in
  mine        run one miner on one algorithm, for testing
```

`start.bat` and `start.sh` run `rbm run`.

### Configuration

The configuration is a directory of YAML files, `config` by default. The web interface edits them and explains every setting; they can be edited by hand as well.

| file | holds |
|---|---|
| `config.yaml` | rig name, electricity price, switching, CPU mining, memory reserves, web interface |
| `pools.yaml` | the wallets, and per pool what may be mined there |
| `miners.yaml` | which miner programs are used, for which algorithms, with which extra arguments |
| `algorithms.yaml` | which algorithms may be mined at all |
| `devices.yaml` | which devices stay out of mining |

`data/` holds the miner programs, their logs and the database with benchmarks and activity.

### The web interface from another machine

It listens on `127.0.0.1:4000`, which is only reachable from the machine itself. For a rig without a screen:

- an SSH tunnel needs no change: `ssh -L 4000:127.0.0.1:4000 user@rig`, then open <http://127.0.0.1:4000/>
- or set `api.listen` (for example `0.0.0.0:4000`) and `api.password` in `config/config.yaml`. Without a password rbm answers this machine only. The connection is not encrypted: use it on a network you trust.

## What has been run

On Windows 11 with an RTX 3060 and an RTX 2080 SUPER, at zpool.ca:

- Rigel, WildRig Multi and ccminer-tpfuemp mine, their statistics are read correctly and shares are accepted
- benchmarks, switching, the watchdog and the web interface

Built from documentation and tested only with made-up data so far:

- the other miner programs, and the algorithms added to the three above later
- 2Miners and WoolyPooly, including their balances and payouts
- mining single coins at zpool, and the balance it reports
- everything on Linux: the program compiles and its tests pass there, but it has not mined on a Linux machine

A miner program or algorithm that does not work fails its benchmark and is taken out for six hours; the rest goes on.

## Build

Go 1.27 or newer, nothing else:

```sh
go build -o rbm ./cmd/rbm        # Linux
go build -o rbm.exe ./cmd/rbm    # Windows
```

For the other system set `GOOS` (`linux` or `windows`) and `CGO_ENABLED=0`. `go test ./...` runs the tests; they need no hardware and no network.

To look at the interface without mining, with made-up data:

```sh
RBM_UI_DEMO=127.0.0.1:4011 go test ./internal/api -run TestDemoServer -timeout 0 -v
```

## A word of caution

Mining uses a lot of electricity and wears hardware; whether it pays depends on your power price. rbm cannot check a wallet address: a wrong one sends what you mine to someone else. The miner programs are other people's software, mostly closed source, run with your user's rights.

## License

rbm is under the [MIT License](LICENSE). The miner programs it downloads are not part of it; each has its own license and terms.
