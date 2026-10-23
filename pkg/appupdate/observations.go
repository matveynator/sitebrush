package appupdate

import (
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/matveynator/netchan/v2"
)

// ObservationContact owns one persistent NetChan session. Each consumer supplies
// its reply contact; a failed session leaves the batch cursor with the consumer.
func ObservationContact(storagePath string, requests <-chan Request, stop <-chan struct{}) {
	var connection *netchan.Channel[wireRequest]
	defer func() {
		if connection != nil {
			_ = connection.Abort()
		}
	}()
	for {
		select {
		case <-stop:
			return
		case request, open := <-requests:
			if !open {
				return
			}
			select {
			case <-stop:
				return
			default:
			}
			var result Response
			var err error
			if request.Action != "public_observations" {
				result.Error = "invalid observation operation"
			} else {
				if connection == nil {
					connection, err = dialObservationContact(storagePath)
				}
				if err == nil {
					err = sendChannel(connection.Send, wireRequest{Protocol: Protocol, Action: request.Action, Detail: request.Detail}, stop, time.Second)
					if err == nil {
						timer := time.NewTimer(5 * time.Second)
						select {
						case message, active := <-connection.Receive:
							if active {
								result, err = decodeContactResult(message)
							} else {
								err = errors.New("observation contact closed")
							}
						case <-connection.Done:
							err = errors.New("observation session disconnected")
						case <-stop:
							timer.Stop()
							return
						case <-timer.C:
							err = errors.New("observation contact timed out")
						}
						timer.Stop()
					}
				}
				if err != nil {
					result.Error = err.Error()
					if connection != nil {
						_ = connection.Abort()
						connection = nil
					}
				}
			}
			if sendChannel(request.Reply, result, stop, time.Second) != nil {
				return
			}
		}
	}
}
func dialObservationContact(storagePath string) (*netchan.Channel[wireRequest], error) {
	store, err := os.OpenRoot(filepath.Join(storagePath, Directory))
	if err != nil {
		return nil, err
	}
	defer store.Close()
	configuration, err := tlsIdentity(store, false)
	if err != nil {
		return nil, err
	}
	return netchan.Dial[wireRequest](Address(storagePath), netchan.Config{Capacity: 4, TLS: configuration})
}
