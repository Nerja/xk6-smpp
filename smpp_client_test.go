package smpp

import (
	"bufio"
	"bytes"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/fiorix/go-smpp/smpp/pdu"
	"github.com/fiorix/go-smpp/smpp/pdu/pdufield"
	"github.com/fiorix/go-smpp/smpp/pdu/pdutext"
	"github.com/fiorix/go-smpp/smpp/pdu/pdutlv"
	"github.com/stretchr/testify/assert"
)

type SMPPServer struct {
	addr string
}

var (
	systemID       = "magic-systemID"
	password       = "magic-password"
	messageID      = "magic-messageID"
	recipient      = "467097-----"
	message        = "Hello magic world!"
	tlvTag         = 5248
	tlvValue       = "magic-tlv-value"
	testSourceAddr = "346760-----"
)

func TestBind(t *testing.T) {
	testServer, err := newSMPPServer(map[pdu.ID]func(pdu.Body, chan pdu.Body) pdu.Body{})
	if err != nil {
		t.Fatal(err)
	}
	smppClient := new(SMPPClientImpl)
	fmt.Println(testServer.addr)
	if err := smppClient.Bind([]string{testServer.addr}, []string{testServer.addr}, 2, systemID, "systemType", password); err != nil {
		t.Fatal(err)
	}
}

func TestBindToFlappyTarget(t *testing.T) {
	testServer, err := newSMPPServerWithInitialConnClose(map[pdu.ID]func(pdu.Body, chan pdu.Body) pdu.Body{}, 1, 0*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	smppClient := new(SMPPClientImpl)
	fmt.Println(testServer.addr)
	if err := smppClient.Bind([]string{testServer.addr}, []string{testServer.addr}, 2, systemID, "systemType", password); err != nil {
		t.Fatal(err)
	}
}

func TestBindToMultipleFQDNs(t *testing.T) {
	addrs := []string{}
	for i := 0; i < 10; i++ {
		testServer, err := newSMPPServer(map[pdu.ID]func(pdu.Body, chan pdu.Body) pdu.Body{})
		if err != nil {
			t.Fatal(err)
		}
		addrs = append(addrs, testServer.addr)
	}
	smppClient := new(SMPPClientImpl)
	if err := smppClient.Bind(addrs, addrs, 2, systemID, "systemType", password); err != nil {
		t.Fatal(err)
	}
}

func TestSubmitMT(t *testing.T) {

	handlers := map[pdu.ID]func(pdu.Body, chan pdu.Body) pdu.Body{
		pdu.SubmitSMID: func(p pdu.Body, _ chan pdu.Body) pdu.Body {
			resp := pdu.NewSubmitSMResp()
			resp.Header().Seq = p.Header().Seq
			resp.Fields().Set(pdufield.MessageID, messageID)

			recipientMatches := p.Fields()[pdufield.DestinationAddr].String() == recipient
			messageMatches := p.Fields()[pdufield.ShortMessage].String() == message
			tlvMatches := p.TLVFields()[pdutlv.Tag(tlvTag)].String() == tlvValue
			sourceAddrMatches := p.Fields()[pdufield.SourceAddr].String() == testSourceAddr

			if !recipientMatches || !messageMatches || !tlvMatches || !sourceAddrMatches {
				resp.Header().Status = 8
			} else {
				resp.Header().Status = 0
			}
			return resp
		},
	}

	addrs := []string{}
	for i := 0; i < 10; i++ {
		testServer, err := newSMPPServer(handlers)
		if err != nil {
			t.Fatal(err)
		}
		addrs = append(addrs, testServer.addr)
	}
	smppClient := new(SMPPClientImpl)
	if err := smppClient.Bind(addrs, addrs, 2, systemID, "systemType", password); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 100; i++ {
		receivedMessageID, err := smppClient.SubmitMT(recipient, message, testSourceAddr, map[pdutlv.Tag]interface{}{
			pdutlv.Tag(tlvTag): tlvValue,
		})
		if err != nil {
			t.Fatal(err)
		}
		if receivedMessageID != messageID {
			t.Fatalf("Expected message ID '%s', got '%s'", messageID, receivedMessageID)
		}
	}
}

func TestSubmitMTToSlowServer(t *testing.T) {
	handlers := map[pdu.ID]func(pdu.Body, chan pdu.Body) pdu.Body{
		pdu.SubmitSMID: func(p pdu.Body, _ chan pdu.Body) pdu.Body {
			time.Sleep(30 * time.Second)
			return nil
		},
	}
	testServer, err := newSMPPServer(handlers)
	assert.Nil(t, err)
	smppClient := new(SMPPClientImpl)
	addrs := []string{testServer.addr}
	err = smppClient.Bind(addrs, addrs, 1, systemID, "systemType", password)
	assert.Nil(t, err)

	_, err = smppClient.SubmitMT(recipient, message, "TestSourceAddr", map[pdutlv.Tag]interface{}{})
	assert.NotNil(t, err)
}

func TestAwaitDRs(t *testing.T) {
	handler := map[pdu.ID]func(pdu.Body, chan pdu.Body) pdu.Body{
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
	}
	addrs := []string{}
	for i := 0; i < 10; i++ {
		testServer, err := newSMPPServer(handler)
		if err != nil {
			t.Fatal(err)
		}
		addrs = append(addrs, testServer.addr)
	}
	smppClient := new(SMPPClientImpl)
	if err := smppClient.Bind(addrs, addrs, 2, systemID, "systemType", password); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 100; i++ {
		receivedMessageID, err := smppClient.SubmitMT(recipient, message, "TestSourceAddr", map[pdutlv.Tag]interface{}{
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
}

func newSMPPServer(handlers map[pdu.ID]func(pdu.Body, chan pdu.Body) pdu.Body) (*SMPPServer, error) {
	return newSMPPServerWithInitialConnClose(handlers, 0, 0*time.Second)
}

func newSMPPServerWithInitialConnClose(handlers map[pdu.ID]func(pdu.Body, chan pdu.Body) pdu.Body, initialConnClose int, respDelay time.Duration) (*SMPPServer, error) {
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
		closeCnt := 0
		for {
			conn, err := listener.Accept()
			if err != nil {
				continue
			}
			if closeCnt < initialConnClose {
				closeCnt++
				time.Sleep(respDelay)
				conn.Close()
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
