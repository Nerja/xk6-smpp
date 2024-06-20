package smpp

import (
	"github.com/fiorix/go-smpp/smpp/pdu/pdutlv"
	"go.k6.io/k6/js/modules"
)

type K6SMPPClient struct {
	SMPPClient SMPPClient
}

type MTSubmitResponse struct {
	MessageID string
	Error     error
}

type AwaitDeliveryReceiptResponse struct {
	MessageID  string
	Success    bool
	SeenStates []string
	Error      error
}

func init() {
	client := &K6SMPPClient{
		SMPPClient: &SMPPClientImpl{},
	}
	modules.Register("k6/x/smpp", client)
}

func (c *K6SMPPClient) Bind(transmitterAddr string, receiverAddr string, systemID string, systemType string, password string) error {
	return c.SMPPClient.Bind(transmitterAddr, receiverAddr, systemID, systemType, password)
}

func (c *K6SMPPClient) SubmitMT(destinationMSISDN string, text string, tlvs map[pdutlv.Tag]interface{}) MTSubmitResponse {
	messageID, err := c.SMPPClient.SubmitMT(destinationMSISDN, text, tlvs)
	return MTSubmitResponse{
		MessageID: messageID,
		Error:     err,
	}
}

func (c *K6SMPPClient) AwaitDeliveryReceipt(messageID string, targetState string) AwaitDeliveryReceiptResponse {
	success, seenStates, err := c.SMPPClient.AwaitDRs(messageID, targetState)
	return AwaitDeliveryReceiptResponse{
		MessageID:  messageID,
		Success:    success,
		SeenStates: seenStates,
		Error:      err,
	}
}
