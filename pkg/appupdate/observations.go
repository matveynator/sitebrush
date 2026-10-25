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
	publicContact(storagePath, requests, stop, false)
}

// SecurityContact is separate from observations so delivery acknowledgements
// and their reconnects cannot occupy the security consumer's session.
func SecurityContact(storagePath string, requests <-chan Request, stop <-chan struct{}) {
	publicContact(storagePath, requests, stop, true)
}

func NewSecurityContact(storagePath string, stop <-chan struct{}) func(Request) (Response, error) {
	requests := make(chan Request, 128)
	go SecurityContact(storagePath, requests, stop)
	return func(request Request) (Response, error) {
		reply := make(chan Response, 1)
		request.Reply = reply
		select {
		case <-stop:
			return Response{}, errors.New("security contact stopped")
		default:
		}
		select {
		case requests <- request:
		case <-stop:
			return Response{}, errors.New("security contact stopped")
		default:
			return Response{}, errors.New("security contact is busy")
		}
		select {
		case result := <-reply:
			if result.Error != "" {
				return result, errors.New(result.Error)
			}
			return result, nil
		case <-stop:
			return Response{}, errors.New("security contact stopped")
		}
	}
}

func publicContact(storagePath string, requests <-chan Request, stop <-chan struct{}, security bool) {
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
			allowed := !security && request.Action == "public_observations" || security && (request.Action == "public_security" || request.Action == "public_throttle")
			if !allowed {
				result.Error = "invalid public contact operation"
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
