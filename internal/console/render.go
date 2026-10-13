// Package console draws the state of the program in the terminal it was
// started in: a dashboard that is redrawn in place, the way RainbowMiner
// shows what it is doing, instead of a log that scrolls by.
package console

import (
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"hashsteer/internal/api"
	"hashsteer/internal/engine"
)

// Snapshot is everything the dashboard shows at one moment.
type Snapshot struct {
	Version string
	Worker  string
	// URL is the address of the web interface, empty if it is turned off.
	URL string
	Now time.Time
	// Waiting says why nothing is mined, empty while the engine runs.
	Waiting    string
	Status     engine.Status
	System     api.System
	Devices    []api.DeviceStatus
	Balances   api.BalanceInfo
	Update     api.UpdateInfo
	Benchmarks []engine.BenchmarkStatus
	Log        []string
	// Keys says the keyboard is read, so the keys are worth showing.
	Keys bool
}

// The colours of the dashboard, as terminals understand them.
const (
	reset  = "\x1b[0m"
	bold   = "\x1b[1m"
	dim    = "\x1b[2m"
	red    = "\x1b[31m"
	green  = "\x1b[32m"
	yellow = "\x1b[33m"
	cyan   = "\x1b[36m"
)

// painter adds colour, or leaves it out where the output is to be plain.
type painter bool

func (p painter) in(code, text string) string {
	if !p || text == "" {
		return text
	}
	return code + text + reset
}

var units = []string{"H/s", "kH/s", "MH/s", "GH/s", "TH/s", "PH/s"}

func hashrate(v float64) string {
	if v <= 0 {
		return "-"
	}
	i := 0
	for v >= 1000 && i < len(units)-1 {
		v /= 1000
		i++
	}
	return fmt.Sprintf("%.2f %s", v, units[i])
}

func watts(v float64) string {
	if v <= 0 {
		return "-"
	}
	return fmt.Sprintf("%.0f W", v)
}

// span writes a duration the short way: 3d 4h, 2h 05m, 12m, 40s.
func span(d time.Duration) string {
	seconds := int(math.Round(d.Seconds()))
	if seconds < 0 {
		seconds = 0
	}
	days, hours, minutes := seconds/86400, seconds%86400/3600, seconds%3600/60
	switch {
	case days > 0:
		return fmt.Sprintf("%dd %dh", days, hours)
	case hours > 0:
		return fmt.Sprintf("%dh %02dm", hours, minutes)
	case minutes > 0:
		return fmt.Sprintf("%dm", minutes)
	}
	return fmt.Sprintf("%ds", seconds)
}

func coins(v float64, currency string) string {
	text := strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.8f", v), "0"), ".")
	return text + " " + currency
}

// table lays rows out in columns. right names the columns that hold
// numbers, which are set flush right.
func table(head []string, right map[int]bool, rows [][]string) []string {
	widths := make([]int, len(head))
	for i, h := range head {
		widths[i] = utf8.RuneCountInString(h)
	}
	for _, row := range rows {
		for i, cell := range row {
			if n := utf8.RuneCountInString(cell); i < len(widths) && n > widths[i] {
				widths[i] = n
			}
		}
	}
	line := func(cells []string) string {
		var b strings.Builder
		for i, cell := range cells {
			if i >= len(widths) {
				break
			}
			pad := strings.Repeat(" ", widths[i]-utf8.RuneCountInString(cell))
			if i > 0 {
				b.WriteString("  ")
			}
			if right[i] {
				b.WriteString(pad + cell)
			} else {
				b.WriteString(cell + pad)
			}
		}
		return strings.TrimRight(b.String(), " ")
	}
	out := []string{line(head)}
	for _, row := range rows {
		out = append(out, line(row))
	}
	return out
}

// benchmarkETA says how many benchmarks are open and roughly how long they
// will take. The groups measure side by side, so the slowest one sets the
// time. It counts the measured time and two minutes of starting each.
func benchmarkETA(benchmarks []engine.BenchmarkStatus, goal float64) (open int, eta time.Duration) {
	const overhead = 120
	perGroup := map[string]float64{}
	for _, b := range benchmarks {
		if b.Status != engine.BenchPending && b.Status != engine.BenchRunning {
			continue
		}
		open++
		seconds := math.Max(0, goal-b.MeasuredSeconds)
		if !(b.Status == engine.BenchRunning && b.MeasuredSeconds > 0) {
			seconds += overhead
		}
		perGroup[b.Group] += seconds
	}
	longest := 0.0
	for _, s := range perGroup {
		longest = math.Max(longest, s)
	}
	return open, time.Duration(longest) * time.Second
}

