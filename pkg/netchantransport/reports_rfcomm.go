//go:build !tinygo && !netchan_no_rfcomm

package netchantransport

import (
	"github.com/matveynator/netchan/v2/pkg/transport"
	"github.com/matveynator/netchan/v2/pkg/transport/rfcomm"
)

func appendRFCOMMReports(drivers []transport.Driver, stop <-chan struct{}, reports chan<- string) []transport.Driver {
	return append(drivers, reportedDriver{Driver: rfcomm.Driver{}, stop: stop, reports: reports})
}
