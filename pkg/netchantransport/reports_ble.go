//go:build !netchan_no_ble

package netchantransport

import (
	"github.com/matveynator/netchan/v2/pkg/transport"
	"github.com/matveynator/netchan/v2/pkg/transport/ble"
)

func appendBLEReports(drivers []transport.Driver, stop <-chan struct{}, reports chan<- string) []transport.Driver {
	if driver, err := ble.NewDriver(stop); err == nil {
		drivers = append(drivers, reportedDriver{Driver: driver, stop: stop, reports: reports})
	}
	return drivers
}
