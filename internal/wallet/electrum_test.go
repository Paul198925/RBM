package wallet

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// electrumStub plays an Electrum server: it answers every request with
// what answer returns for its method and first parameter.
func electrumStub(t *testing.T, answer func(method, param string) string) *Reader {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				lines := bufio.NewScanner(conn)
				for lines.Scan() {
					var request struct {
						ID     int    `json:"id"`
						Method string `json:"method"`
						Params []any  `json:"params"`
					}
					if json.Unmarshal(lines.Bytes(), &request) != nil {
						return
					}
					param := ""
					if len(request.Params) > 0 {
						param, _ = request.Params[0].(string)
					}
					io.WriteString(conn, answer(request.Method, param)+"\n")
				}
			}()
		}
	}()
	var dialed []string
	t.Cleanup(func() {
		for _, server := range dialed {
			if server != "electrum.nexa.org:20002" && server != "electrumx.radiant4people.com:50012" {
				t.Errorf("asked %s", server)
			}
		}
	})
	return &Reader{Dial: func(ctx context.Context, server string) (net.Conn, error) {
		dialed = append(dialed, server)
		return (&net.Dialer{}).DialContext(ctx, "tcp", listener.Addr().String())
	}}
}

func TestElectrumBalances(t *testing.T) {
	// the hash of the script that pays to 1BitcoinEaterAddressDontSendf59kuE,
	// as a real server took it
	const eater = "5f923a2ae63c4de79d21fe806f3686850af2e61b99a174119624073139f394c2"
	r := electrumStub(t, func(method, param string) string {
		switch {
		case method == "server.version":
			return `{"jsonrpc":"2.0","result":["Rostrum 14.0.1","1.4"],"id":0}`
		case method == "blockchain.address.get_balance" && param == "nexa:nqtsq5g5test":
			return `{"id":1,"jsonrpc":"2.0","result":{"confirmed":123456789,"unconfirmed":-5000}}`
		case method == "blockchain.scripthash.get_balance" && param == eater:
			return `{"jsonrpc":"2.0","result":{"confirmed":250000000000,"unconfirmed":0},"id":1}`
		case method == "blockchain.address.get_balance":
			return `{"error":{"code":-32603,"message":"(InvalidPrefix(\"nexa\"), Base58(InvalidLength(38)))"},"id":1,"jsonrpc":"2.0"}`
		}
		return `{"jsonrpc":"2.0","error":{"code":-32601,"message":"unknown method"},"id":1}`
	})
	ctx := context.Background()

	// a NEXA has two decimals
	b, err := r.Balance(ctx, "NEXA", "nexa:nqtsq5g5test")
	if err != nil || math.Abs(b.Balance-1234567.89) > 1e-6 || b.Pending != -50 || b.Explorer != "electrum.nexa.org" {
		t.Errorf("NEXA: %+v, %v", b, err)
	}
	b, err = r.Balance(ctx, "RXD", "1BitcoinEaterAddressDontSendf59kuE")
	if err != nil || b.Balance != 2500 || b.Explorer != "electrumx.radiant4people.com" {
		t.Errorf("RXD: %+v, %v", b, err)
	}
	// what the server says is wrong with an address is passed on
	if _, err := r.Balance(ctx, "NEXA", "nexa:wrong"); err == nil || !strings.Contains(err.Error(), "InvalidPrefix") {
		t.Errorf("a wrong NEXA address: %v", err)
	}
	// an address with a typing error does not leave the machine
	for _, wrong := range []string{"1BitcoinEaterAddressDontSendf59kuF", "notanaddress0OIl", "bc1qsegwitaddress"} {
		if _, err := r.Balance(ctx, "RXD", wrong); err == nil {
			t.Errorf("RXD address %q was taken", wrong)
		}
	}
	// a server that is not there: the address stays out of the error
	gone := &Reader{Dial: func(ctx context.Context, server string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", "127.0.0.1:1")
	}}
	if _, err := gone.Balance(ctx, "NEXA", "nexa:nqtsq5g5secret"); err == nil || strings.Contains(err.Error(), "secret") {
		t.Errorf("a server that is down: %v", err)
	}
}

func TestScriptHash(t *testing.T) {
	got, err := scriptHash("1BitcoinEaterAddressDontSendf59kuE")
	if err != nil || got != "5f923a2ae63c4de79d21fe806f3686850af2e61b99a174119624073139f394c2" {
		t.Errorf("pay to key: %s, %v", got, err)
	}
	// an address that starts with ones keeps its leading zero bytes
	if got, err := scriptHash("1111111111111111111114oLvT2"); err != nil || len(got) != 64 {
		t.Errorf("the address of all zeros: %s, %v", got, err)
	}
	// pay to script
	if got, err := scriptHash("3J98t1WpEZ73CNmQviecrnyiWrnqRhWNLy"); err != nil || len(got) != 64 {
		t.Errorf("pay to script: %s, %v", got, err)
	}
}

func TestNodeBalance(t *testing.T) {
	var asked string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		asked = r.Method + " " + string(body)
		if strings.Contains(string(body), "0x000000000000000000000000000000000000dead") {
			io.WriteString(w, `{"jsonrpc":"2.0","id":1,"error":{"code":-32602,"message":"invalid argument 0"}}`)
			return
		}
		io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":"0x2d6e32e508623c0275c"}`)
	}))
	t.Cleanup(srv.Close)
	r := &Reader{Base: srv.URL}
	b, err := r.Balance(context.Background(), "ETHW", "0x000000000000000000000000000000000000dEaD")
	if err != nil || math.Abs(b.Balance-13408.7063) > 0.001 || b.Explorer != "mainnet.ethereumpow.org" {
		t.Errorf("ETHW: %+v, %v", b, err)
	}
	if !strings.HasPrefix(asked, "POST ") || !strings.Contains(asked, `"eth_getBalance"`) || !strings.Contains(asked, `"latest"`) {
		t.Errorf("asked %s", asked)
	}
	// the node's own error is passed on, and what is no address is not sent
	if _, err := r.Balance(context.Background(), "ETHW", "0x000000000000000000000000000000000000dead"); err == nil || !strings.Contains(err.Error(), "invalid argument") {
		t.Errorf("the node's error: %v", err)
	}
	asked = ""
	if _, err := r.Balance(context.Background(), "ETHW", "RNotAnEthAddress"); err == nil || asked != "" {
		t.Errorf("no address: %v, asked %q", err, asked)
	}
}
