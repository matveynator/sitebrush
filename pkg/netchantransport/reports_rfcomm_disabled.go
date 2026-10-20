//go:build tinygo || netchan_no_rfcomm

package netchantransport

import "github.com/matveynator/netchan/v2/pkg/transport"

func appendRFCOMMReports(drivers []transport.Driver, stop <-chan struct{}, reports chan<- string) []transport.Driver {
	return drivers
}
