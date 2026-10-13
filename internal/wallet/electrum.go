package wallet

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"strings"
	"time"
)

// Some coins have no explorer that answers over HTTP without a key. Their
// balances are asked for where their wallets ask: at a public Electrum
// server, one line of JSON each way over TLS, or at a public node of the
// network.

// electrumBalance asks an Electrum server for the balance of an address.
// server is "host:port". Servers of Bitcoin's kind do not take an address
// but the hash of the script that pays to it: byScript says so.
func (r *Reader) electrumBalance(ctx context.Context, server, address string, byScript bool) (confirmed, pending float64, err error) {
	method, param := "blockchain.address.get_balance", address
	if byScript {
		method = "blockchain.scripthash.get_balance"
		if param, err = scriptHash(address); err != nil {
			return 0, 0, err
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	conn, err := r.dial(ctx, server)
	if err != nil {
		return 0, 0, err
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		conn.SetDeadline(deadline)
	}

	call := func(id int, method string, params []any, into any) error {
		request, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
		if err != nil {
			return err
		}
		if _, err := conn.Write(append(request, '\n')); err != nil {
			return err
		}
		line, err := bufio.NewReaderSize(conn, 64<<10).ReadBytes('\n')
		if err != nil {
			return err
		}
		var reply struct {
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if err := json.Unmarshal(line, &reply); err != nil {
			return err
		}
		if len(reply.Error) > 0 && string(reply.Error) != "null" {
			text := string(reply.Error)
			var named struct {
				Message string `json:"message"`
			}
			if json.Unmarshal(reply.Error, &named) == nil && named.Message != "" {
				text = named.Message
			}
			if len(text) > 160 {
				text = text[:160]
			}
			return errors.New(text)
		}
		if into == nil {
			return nil
		}
		return json.Unmarshal(reply.Result, into)
	}
	// a server may want to be told who asks before it answers
	if err := call(0, "server.version", []any{"hashsteer", "1.4"}, nil); err != nil {
		return 0, 0, err
	}
	var balance struct {
		Confirmed   *amount `json:"confirmed"`
		Unconfirmed amount  `json:"unconfirmed"`
	}
	if err := call(1, method, []any{param}, &balance); err != nil {
		return 0, 0, err
	}
	if balance.Confirmed == nil {
		return 0, 0, errors.New("the answer holds no balance")
	}
	return balance.Confirmed.value, balance.Unconfirmed.value, nil
}

// dial connects to an Electrum server, with TLS and its certificate checked.
func (r *Reader) dial(ctx context.Context, server string) (net.Conn, error) {
	if r.Dial != nil {
		return r.Dial(ctx, server)
	}
	dialer := &tls.Dialer{NetDialer: &net.Dialer{Timeout: 10 * time.Second}}
	return dialer.DialContext(ctx, "tcp", server)
}

// nodeBalance asks a node of an Ethereum-like network for the balance of
// an address, in the network's smallest unit.
func (r *Reader) nodeBalance(ctx context.Context, node, address string) (float64, error) {
	if !strings.HasPrefix(address, "0x") || len(address) != 42 {
		return 0, errors.New("this does not look like an address of the network")
	}
	request, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "eth_getBalance", "params": []any{address, "latest"}})
	if err != nil {
		return 0, err
	}
	var reply struct {
		Result *string `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := r.send(ctx, http.MethodPost, node, bytes.NewReader(request), &reply); err != nil {
		return 0, err
	}
	if reply.Error != nil {
		return 0, errors.New(reply.Error.Message)
	}
	if reply.Result == nil {
		return 0, errors.New("the answer holds no balance")
	}
	wei, ok := new(big.Int).SetString(strings.TrimPrefix(*reply.Result, "0x"), 16)
	if !ok {
		return 0, fmt.Errorf("%q is not an amount", *reply.Result)
	}
	value, _ := new(big.Float).SetInt(wei).Float64()
	return value, nil
}

const base58Alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

// scriptHash turns an address of Bitcoin's old kind into what an Electrum
// server knows it by: the hash of the script that pays to it, back to
// front. Addresses that start with 1 pay to a key, those with 3 to a script.
func scriptHash(address string) (string, error) {
	n := new(big.Int)
	for _, c := range address {
		digit := strings.IndexRune(base58Alphabet, c)
		if digit < 0 {
			return "", errors.New("this does not look like an address of the coin")
		}
		n.Mul(n, big.NewInt(58)).Add(n, big.NewInt(int64(digit)))
	}
	raw := n.Bytes()
	// leading ones stand for leading zero bytes
	for _, c := range address {
		if c != '1' {
			break
		}
		raw = append([]byte{0}, raw...)
	}
	if len(raw) != 25 {
		return "", errors.New("this does not look like an address of the coin")
	}
	first := sha256.Sum256(raw[:21])
	check := sha256.Sum256(first[:])
	if !bytes.Equal(check[:4], raw[21:]) {
		return "", errors.New("the address has a typing error: its checksum is wrong")
	}
	var script []byte
	switch raw[0] {
	case 0x00: // pay to the hash of a key
		script = append(append([]byte{0x76, 0xa9, 0x14}, raw[1:21]...), 0x88, 0xac)
	case 0x05: // pay to the hash of a script
		script = append(append([]byte{0xa9, 0x14}, raw[1:21]...), 0x87)
	default:
		return "", errors.New("this kind of address is not known here")
	}
	hash := sha256.Sum256(script)
	for i, j := 0, len(hash)-1; i < j; i, j = i+1, j-1 {
		hash[i], hash[j] = hash[j], hash[i]
	}
	return hex.EncodeToString(hash[:]), nil
}
