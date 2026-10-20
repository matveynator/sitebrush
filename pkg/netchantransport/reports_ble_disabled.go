//go:build netchan_no_ble

package netchantransport

import "github.com/matveynator/netchan/v2/pkg/transport"

func appendBLEReports(drivers []transport.Driver, stop <-chan struct{}, reports chan<- string) []transport.Driver {
	return drivers
}