// Render draws the dashboard as lines of at most width characters, for a
// terminal of the given height. The log gets what room is left.
func Render(s Snapshot, width, height int, colour bool) []string {
	p := painter(colour)
	if width < 40 {
		width = 40
	}
	var lines []string
	add := func(text ...string) { lines = append(lines, text...) }
	section := func(title string) { add("", p.in(bold, title)) }

	// ---- title
	title := "hashsteer " + s.Version
	if s.Worker != "" {
		title += "   worker " + s.Worker
	}
	if !s.System.Started.IsZero() {
		title += "   up " + span(s.Now.Sub(s.System.Started))
	}
	add(p.in(bold+cyan, title))
	if s.URL != "" {
		add(p.in(dim, "web interface: "+s.URL))
	}

	// ---- what the user should know before anything else
	if s.Update.Downloaded != "" {
		add(p.in(yellow, "hashsteer "+s.Update.Downloaded+" is fetched and takes over at the next start."))
	} else if s.Update.Available {
		add(p.in(yellow, "hashsteer "+s.Update.Latest+" is available. Install it in the web interface."))
	}
	for _, notice := range s.System.Notices {
		add(p.in(yellow, notice))
	}
	if s.Waiting != "" {
		add(p.in(red, "Not mining: "+s.Waiting))
	}
	if s.System.Paused || s.Status.Paused {
		add(p.in(yellow, "Mining is paused."))
	}

	// ---- totals
	profit, power := 0.0, 0.0
	for _, g := range s.Status.Groups {
		profit += g.ProfitBTC
		if g.State != "idle" {
			power += g.Power
		}
	}
	total := fmt.Sprintf("Profit %.8f BTC/day", profit)
	if s.Status.BTCPrice > 0 {
		total += fmt.Sprintf(" = %.2f %s/day", profit*s.Status.BTCPrice, s.Status.Currency)
	}
	total += "   Power " + watts(power)
	if s.Status.BTCPrice > 0 {
		total += fmt.Sprintf("   1 BTC = %.0f %s", s.Status.BTCPrice, s.Status.Currency)
	}
	add("", p.in(green, total))

	// ---- miners
	section("RUNNING MINERS")
	var running [][]string
	var idle []string
	for _, g := range s.Status.Groups {
		if g.State == "idle" {
			idle = append(idle, g.Name+": idle - "+g.Reason)
			continue
		}
		coin := g.Coin
		if coin == "" {
			coin = "pool picks"
		}
		earns := fmt.Sprintf("%.8f", g.ProfitBTC)
		for _, c := range g.Candidates {
			if c.ID == g.Running && c.Estimated {
				earns += " est."
			}
		}
		if g.State == "benchmarking" {
			earns = "benchmarking"
		}
		active := ""
		if g.Since != nil {
			active = span(s.Now.Sub(*g.Since))
		}
		running = append(running, []string{g.Name, g.Miner, g.Algorithm, g.Pool, coin, hashrate(g.Hashrate), watts(g.Power), earns,
			fmt.Sprintf("%d/%d", g.Accepted, g.Rejected+g.Invalid), active})
	}
	if len(running) == 0 {
		add(p.in(dim, "none"))
	} else {
		rows := table([]string{"Devices", "Miner", "Algorithm", "Pool", "Coin", "Speed", "Power", "BTC/day", "Acc/Rej", "Active"},
			map[int]bool{5: true, 6: true, 7: true, 8: true, 9: true}, running)
		add(p.in(dim, rows[0]))
		add(rows[1:]...)
	}
	for _, line := range idle {
		add(p.in(dim, line))
	}
	if open, eta := benchmarkETA(s.Benchmarks, s.Status.BenchmarkSeconds); open > 0 && s.Status.BenchmarkSeconds > 0 {
		add(p.in(yellow, fmt.Sprintf("%d benchmarks are open, about %s to go.", open, span(eta))))
	}

	// ---- devices
	// hotAt is the temperature from which a card is marked
	const hotAt = 80
	var devices [][]string
	var hot []bool
	for _, d := range s.Devices {
		if d.Telemetry == nil {
			continue
		}
		t := d.Telemetry
		devices = append(devices, []string{d.ID, d.Model, fmt.Sprintf("%.0f °C", t.TemperatureC), fmt.Sprintf("%.0f %%", t.FanPercent),
			watts(t.PowerWatts), fmt.Sprintf("%.0f %%", t.UtilizationPercent)})
		hot = append(hot, t.TemperatureC >= hotAt)
	}
	if len(devices) > 0 {
		section("DEVICES")
		rows := table([]string{"Name", "Model", "Temp", "Fan", "Power", "Load"}, map[int]bool{2: true, 3: true, 4: true, 5: true}, devices)
		add(p.in(dim, rows[0]))
		for i, row := range rows[1:] {
			if hot[i] {
				row = p.in(yellow, row)
			}
			add(row)
		}
	}

	// ---- balances
	if len(s.Balances.Balances) > 0 {
		section("POOL BALANCES")
		var rows [][]string
		for _, b := range s.Balances.Balances {
			worth := ""
			if b.ValueBTC > 0 && s.Status.BTCPrice > 0 {
				worth = fmt.Sprintf("%.2f %s", b.ValueBTC*s.Status.BTCPrice, s.Status.Currency)
			}
			rows = append(rows, []string{b.Pool, coins(b.Unpaid, b.Currency), worth, coins(b.Balance, b.Currency), coins(b.Paid24h, b.Currency)})
		}
		out := table([]string{"Pool", "Unpaid", "Worth", "Confirmed", "Paid in 24 h"}, map[int]bool{1: true, 2: true, 3: true, 4: true}, rows)
		add(p.in(dim, out[0]))
		add(out[1:]...)
	}

	// ---- what has arrived at the wallets
	if len(s.Balances.Wallets) > 0 {
		section("WALLETS")
		var rows [][]string
		for _, w := range s.Balances.Wallets {
			pending := ""
			if w.Pending != 0 {
				pending = coins(w.Pending, w.Currency)
			}
			worth := ""
			if w.ValueBTC > 0 && s.Status.BTCPrice > 0 {
				worth = fmt.Sprintf("%.2f %s", w.ValueBTC*s.Status.BTCPrice, s.Status.Currency)
			}
			rows = append(rows, []string{w.Currency, short(w.Address), coins(w.Balance, w.Currency), worth, pending})
		}
		out := table([]string{"Coin", "Address", "Balance", "Worth", "Unconfirmed"}, map[int]bool{2: true, 3: true, 4: true}, rows)
		add(p.in(dim, out[0]))
		add(out[1:]...)
	}

	// ---- the keys, at the bottom
	var foot []string
	if s.Keys {
		pause := "[P] pause"
		if s.System.Paused || s.Status.Paused {
			pause = "[P] resume"
		}
		foot = []string{"", p.in(dim, pause+"   [Q] quit   Everything else is in the web interface.")}
	}

	// ---- the log takes what is left; the last line of the window stays
	// empty, or writing it would scroll
	room := height - 1 - len(lines) - len(foot)
	if room >= 5 && len(s.Log) > 0 {
		section("LOG")
		log := s.Log
		if len(log) > room-2 {
			log = log[len(log)-(room-2):]
		}
		add(log...)
	} else if room < 0 {
		if height < 6 {
			foot = nil
		}
		keep := min(max(height-2-len(foot), 0), len(lines))
		lines = append(lines[:keep], p.in(dim, "... the window is too small to show everything"))
	}
	add(foot...)

	// nothing may wrap: a wrapped line would push the picture out of place
	for i, line := range lines {
		lines[i] = clip(line, width-1)
	}
	return lines
}

// short shows an address by its ends: enough to tell wallets apart.
func short(address string) string {
	if utf8.RuneCountInString(address) <= 16 {
		return address
	}
	r := []rune(address)
	return string(r[:7]) + "..." + string(r[len(r)-6:])
}

// clip cuts a line to n visible characters. Colour codes do not count, and
// a line that was cut still ends its colour.
func clip(line string, n int) string {
	if n < 1 {
		n = 1
	}
	visible, inCode, coloured := 0, false, false
	for i, r := range line {
		switch {
		case inCode:
			if r == 'm' {
				inCode = false
			}
		case r == '\x1b':
			inCode, coloured = true, true
		default:
			if visible == n {
				if coloured {
					return line[:i] + reset
				}
				return line[:i]
			}
			visible++
		}
	}
	return line
}
