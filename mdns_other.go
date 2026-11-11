//go:build !darwin

package main

import "time"

// browseRaw uses the pure Go mDNS querier on platforms without the macOS DNS-SD API.
func browseRaw(regtype string, timeout time.Duration) (*browseState, error) {
	return queryMDNS(regtype+".local.", timeout)
}
