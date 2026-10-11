// Package algo gives mining algorithms one spelling, so that the names used
// by pools and miner programs can be matched.
package algo

import "strings"

// Normalize returns the canonical name of an algorithm: lower case, without
// separators. "KawPoW", "kawpow" and "Kaw-Pow" are the same algorithm.
func Normalize(name string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '-', '_', ' ', '.', '/':
			return -1
		}
		return r
	}, strings.ToLower(strings.TrimSpace(name)))
}
