package smpp

import (
	"fmt"
	"regexp"
	"sync"
	"time"

	"math/rand"

	"github.com/fiorix/go-smpp/smpp"
	"github.com/fiorix/go-smpp/smpp/pdu"
	"github.com/fiorix/go-smpp/smpp/pdu/pdufield"
	"github.com/fiorix/go-smpp/smpp/pdu/pdutext"
	"github.com/fiorix/go-smpp/smpp/pdu/pdutlv"
)

var (
	drShortMessageRegex = regexp.MustCompile(`(\w+):\s*([^\s]+)`)
)

type SMPPClient interface {
	Bind(transmitterAddrs []string, receiverAddrs []string, connsPerTarget int, systemID string, systemType string, password string) error
	SubmitMT(destinationMSISDN string, message string, tlvs map[pdutlv.Tag]interface{}) (string, error)
	AwaitDRs(messageID string, targetState string) (bool, []string, error)
}

type SMPPClientImpl struct {
	transmitters             []*smpp.Transmitter
	receivers                []*smpp.Receiver
	deliverSMChannel         chan pdu.Body
	DRChannelMapLock         sync.Mutex
	DRChannelMap             map[string]chan string
	DRChannelMapCleanChannel chan string
}

func (s *SMPPClientImpl) bindTransmitters(transmitterAddrs []string, connsPerTarget int, systemID string, systemType string, password string) error {
	s.transmitters = []*smpp.Transmitter{}

	transmitterChannels := make(chan *smpp.Transmitter, len(transmitterAddrs)*connsPerTarget)
	defer close(transmitterChannels)
	errorChannels := make(chan error, len(transmitterAddrs)*connsPerTarget)
	defer close(errorChannels)

	for _, transmitterAddr := range transmitterAddrs {
		for i := 0; i < connsPerTarget; i++ {
			go func() {
				transmitter := &smpp.Transmitter{
					Addr:               transmitterAddr,
					User:               systemID,
					Passwd:             password,
					SystemType:         systemType,
					EnquireLink:        10 * time.Second,
					EnquireLinkTimeout: 30 * time.Second,
				}
				var err error = nil
				retryCnt := 0
				for (err != nil || retryCnt == 0) && retryCnt < 10 {
					err = bind(transmitter.Bind())
					if err != nil {
						time.Sleep(1 * time.Second)
					}
					retryCnt++
				}
				if err != nil {
					errorChannels <- err
					return
				}
				transmitterChannels <- transmitter
			}()
		}
	}

	for i := 0; i < len(transmitterAddrs)*connsPerTarget; i++ {
		select {
		case transmitter := <-transmitterChannels:
			s.transmitters = append(s.transmitters, transmitter)
		case err := <-errorChannels:
			return err
		}
	}

	return nil
}

func (s *SMPPClientImpl) Bind(transmitterAddrs []string, receiverAddrs []string, connsPerTarget int, systemID string, systemType string, password string) error {
	if err := s.bindTransmitters(transmitterAddrs, connsPerTarget, systemID, systemType, password); err != nil {
		return err
	}
	if err := s.bindReceivers(receiverAddrs, connsPerTarget, systemID, systemType, password); err != nil {
		return err
	}

	s.DRChannelMapLock = sync.Mutex{}
	s.DRChannelMap = make(map[string]chan string)
	go s.processDeliverSM()
	return nil
}

func (s *SMPPClientImpl) bindReceivers(receiverAddrs []string, connsPerTarget int, systemID string, systemType string, password string) error {
	s.receivers = []*smpp.Receiver{}
	s.deliverSMChannel = make(chan pdu.Body, 1000)

	receiverChannels := make(chan *smpp.Receiver, len(receiverAddrs)*connsPerTarget)
	defer close(receiverChannels)
	errorChannels := make(chan error, len(receiverAddrs)*connsPerTarget)
	defer close(errorChannels)

	for _, receiverAddr := range receiverAddrs {
		for i := 0; i < connsPerTarget; i++ {
			go func() {
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
				var err error = nil
				retryCnt := 0
				for (err != nil || retryCnt == 0) && retryCnt < 10 {
					err = bind(receiver.Bind())
					if err != nil {
						time.Sleep(1 * time.Second)
					}
					retryCnt++
				}
				if err != nil {
					errorChannels <- err
					return
				}
				receiverChannels <- receiver
			}()
		}
	}

	for i := 0; i < len(receiverAddrs)*connsPerTarget; i++ {
		select {
		case receiver := <-receiverChannels:
			s.receivers = append(s.receivers, receiver)
		case err := <-errorChannels:
			return err
		}
	}

	return nil
}

func (s *SMPPClientImpl) SubmitMT(destinationMSISDN string, message string, tlvs map[pdutlv.Tag]interface{}) (string, error) {
	shortMessage := smpp.ShortMessage{
		Dst:       destinationMSISDN,
		Text:      pdutext.Raw(message),
		TLVFields: tlvs,
	}
	transmitter := s.transmitters[rand.Intn(len(s.transmitters))]
	resp, err := transmitter.Submit(&shortMessage)
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
			messageID, stat, ok := extractMessageIDAndStateFromShortMessage(deliverSM)
			if ok {
				s.getDRChannel(messageID) <- stat
			}
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

func extractMessageIDAndStateFromShortMessage(deliverSM pdu.Body) (string, string, bool) {
	matches := drShortMessageRegex.FindAllStringSubmatch(deliverSM.Fields()[pdufield.ShortMessage].String(), -1)
	result := make(map[string]string)
	for _, match := range matches {
		result[match[1]] = match[2]
	}
	messageID, ok := result["id"]
	if !ok {
		return "", "", false
	}
	stat, ok := result["stat"]
	if !ok {
		return "", "", false
	}
	return messageID, stat, true
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
