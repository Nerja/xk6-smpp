package internal

import (
	"bufio"
	"bytes"
	"fmt"
	"net"
	"testing"

	"github.com/fiorix/go-smpp/smpp/pdu"
	"github.com/fiorix/go-smpp/smpp/pdu/pdufield"
)

type SMPPServer struct {
	addr string
}

var (
	systemID = "magic-systemID"
	password = "magic-password"
)

func TestBind(t *testing.T) {
	testServer, err := newSMPPServer()
	if err != nil {
		t.Fatal(err)
	}
	smppClient := new(SMPPClientImpl)
	fmt.Println(testServer.addr)
	if err := smppClient.Bind(testServer.addr, testServer.addr, systemID, "systemType", password); err != nil {
		t.Fatal(err)
	}
}

func newSMPPServer() (*SMPPServer, error) {
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
					}
					var b bytes.Buffer
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
