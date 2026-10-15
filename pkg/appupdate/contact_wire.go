package appupdate

import (
	"encoding/json"
	"errors"
	"time"
)

// Protocol 2's NetChan types are frozen. Domain state and lifecycle results use
// JSON so adding journal fields cannot change the root or session schema hash.
// An incompatible change to these wire types requires a new launcher protocol.
type wireRequest struct {
	Protocol     int
	Action       string
	ManifestHash string
	Detail       string
	Child        chan<- wireChildTask
	Result       []byte
}

type wireChildTask struct {
	Action string
	Reply  chan<- []byte
}

const contactPayloadLimit = 1 << 20

func encodeContactResult(response Response) (wireRequest, error) {
	payload, err := json.Marshal(response)
	if err != nil {
		return wireRequest{}, err
	}
	if len(payload) > contactPayloadLimit {
		return wireRequest{}, errors.New("supervisor result exceeds protocol limit")
	}
	return wireRequest{Protocol: Protocol, Result: payload}, nil
}

func decodeContactResult(message wireRequest) (Response, error) {
	var response Response
	if message.Protocol != Protocol {
		return response, errors.New("unsupported supervisor contact protocol")
	}
	if len(message.Result) == 0 || len(message.Result) > contactPayloadLimit {
		return response, errors.New("invalid supervisor result size")
	}
	if err := json.Unmarshal(message.Result, &response); err != nil {
		return response, err
	}
	if response.Error != "" {
		return response, errors.New(response.Error)
	}
	return response, nil
}

// Each bridge owns its pending result. The parent contact owns the stop channel
// and joins the bridge before releasing the connection or its session slot.
func sendChildWire(tasks <-chan ChildTask, remote chan<- wireChildTask, stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	for {
		select {
		case <-stop:
			return
		case task, open := <-tasks:
			if !open {
				return
			}
			response := exchangeChildWire(remote, task.Action, stop)
			if err := sendChannel(task.Reply, response, stop, time.Second); err != nil {
				return
			}
		}
	}
}

func exchangeChildWire(remote chan<- wireChildTask, action string, stop <-chan struct{}) ChildResult {
	reply := make(chan []byte, 1)
	if err := sendChannel(remote, wireChildTask{Action: action, Reply: reply}, stop, time.Second); err != nil {
		return ChildResult{Error: err.Error()}
	}
	timer := time.NewTimer(40 * time.Second)
	defer timer.Stop()
	select {
	case payload, open := <-reply:
		if !open || len(payload) == 0 || len(payload) > contactPayloadLimit {
			return ChildResult{Error: "invalid lifecycle result size"}
		}
		var result ChildResult
		if err := json.Unmarshal(payload, &result); err != nil {
			return ChildResult{Error: err.Error()}
		}
		return result
	case <-stop:
		return ChildResult{Error: "lifecycle contact stopped"}
	case <-timer.C:
		return ChildResult{Error: "lifecycle result timed out"}
	}
}

func receiveChildWire(session <-chan wireChildTask, tasks chan<- ChildTask, stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	for {
		select {
		case <-stop:
			return
		case task, open := <-session:
			if !open {
				return
			}
			reply := make(chan ChildResult, 1)
			result := ChildResult{}
			if err := sendChannel(tasks, ChildTask{Action: task.Action, Reply: reply}, stop, time.Second); err != nil {
				result.Error = err.Error()
			} else {
				timer := time.NewTimer(40 * time.Second)
				select {
				case received, open := <-reply:
					if !open {
						result.Error = "lifecycle reply closed"
					} else {
						result = received
					}
				case <-stop:
					timer.Stop()
					return
				case <-timer.C:
					result.Error = "lifecycle result timed out"
				}
				timer.Stop()
			}
			payload, err := json.Marshal(result)
			if err != nil || len(payload) > contactPayloadLimit {
				return
			}
			if err := sendChannel(task.Reply, payload, stop, time.Second); err != nil {
				return
			}
		}
	}
}
