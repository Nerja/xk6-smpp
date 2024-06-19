package smpp

import (
	"fmt"
	"time"

	"github.com/fiorix/go-smpp/smpp"
	"github.com/fiorix/go-smpp/smpp/pdu/pdufield"
	"github.com/fiorix/go-smpp/smpp/pdu/pdutext"
	"github.com/fiorix/go-smpp/smpp/pdu/pdutlv"
)

type SMPPClient interface {
	Bind(transmitterAddr string, receiverAddr string, systemID string, systemType string, password string) error
	SubmitMT(destinationMSISDN string, message string, tlvs map[pdutlv.Tag]interface{}) (string, error)
}

type SMPPClientImpl struct {
	transmitter *smpp.Transmitter
	receiver    *smpp.Receiver
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
	receiver := &smpp.Receiver{
		Addr:               receiverAddr,
		User:               systemID,
		Passwd:             password,
		SystemType:         systemType,
		EnquireLink:        10 * time.Second,
		EnquireLinkTimeout: 30 * time.Second,
	}
	if err := bind(transmitter.Bind()); err != nil {
		return err
	}
	if err := bind(receiver.Bind()); err != nil {
		return err
	}
	s.transmitter = transmitter
	s.receiver = receiver
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

func bind(connStatusChan <-chan smpp.ConnStatus) error {
	if status := <-connStatusChan; status.Error() != nil {
		return status.Error()
	}
	return nil
}
