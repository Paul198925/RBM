# hashsteer

hashsteer mines with the most profitable combination of miner program, algorithm, coin and pool, on NVIDIA graphics cards and CPUs. It measures what each miner program does on your hardware, asks the pools what they pay, and switches when something else earns more. Everything is watched and configured in a web interface. Until version 0.3 it was called rbm.

It is written in Go, as one program without dependencies to install, for Windows and Linux. The idea and the look of the interface come from [RainbowMiner](https://github.com/RainbowMiner/RainbowMiner).

> **State of things.** hashsteer is young. It is developed and run on Windows with two NVIDIA cards at zpool.ca. Much of what is listed below was built from the documentation of the miner programs and pools and has not been run yet; the section [What has been run](#what-has-been-run) says what has.

## What it does

- **Benchmarks by mining.** A miner program is measured while it really mines, for three minutes by default. The figures are kept, so a restart does not measure again.
- **Estimates to start from.** hashsteer comes with hashrates that were measured on other machines. For a card it has figures for, it mines by them at once instead of benchmarking everything first; what is mined is measured, and the measurement replaces the estimate. See [Shared hashrates](#shared-hashrates).
- **Profit switching.** Each round (one minute by default) it compares what every measured combination would earn, less electricity if you set a price, and switches when another one earns enough more. Prices count as an hourly average, and a started miner is left alone for a while, so it does not chase every spike.
- **Device groups.** Cards of the same model mine together with one miner program; different models choose for themselves. The CPU can mine too and is off by default.
- **Watchdog.** A miner that exits or reports no hashrate is restarted. One that keeps failing is taken out for a while. A benchmark that comes to nothing is tried again after six hours and after a day; after the third failure it is given up until the miner program has a new version or you let it back in from the interface. This is remembered across restarts.
- **Memory guard.** A miner whose algorithm needs more memory than is free is not started. This keeps two miners from building a large DAG at the same time.
- **Miner programs are downloaded for you**, and each download is checked against a known SHA-256 before it is unpacked.
- **Updates.** hashsteer looks for newer releases and says so on the dashboard, where one click installs one. See [Updates](#updates).
- **Web interface.** Dashboard, devices, pools and what they pay, balances and payouts, benchmarks, activity of the last days, watchdog, logs, and all settings.

## Pools

| pool | pays | coins |
|---|---|---|
| [zpool.ca](https://zpool.ca) | everything in one coin of your choice | whatever its algorithms mine; single coins can be chosen or left to the pool |
| [2Miners](https://2miners.com) | the coin that is mined | BTG, ERG, ETC, ETHW, NEXA, RVN, XNA, ZEPH (CPU) |
| [WoolyPooly](https://woolypooly.com) | the coin that is mined | ERG, ETC, ETHW, FIRO, MEWC, NEXA, OCTA, RTM (CPU), RVN, RXD, VTC, XNA |

Wallets are kept in one list, one address per coin, and every pool uses the ones it can. 2Miners and WoolyPooly mine the coins that have a wallet; zpool pays to the wallet of its payout coin. **Setup Coins** lists every coin that can be mined as a single coin with what it still needs (a pool, a wallet, a miner program) and what it would earn right now, and has one switch per coin that holds for all pools. The dashboard also shows what has arrived at each wallet: for BTC, DOGE, ERG, ETC, FIRO, LTC, MEWC, RVN and XNA the address is looked up every ten minutes at a public block explorer of the coin. That can be switched off under **Setup Wallets**.

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
| XMRig | 6.26.0 | CPU | 3 | Windows, Linux |

Together they cover what zpool.ca offers, except the algorithms that ASICs or FPGAs mine (sha256, scrypt, x11 and the like), where a graphics card earns next to nothing. Where several programs mine the same algorithm, all are measured and the fastest is used. Most of them are closed source and take a developer fee, which hashsteer counts in.

A miner program is described by a YAML file in [`internal/miner/definitions`](internal/miner/definitions): where to download it, its command line and its algorithms. Adding an algorithm to a program is a few lines there.

## Install

Get the archive for your system from the [releases](https://github.com/Paul198925/hashsteer/releases), or [build it](#build).

The first start creates `config/` and `data/` next to the program. Without a wallet it waits: open <http://127.0.0.1:4000/> and add one under **Setup Wallets**.

### Windows

Unpack the archive and start `start.bat`.

Antivirus programs, Windows Defender among them, treat miner programs as unwanted software and remove them. hashsteer keeps the ones it downloads in `data\bin`; they only run if that folder is exempt from scanning.

### Linux

```sh
tar -xzf hashsteer-v0.4.0-linux-amd64.tar.gz
cd hashsteer-v0.4.0-linux-amd64
sh start.sh
```

To mine from boot on, as a systemd service that runs as your user:

```sh
sudo sh linux/install-service.sh
```

`sudo sh linux/install-service.sh remove` takes the service out again. `journalctl -u hashsteer -f` shows what it logs.

### Needs

The NVIDIA driver, with `nvidia-smi` working. `hashsteer devices` lists what is found. AMD and Intel cards are not supported.

hashsteer says so at the start and in the web interface if the driver is missing. A miner program that cannot start because something is missing on the system, such as the Visual C++ runtime on Windows or a library on Linux, fails its benchmark; the reason and what to install are shown with it on the Benchmarks and Watchdog pages. hashsteer installs nothing on the system by itself.

## Use

```
hashsteer [-config dir] <command>

  run         mine with all devices on what pays best, until Ctrl+C
  devices     list the GPUs and the CPU found on this machine
  pools       list what the enabled pools currently pay
  currencies  list the coins zpool.ca can pay out in
  estimates   print what this machine has measured, to share it
  mine        run one miner on one algorithm, for testing
```

`start.bat` and `start.sh` run `hashsteer run`. In a checkout of this repository they build the program first if it is not there yet. If Go is missing for that, `start.bat` asks whether to install it with winget, and `start.sh` says how to install it.

Every start of a miner program writes a log to `data/logs`; the web interface shows them. A log that grows past 32 MB is cut down to its end, logs older than two weeks are removed, and so are the oldest ones while all of them together take more than 1 GB.

In a terminal, `hashsteer run` draws a dashboard that is redrawn in place, the way RainbowMiner does: what each group of devices mines and earns, the open benchmarks, temperatures, pool balances and the end of the log. **P** pauses and resumes, **Q** or Ctrl+C quits. `hashsteer run -plain` writes the log line by line instead; that is also what happens when the output is not a terminal, as under systemd.

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

### Shared hashrates

Benchmarking every miner program on every algorithm takes hours. To skip most of that, hashsteer carries a list of hashrates measured on other machines, in [`internal/estimate/estimates.yaml`](internal/estimate/estimates.yaml): per card model, miner program and algorithm.

Where the list has a figure for your card, hashsteer compares with it right away. An estimate is only that: the same card is faster or slower with another driver, clock or power limit. So whatever is actually mined is measured as before, and your own measurement takes the estimate's place. What the list does not cover is benchmarked as before. `use_estimates: false` under `switching` in `config.yaml` turns this off.

The list is short so far: an RTX 3060 and an RTX 2080 SUPER. To add your cards, let hashsteer benchmark, run `hashsteer estimates`, and send what it prints as a pull request to that file. It prints card model, miner program, version, algorithm, hashrate and power draw, and nothing about you or your rig.

Rigs can also exchange their hashrates through a server, set under `sharing` in `config.yaml` or on the Setup page:

- `url` is the server's address, `https://hashsteer-share.cylvaro.cloud` unless you set another. Versions up to 0.4.0 use `rbm-share.cylvaro.cloud`, which is the same server. Empty exchanges nothing. Like every server it sees the address a request comes from; it does not store it.
- `download: true` (the default) fetches twice a day what other rigs measured. It wins over the list that comes with hashsteer, and counts from the next start of the miners.
- `upload: true` sends what this rig measured: card model, miner program with version, algorithm, hashrate and power draw, under a random number the rig makes up for itself. It is off unless you turn it on.

The page **Shared Hashrates** in the web interface lists these figures, with the number of rigs behind each and, next to it, what your own rig measured. The same list is public at <https://hashsteer-share.cylvaro.cloud/>, to look up what a card does before you have one.

The server hands out the median per card, miner program and algorithm, so that one rig with a wrong figure moves it little. It is not part of this repository. What hashsteer sends to it and takes from it is in [`internal/share`](internal/share); whatever comes from the server is checked the same way as what a rig may send.

### Updates

A few times a day hashsteer asks GitHub whether there is a newer release, and shows it on the dashboard. **Update now** fetches the release, checks it against the SHA-256 GitHub lists for the file, stops the miners and restarts with the new version. That restart needs `start.bat`, `start.sh` or the systemd service; started any other way, the new version waits for the next start.

Both can be changed under `updates` in `config.yaml` or on the Setup page:

- `check: false` stops hashsteer from asking GitHub
- `auto: true` installs a new release without asking. It is off by default: with it, hashsteer runs whatever gets published as a release of this project. The checksum guards against a broken download, not against a release that should not exist.

### The web interface from another machine

It listens on `127.0.0.1:4000`, which is only reachable from the machine itself. For a rig without a screen:

- an SSH tunnel needs no change: `ssh -L 4000:127.0.0.1:4000 user@rig`, then open <http://127.0.0.1:4000/>
- or set `api.listen` (for example `0.0.0.0:4000`) and `api.password` in `config/config.yaml`. Without a password hashsteer answers this machine only. The connection is not encrypted: use it on a network you trust.

## What has been run

On Windows 11 with an RTX 3060 and an RTX 2080 SUPER, at zpool.ca:

- Rigel, WildRig Multi and ccminer-tpfuemp mine, their statistics are read correctly and shares are accepted
- benchmarks, switching, the watchdog and the web interface

Built from documentation and tested only with made-up data so far:

- the other miner programs, and the algorithms added to the three above later
- 2Miners and WoolyPooly, including their balances and payouts
- mining single coins at zpool, and the balance it reports
- everything on Linux: the program compiles and its tests pass there, but it has not mined on a Linux machine

A miner program or algorithm that does not work fails its benchmark, waits, and is given up after the third failure; the rest goes on.

## Build

Go 1.27 or newer, nothing else:

```sh
go build -o hashsteer ./cmd/hashsteer        # Linux
go build -o hashsteer.exe ./cmd/hashsteer    # Windows
```

For the other system set `GOOS` (`linux` or `windows`) and `CGO_ENABLED=0`. `go test ./...` runs the tests; they need no hardware and no network.

To look at the interface without mining, with made-up data:

```sh
HASHSTEER_UI_DEMO=127.0.0.1:4011 go test ./internal/api -run TestDemoServer -timeout 0 -v
```

## A word of caution

Mining uses a lot of electricity and wears hardware; whether it pays depends on your power price. hashsteer cannot check a wallet address: a wrong one sends what you mine to someone else. The miner programs are other people's software, mostly closed source, run with your user's rights.

## License

hashsteer is under the [MIT License](LICENSE). The miner programs it downloads are not part of it; each has its own license and terms.
