package smpp

import "go.k6.io/k6/js/modules"

type K6SMPPClient struct{}

func init() {
	client := &K6SMPPClient{}
	modules.Register("k6/x/smpp", client)
}
