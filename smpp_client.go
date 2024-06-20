package smpp

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/fiorix/go-smpp/smpp"
	"github.com/fiorix/go-smpp/smpp/pdu"
	"github.com/fiorix/go-smpp/smpp/pdu/pdufield"
	"github.com/fiorix/go-smpp/smpp/pdu/pdutext"
	"github.com/fiorix/go-smpp/smpp/pdu/pdutlv"
)

type SMPPClient interface {
	Bind(transmitterAddr string, receiverAddr string, systemID string, systemType string, password string) error
	SubmitMT(destinationMSISDN string, message string, tlvs map[pdutlv.Tag]interface{}) (string, error)
	AwaitDRs(messageID string, targetState string) (bool, []string, error)
}

type SMPPClientImpl struct {
	transmitter              *smpp.Transmitter
	receiver                 *smpp.Receiver
	deliverSMChannel         chan pdu.Body
	DRChannelMapLock         sync.Mutex
	DRChannelMap             map[string]chan string
	DRChannelMapCleanChannel chan string
}

func (s *SMPPClientImpl) Bind(transmitterAddr string, receiverAddr string, systemID string, systemType string, password string) error {
	transmitter := &smpp.Transmitter{
		Addr:               transmitterAddr,
		User:               systemID,
		Passwd:             password,
		SystemType:         systemType,
		EnquireLink:        10 * time.Second,
		EnquireLinkTimeout: 30 * time.Second,
		RespTimeout:        10 * time.Second,
		WindowSize:         5000,
	}
	s.deliverSMChannel = make(chan pdu.Body, 10000)
	receiver := &smpp.Receiver{
		Addr:               receiverAddr,
		User:               systemID,
		Passwd:             password,
		SystemType:         systemType,
		EnquireLink:        10 * time.Second,
		EnquireLinkTimeout: 30 * time.Second,
		Handler: func(p pdu.Body) {
			if p.Header().ID == pdu.DeliverSMID {
				s.deliverSMChannel <- p
			}
		},
	}
	if err := bind(transmitter.Bind()); err != nil {
		return err
	}
	if err := bind(receiver.Bind()); err != nil {
		return err
	}
	s.transmitter = transmitter
	s.receiver = receiver
	s.DRChannelMapLock = sync.Mutex{}
	s.DRChannelMap = make(map[string]chan string)
	go s.processDeliverSM()
	return nil
}

func (s *SMPPClientImpl) SubmitMT(destinationMSISDN string, message string, tlvs map[pdutlv.Tag]interface{}) (string, error) {
	shortMessage := smpp.ShortMessage{
		Dst:       destinationMSISDN,
		Text:      pdutext.Raw(message),
		TLVFields: tlvs,
	}
	resp, err := s.transmitter.Submit(&shortMessage)
	if err != nil {
		return "", err
	}
	if resp.Resp().Header().Status != 0 {
		return "", fmt.Errorf("submit failed: %s", resp.Resp().Header().Status)
	}
	messageID := resp.Resp().Fields()[pdufield.MessageID].String()
	return messageID, nil
}

func (s *SMPPClientImpl) AwaitDRs(messageID string, targetState string) (bool, []string, error) {
	seenStates := []string{}
	messageStateChannel := s.getDRChannel(messageID)
	for {
		select {
		case messageState, ok := <-messageStateChannel:
			if !ok {
				return false, seenStates, fmt.Errorf("state channel closed for message %s", messageID)
			}
			seenStates = append(seenStates, messageState)
			if messageState == targetState {
				return true, seenStates, nil
			}
		case <-time.After(10 * time.Second):
			return false, seenStates, nil
		}
	}
}

func (s *SMPPClientImpl) processDeliverSM() {
	for {
		select {
		case deliverSM := <-s.deliverSMChannel:
			messageID := deliverSM.Fields()[pdufield.ShortMessage].String()
			messageID = strings.Split(strings.Split(messageID, "id:")[1], " ")[0]
			stat := strings.Split(strings.Split(deliverSM.Fields()[pdufield.ShortMessage].String(), "stat:")[1], " ")[0]
			s.getDRChannel(messageID) <- stat
		case messageID := <-s.DRChannelMapCleanChannel:
			s.DRChannelMapLock.Lock()
			statesChannel, ok := s.DRChannelMap[messageID]
			if ok {
				close(statesChannel)
				delete(s.DRChannelMap, messageID)
			}
			s.DRChannelMapLock.Unlock()
		}
	}
}

func (s *SMPPClientImpl) getDRChannel(messageID string) chan string {
	s.DRChannelMapLock.Lock()
	defer s.DRChannelMapLock.Unlock()
	if _, ok := s.DRChannelMap[messageID]; !ok {
		go func() {
			time.Sleep(1 * time.Minute)
			s.DRChannelMapCleanChannel <- messageID
		}()
		s.DRChannelMap[messageID] = make(chan string, 10)
	}
	return s.DRChannelMap[messageID]
}

func bind(connStatusChan <-chan smpp.ConnStatus) error {
	if status := <-connStatusChan; status.Error() != nil {
		return status.Error()
	}
	return nil
}
