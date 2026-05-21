//go:build windows

package winservice

import (
	"context"
	"log"

	"golang.org/x/sys/windows/svc"
)

type handler struct {
	run func(context.Context) error
}

func RunIfNeeded(name string, run func(context.Context) error) (bool, error) {
	interactive, err := svc.IsAnInteractiveSession()
	if err != nil || interactive {
		return false, nil
	}
	return true, svc.Run(name, handler{run: run})
}

func (service handler) Execute(args []string, requests <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	status <- svc.Status{State: svc.StartPending}
	go func() {
		done <- service.run(ctx)
	}()
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}

	for {
		select {
		case request := <-requests:
			switch request.Cmd {
			case svc.Interrogate:
				status <- request.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending}
				cancel()
				if err := <-done; err != nil {
					log.Printf("Sitebrush service stop failed: %v", err)
					return false, 1
				}
				return false, 0
			default:
				status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
			}
		case err := <-done:
			if err != nil {
				log.Printf("Sitebrush service stopped with error: %v", err)
				return false, 1
			}
			return false, 0
		}
	}
}
