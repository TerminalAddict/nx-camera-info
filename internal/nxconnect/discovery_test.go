package nxconnect

import (
	"net"
	"testing"
)

func TestDiscoveryUsesSenderAndRejectsNonServer(t *testing.T) {
	valid := []byte(`{"type":"Media Server","name":"Demo","version":"6.1.2.42921","port":7443,"address":"https://attacker.invalid","remoteAddresses":["attacker.invalid"]}`)
	s, ok := discoveryServer(valid, net.ParseIP("192.0.2.10"))
	if !ok || s.Address != "https://192.0.2.10:7443" {
		t.Fatal("advertised URL overrode sender address")
	}
	for _, invalid := range []string{`{"type":"Desktop Client","version":"6.1","port":7001}`, `{"type":"Media Server","port":7001}`, `{"type":"Media Server","version":"6.1","port":-1}`, `not JSON`} {
		if _, ok := discoveryServer([]byte(invalid), net.ParseIP("192.0.2.10")); ok {
			t.Fatal("invalid discovery announcement accepted")
		}
	}
}
