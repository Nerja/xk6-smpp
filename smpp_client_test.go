package smpp

import (
	"bufio"
	"bytes"
	"fmt"
	"net"
	"testing"

	"github.com/fiorix/go-smpp/smpp/pdu"
	"github.com/fiorix/go-smpp/smpp/pdu/pdufield"
	"github.com/fiorix/go-smpp/smpp/pdu/pdutext"
	"github.com/fiorix/go-smpp/smpp/pdu/pdutlv"
)

type SMPPServer struct {
	addr string
}

var (
	systemID  = "magic-systemID"
	password  = "magic-password"
	messageID = "magic-messageID"
	recipient = "467097-----"
	message   = "Hello magic world!"
	tlvTag    = 5248
	tlvValue  = "magic-tlv-value"
)

func TestBind(t *testing.T) {
	testServer, err := newSMPPServer(map[pdu.ID]func(pdu.Body, chan pdu.Body) pdu.Body{})
	if err != nil {
		t.Fatal(err)
	}
	smppClient := new(SMPPClientImpl)
	fmt.Println(testServer.addr)
	if err := smppClient.Bind(testServer.addr, testServer.addr, systemID, "systemType", password); err != nil {
		t.Fatal(err)
	}
}

func TestSubmitMT(t *testing.T) {
	testServer, err := newSMPPServer(map[pdu.ID]func(pdu.Body, chan pdu.Body) pdu.Body{
		pdu.SubmitSMID: func(p pdu.Body, _ chan pdu.Body) pdu.Body {
			resp := pdu.NewSubmitSMResp()
			resp.Header().Seq = p.Header().Seq
			resp.Fields().Set(pdufield.MessageID, messageID)
			recipientMatches := p.Fields()[pdufield.DestinationAddr].String() == recipient
			messageMatches := p.Fields()[pdufield.ShortMessage].String() == message
			tlvMatches := p.TLVFields()[pdutlv.Tag(tlvTag)].String() == tlvValue
			if !recipientMatches || !messageMatches || !tlvMatches {
				resp.Header().Status = 8
			} else {
				resp.Header().Status = 0
			}
			return resp
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	smppClient := new(SMPPClientImpl)
	fmt.Println(testServer.addr)
	if err := smppClient.Bind(testServer.addr, testServer.addr, systemID, "systemType", password); err != nil {
		t.Fatal(err)
	}

	receivedMessageID, err := smppClient.SubmitMT(recipient, message, map[pdutlv.Tag]interface{}{
		pdutlv.Tag(tlvTag): tlvValue,
	})
	if err != nil {
		t.Fatal(err)
	}
	if receivedMessageID != messageID {
		t.Fatalf("Expected message ID '%s', got '%s'", messageID, receivedMessageID)
	}
}

func TestAwaitDRs(t *testing.T) {
	testServer, err := newSMPPServer(map[pdu.ID]func(pdu.Body, chan pdu.Body) pdu.Body{
		pdu.SubmitSMID: func(p pdu.Body, receiverChan chan pdu.Body) pdu.Body {
			resp := pdu.NewSubmitSMResp()
			resp.Header().Seq = p.Header().Seq
			resp.Fields().Set(pdufield.MessageID, messageID)
			recipientMatches := p.Fields()[pdufield.DestinationAddr].String() == recipient
			messageMatches := p.Fields()[pdufield.ShortMessage].String() == message
			tlvMatches := p.TLVFields()[pdutlv.Tag(tlvTag)].String() == tlvValue
			if !recipientMatches || !messageMatches || !tlvMatches {
				resp.Header().Status = 8
			} else {
				resp.Header().Status = 0
			}

			deliverSM := pdu.NewDeliverSM()
			deliverSM.Fields().Set(pdufield.ESMClass, 4)
			deliverSM.Fields().Set(pdufield.DestinationAddr, "46722335411")
			deliverSM.Fields().Set(pdufield.SourceAddr, "46722335411")
			deliverSM.Fields().Set(pdufield.ShortMessage, pdutext.GSM7("x:y stat:DELIVRD id:"+messageID+" "))
			deliverSM.Fields().Set(pdufield.DataCoding, 0)
			receiverChan <- deliverSM

			return resp
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	smppClient := new(SMPPClientImpl)
	fmt.Println(testServer.addr)
	if err := smppClient.Bind(testServer.addr, testServer.addr, systemID, "systemType", password); err != nil {
		t.Fatal(err)
	}

	receivedMessageID, err := smppClient.SubmitMT(recipient, message, map[pdutlv.Tag]interface{}{
		pdutlv.Tag(tlvTag): tlvValue,
	})
	if err != nil {
		t.Fatal(err)
	}
	if receivedMessageID != messageID {
		t.Fatalf("Expected message ID '%s', got '%s'", messageID, receivedMessageID)
	}
	success, states, err := smppClient.AwaitDRs(receivedMessageID, "DELIVRD")
	if err != nil {
		t.Fatal(err)
	}
	if !success {
		t.Fatalf("Expected success, got failure with states seen: %v", states)
	}
}

func newSMPPServer(handlers map[pdu.ID]func(pdu.Body, chan pdu.Body) pdu.Body) (*SMPPServer, error) {
	server := new(SMPPServer)
	addr, err := net.ResolveTCPAddr("tcp", "localhost:0")
	if err != nil {
		return nil, err
	}
	listener, err := net.ListenTCP("tcp", addr)
	if err != nil {
		return nil, err
	}
	server.addr = listener.Addr().String()

	receiverChan := make(chan pdu.Body, 100)

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				continue
			}
			go func() {
				r, w := bufio.NewReader(conn), bufio.NewWriter(conn)
				for {
					p, err := pdu.Decode(r)
					if err != nil {
						fmt.Printf("Failed to decode PDU %v\n", err)
						continue
					}
					var pduResp pdu.Body
					switch p.Header().ID {
					case pdu.BindTransmitterID:
						pduResp = pdu.NewBindTransmitterResp()
						pduResp.Header().Seq = p.Header().Seq
						pduResp.Header().Status = 0
						pduResp = checkCredentials(p, pduResp)
					case pdu.EnquireLinkID:
						pduResp = pdu.NewEnquireLinkResp()
						pduResp.Header().Seq = p.Header().Seq
					case pdu.BindReceiverID:
						pduResp = pdu.NewBindReceiverResp()
						pduResp.Header().Seq = p.Header().Seq
						pduResp.Header().Status = 0
						pduResp = checkCredentials(p, pduResp)
					case pdu.DeliverSMRespID:
						pduResp = pdu.NewDeliverSMResp()
						pduResp.Header().Seq = p.Header().Seq
						pduResp.Header().Status = 0
					}
					if handler, ok := handlers[p.Header().ID]; ok {
						pduResp = handler(p, receiverChan)
					}
					var b bytes.Buffer
					if pduResp == nil {
						fmt.Printf("No response for PDU %v\n", p.Header().ID)
					}
					if err := pduResp.SerializeTo(&b); err != nil {
						fmt.Printf("Failed to serialize PDU %v\n", err)
						return
					}

					if _, err := w.Write(b.Bytes()); err != nil {
						fmt.Printf("Failed to write PDU %v\n", err)
						return
					}

					if err := w.Flush(); err != nil {
						fmt.Printf("Failed to flush PDU %v\n", err)
						return
					}
					if p.Header().ID == pdu.BindReceiverID {
						go func() {
							for {
								select {
								case dr := <-receiverChan:
									var b bytes.Buffer
									if err := dr.SerializeTo(&b); err != nil {
										fmt.Printf("Failed to serialize PDU %v\n", err)
										return
									}
									if _, err := w.Write(b.Bytes()); err != nil {
										fmt.Printf("Failed to write PDU %v\n", err)
										return
									}
									if err := w.Flush(); err != nil {
										fmt.Printf("Failed to flush PDU %v\n", err)
										return
									}
								}
							}
						}()
					}
				}
			}()
		}
	}()

	return server, nil
}

func checkCredentials(p pdu.Body, resp pdu.Body) pdu.Body {
	foundSystemID := p.Fields()[pdufield.SystemID].String()
	foundPassword := p.Fields()[pdufield.Password].String()
	if foundSystemID != systemID || foundPassword != password {
		resp.Header().Status = 13
	} else {
		resp.Header().Status = 0
	}
	return resp
}
