package billingnetchan

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/matveynator/netchan/v2"
)

func paidEvent() Event {
	return Event{
		SchemaVersion: SchemaVersion, EventID: "evt_0001", InstallationID: "server-a",
		InvoiceNumber: "SB-100", PaymentID: "pi_100", Currency: "EUR",
		AmountMinor: 10000, Status: "paid", OccurredAt: "2026-11-21T21:00:00Z",
	}
}

func TestApplyRequiresAuthenticatedCentralPeerAndMatchingInstallation(t *testing.T) {
	event := paidEvent()
	calls := 0
	apply := func(Event) (bool,error) { calls++; return false,nil }
	if result := Apply(false, "server-a", event, apply); result.Committed || calls != 0 {
		t.Fatalf("untrusted peer changed payment journal: %+v", result)
	}
	if result := Apply(true, "server-b", event, apply); result.Committed || calls != 0 {
		t.Fatalf("wrong installation changed payment journal: %+v", result)
	}
	if result := Apply(true, "server-a", event, apply); !result.Committed || calls != 1 {
		t.Fatalf("verified event wasn't committed: %+v", result)
	}
	event.SchemaVersion++
	if result := Apply(true, "server-a", event, apply); result.Committed { t.Fatal("unknown version accepted") }
	if calls != 1 { t.Fatal("invalid event reached journal") }
}

func TestApplyDeduplicationAndCommitFailure(t *testing.T) {
	event := paidEvent()
	existing := map[string]bool{}
	commit := func(e Event) (bool, error) {
		if existing[e.EventID] { return true,nil }
		existing[e.EventID] = true
		return false,nil
	}
	first := Apply(true,"server-a",event,commit)
	second := Apply(true,"server-a",event,commit)
	if !first.Committed || first.Duplicate || !second.Committed || !second.Duplicate || len(existing)!=1 {
		t.Fatalf("wrong replay handling: first=%+v repeated=%+v",first,second)
	}
	fail := Apply(true,"server-a",event,func(Event)(bool,error){return false,errors.New("database offline")})
	if fail.Committed || !strings.Contains(fail.Error,"journal commit failed") {
		t.Fatalf("failed commit was acknowledged: %+v",fail)
	}
}

func TestNetChanDeliveryAcknowledgementAndDisconnect(t *testing.T) {
	outgoing := make(chan Message)
	done := make(chan struct{})
	connection := &netchan.Channel[Message]{Send:outgoing,Done:done}
	event := paidEvent()
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		request := <-outgoing
		request.Reply <- Receipt{EventID:request.Delivery.EventID,Committed:true}
	}()
	receipt,err:=Deliver(connection,event,nil,time.Second)
	if err!=nil || !receipt.Committed {t.Fatalf("delivery error=%v receipt=%+v",err,receipt)}
	<-finished
	close(done)
	_,err=Deliver(connection,event,nil,time.Second)
	if err==nil {t.Fatal("closed payment channel accepted delivery")}
}

func TestNetChanReceiverAcknowledgesOnlyCommittedEvent(t *testing.T) {
	incoming := make(chan Message)
	done := make(chan struct{})
	stopped := make(chan struct{})
	connection := &netchan.Channel[Message]{Receive:incoming,Done:done}
	finished := make(chan struct{})
	count := 0
	go func(){
		defer close(finished)
		Serve(connection,true,"server-a",func(Event)(bool,error){count++;return false,nil},stopped)
	}()
	reply:=make(chan Receipt)
	session:=make(chan struct{})
	incoming<-Message{Delivery:paidEvent(),Reply:reply,Session:session}
	result:=<-reply
	if !result.Committed || count!=1 { t.Fatalf("no durable acknowledgement: %+v",result) }
	close(session)
	close(stopped)
	<-finished
}

func TestNetChanReceiverRejectsUntrustedSource(t *testing.T) {
	incoming:=make(chan Message)
	done:=make(chan struct{})
	stop:=make(chan struct{})
	connection:=&netchan.Channel[Message]{Receive:incoming,Done:done}
	finished:=make(chan struct{})
	go func(){defer close(finished);Serve(connection,false,"server-a",func(Event)(bool,error){
		t.Error("unauthorized payment reached journal")
		return false,nil
	},stop)}()
	reply:=make(chan Receipt)
	session:=make(chan struct{})
	incoming<-Message{Delivery:paidEvent(),Reply:reply,Session:session}
	if result:=<-reply;result.Committed||result.Error!="untrusted payment source"{t.Fatalf("untrusted event accepted: %+v",result)}
	close(session)
	close(stop)
	<-finished
}

func TestEventValidationRejectsUnsignedClaimsAndInvalidData(t *testing.T) {
	event:=paidEvent()
	for _,edit:=range []func(*Event){
		func(e *Event){e.Currency="usd"},
		func(e *Event){e.AmountMinor=0},
		func(e *Event){e.OccurredAt="2026-11-21T22:00:00+01:00"},
		func(e *Event){e.Status="pending"},
		func(e *Event){e.PaymentID=""},
	} {
		bad:=event
		edit(&bad)
		if err:=bad.Validate("server-a");err==nil {t.Fatalf("invalid event accepted: %+v",bad)}
	}
}
