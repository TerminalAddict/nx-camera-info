package main

import "testing"

// Fixed vectors produced independently using OpenSSL enc -aes-128-cbc -nopad
// on synthetic UTF-8 inputs padded to the next block with zero bytes.
const demoCredentials = "62dc2dc436557eb439f7be5f64b1ff747bd4e2a2d5fa7b2779e08ecc27645e72"

func TestCredentialVectors(t *testing.T) {
	for _, tc := range []struct{ name, encoded, username, password string }{
		{"password", demoCredentials, "admin", "demo-password"},
		{"colon", "cfd1a9271d752cc4024c336dc2279848", "operator", "p:a:ss"},
		{"empty password", "46f1a8a07ab5c262bd103ba5bd9e7ee0", "viewer", ""},
		{"username only", "15f6ce62d981a868bf15a9c0f80e25ef", "username", ""},
		{"unicode", "68bb92992fba73e18d67c942498fb578", "user", "päss🔑"},
		{"block boundary", "037194a437d88912a728df58f5dbb3df1ad2db6df657aef53ad435835610361d", "1234567890123456", ""},
		{"empty pair", "87e7713e4b2a34a57bb6e597ae67d4ee", "", ""},
		{"empty field", "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			user, password, err := decodeCredentials(tc.encoded)
			if err != nil || user != tc.username || password != tc.password {
				t.Fatalf("decoded synthetic vector: %q, %q, %v", user, password, err)
			}
		})
	}
}

func TestRejectUnsupportedCredentials(t *testing.T) {
	for _, value := range []string{"plaintext:secret", "0", "ab", "00000000000000000000000000000000", demoCredentials[:len(demoCredentials)-2]} {
		if _, _, err := decodeCredentials(value); err == nil {
			t.Fatalf("accepted invalid credential value %q", value)
		}
	}
}

func TestCredentialPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name   string
		props  map[string]string
		source string
		status string
	}{
		{"explicit", map[string]string{"credentials": demoCredentials, "defaultCredentials": "bad"}, "credentials", "recovered"},
		{"fallback", map[string]string{"defaultCredentials": demoCredentials}, "defaultCredentials", "recovered"},
		{"explicit empty", map[string]string{"credentials": "", "defaultCredentials": demoCredentials}, "credentials", "empty"},
		{"explicit invalid", map[string]string{"credentials": "bad", "defaultCredentials": demoCredentials}, "credentials", "decode_error"},
		{"missing", nil, "", "not_stored"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var cam Camera
			setCredentials(&cam, tc.props, false)
			if cam.CredentialSource != tc.source || cam.CredentialStatus != tc.status {
				t.Fatalf("wrong credential selection: %+v", cam)
			}
			if cam.HasPassword && cam.Password != "[redacted]" {
				t.Fatal("password was not masked")
			}
		})
	}
}
