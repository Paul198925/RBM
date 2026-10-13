package pool

import (
	"context"
	"errors"
	"net/url"
	"sort"
	"time"
)

const zpoolWalletURL = "https://www.zpool.ca/api/walletEx"

// Balance is what a pool holds for a wallet, in the payout currency.
type Balance struct {
	Pool     string `json:"pool"`
	Currency string `json:"currency"`
	// Balance is confirmed and waits for the next payout; Unsold is mined
	// but not yet exchanged into the payout currency.
	Balance float64 `json:"balance"`
	Unsold  float64 `json:"unsold"`
	// Unpaid is both together.
	Unpaid  float64 `json:"unpaid"`
	Paid24h float64 `json:"paid_24h"`
	// Total is everything ever earned with this wallet at the pool.
	Total   float64   `json:"total"`
	Payouts []Payout  `json:"payouts"`
	Updated time.Time `json:"updated"`
}

// Payout is one payment of a pool to the wallet.
type Payout struct {
	Time   time.Time `json:"time"`
	Amount float64   `json:"amount"`
	Tx     string    `json:"tx"`
}

type zpoolWallet struct {
	Currency string    `json:"currency"`
	Unsold   flexFloat `json:"unsold"`
	Balance  flexFloat `json:"balance"`
	Unpaid   flexFloat `json:"unpaid"`
	Paid24h  flexFloat `json:"paid24h"`
	Total    flexFloat `json:"total"`
	// PaidTotal is what the detailed answer calls the total: everything
	// earned, paid out or not. It has no "total".
	PaidTotal flexFloat `json:"paidtotal"`
	Payouts   []struct {
		Time   flexFloat `json:"time"`
		Amount flexFloat `json:"amount"`
		Tx     string    `json:"tx"`
	} `json:"payouts"`
}

// Balance asks zpool what it holds for the configured wallet and what it
// has paid out lately. The pool wants the address in the request; it is its
// own account name there.
func (z *Zpool) Balance(ctx context.Context) (Balance, error) {
	if z.Wallet == "" {
		return Balance{}, errors.New("zpool: no wallet address")
	}
	base := z.WalletURL
	if base == "" {
		base = zpoolWalletURL
	}
	var wallet zpoolWallet
	if err := z.get(ctx, base+"?address="+url.QueryEscape(z.Wallet), &wallet); err != nil {
		return Balance{}, err
	}
	// an address the pool has never seen comes back without a currency
	if wallet.Currency == "" {
		return Balance{}, errors.New("zpool: the pool does not know this wallet yet")
	}

	total := float64(wallet.Total)
	if total == 0 {
		total = float64(wallet.PaidTotal)
	}
	balance := Balance{
		Pool:     z.Name(),
		Currency: wallet.Currency,
		Balance:  float64(wallet.Balance),
		Unsold:   float64(wallet.Unsold),
		Unpaid:   float64(wallet.Unpaid),
		Paid24h:  float64(wallet.Paid24h),
		Total:    total,
		Payouts:  []Payout{},
		Updated:  time.Now(),
	}
	for _, p := range wallet.Payouts {
		balance.Payouts = append(balance.Payouts, Payout{
			Time:   time.Unix(int64(p.Time), 0),
			Amount: float64(p.Amount),
			Tx:     p.Tx,
		})
	}
	sort.Slice(balance.Payouts, func(i, j int) bool { return balance.Payouts[i].Time.After(balance.Payouts[j].Time) })
	return balance, nil
}
