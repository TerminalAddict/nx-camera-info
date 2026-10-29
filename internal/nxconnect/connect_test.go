package nxconnect_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nx-camera-info/internal/nxconnect"
	"nx-camera-info/internal/nxfixture"
)

func backup() []byte { return append([]byte("SQLite format 3\x00"), make([]byte, 100)...) }
func TestCloudDiscoveryDownloadMFAFreshAndCleanup(t *testing.T) {
	for _, mode := range []string{"normal", "bare-list", "mfa", "fresh"} {
		t.Run(mode, func(t *testing.T) {
			fixture := nxfixture.New(backup())
			fixture.Mode(mode)
			client := &nxconnect.Client{Transport: fixture}
			codes := 0
			cloud := nxconnect.NewCloud(client, "operator@example.invalid", "demo-account-password", func(context.Context, string) (string, error) {
				codes++
				if codes == 1 {
					return "000000", nil
				}
				return "123456", nil
			})
			ctx := context.Background()
			if err := cloud.Login(ctx); err != nil {
				t.Fatal(err)
			}
			sites, err := cloud.Sites(ctx)
			if err != nil || len(sites) != 3 {
				t.Fatalf("all owned/shared/organisation sites: %d %v", len(sites), err)
			}
			data, err := cloud.Download(ctx, nxfixture.SiteID, "", nil, nil)
			if err != nil || !bytes.Equal(data, backup()) {
				t.Fatalf("download: %v", err)
			}
			if mode == "mfa" && codes != 2 {
				t.Fatal("MFA did not retry and validate the site token")
			}
			if err := cloud.Close(ctx); err != nil {
				t.Fatal(err)
			}
			calls := strings.Join(fixture.Calls(), "\n")
			if !strings.Contains(calls, "relay-test.vmsproxy.com/rest/v4/site/database site-") {
				t.Fatal("regional redirect lost site bearer")
			}
			child := strings.Index(calls, "DELETE nxvms.com/cdb/oauth2/token/site-refresh-")
			parent := strings.Index(calls, "DELETE nxvms.com/cdb/oauth2/token/cloud-refresh-")
			if child < 0 || parent < child {
				t.Fatal("tokens not revoked child first")
			}
		})
	}
}
func TestCloudDirectServerAndDeniedOrUntrustedRelay(t *testing.T) {
	for _, mode := range []string{"direct", "denied", "redirect-attack", "corrupt"} {
		t.Run(mode, func(t *testing.T) {
			fixture := nxfixture.New(backup())
			fixture.Mode(mode)
			client := &nxconnect.Client{Transport: fixture}
			cloud := nxconnect.NewCloud(client, "operator@example.invalid", "demo-account-password", nil)
			defer cloud.Close(context.Background())
			address := ""
			if mode == "direct" {
				address = "https://192.0.2.10:7001"
			}
			_, err := cloud.Download(context.Background(), nxfixture.SiteID, address, client, nil)
			if mode == "direct" && err != nil {
				t.Fatal(err)
			}
			if mode != "direct" && err == nil {
				t.Fatal("unsafe/unauthorised backup accepted")
			}
			calls := strings.Join(fixture.Calls(), "\n")
			if strings.Contains(calls, "example.invalid/stolen") {
				t.Fatal("token forwarded to arbitrary host")
			}
			if mode == "denied" && strings.Contains(calls, "/rest/v3/") {
				t.Fatal("permission denial triggered endpoint fallback")
			}
			if mode == "direct" && (strings.Contains(calls, "login/sessions") || strings.Contains(calls, "relay.vmsproxy")) {
				t.Fatal("cloud credentials used with server login or unwanted relay")
			}
		})
	}
}
func TestLocalCertificateTrustAndLogin(t *testing.T) {
	fixture := nxfixture.New(backup())
	server := httptest.NewUnstartedServer(fixture)
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.StartTLS()
	defer server.Close()
	client := nxconnect.New()
	defer client.Close()
	untrusted := client.Local(server.URL, "")
	defer untrusted.Close()
	_, err := untrusted.Probe(context.Background(), server.URL)
	var cert *nxconnect.CertificateError
	if !errors.As(err, &cert) || len(cert.Certificate.Fingerprint) != 64 {
		t.Fatalf("expected certificate challenge, got %v", err)
	}
	if len(fixture.Calls()) != 0 {
		t.Fatal("sent HTTP data before certificate trust")
	}
	trusted := client.Local(server.URL, cert.Certificate.Fingerprint)
	defer trusted.Close()
	info, err := trusted.Probe(context.Background(), server.URL)
	if err != nil || info.CloudSystemID != nxfixture.SiteID {
		t.Fatalf("trusted server: %v", err)
	}
	login, err := trusted.LoginLocal(context.Background(), server.URL, "admin", "demo-local-password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = trusted.Download(context.Background(), server.URL, login.Token, "", nil); err != nil {
		t.Fatal(err)
	}
	if err = trusted.LogoutLocal(context.Background(), server.URL, login); err != nil {
		t.Fatal(err)
	}
	wrong := client.Local(server.URL, strings.Repeat("0", 64))
	defer wrong.Close()
	if _, err = wrong.Probe(context.Background(), server.URL); !errors.Is(err, nxconnect.ErrCertificateChanged) {
		t.Fatalf("changed certificate was not explicitly rejected: %v", err)
	}
}
func TestAddressValidationAndCancellation(t *testing.T) {
	for _, address := range []string{"http://example.com", "https://user:pass@example.com", "https://example.com/path", "https://example.com?token=SECRET", "https://example.com#SECRET", "example.com:0", "example.com:65536"} {
		if _, err := nxconnect.NormalizeAddress(address); err == nil {
			t.Fatalf("accepted unsafe address %q", address)
		}
	}
	for input, want := range map[string]string{"192.0.2.10": "https://192.0.2.10:7001", "server.example:7443": "https://server.example:7443", "https://[::1]:7001/": "https://[::1]:7001"} {
		got, err := nxconnect.NormalizeAddress(input)
		if err != nil || got != want {
			t.Fatalf("address %q: %q %v", input, got, err)
		}
	}
	fixture := nxfixture.New(backup())
	fixture.Mode("slow")
	client := &nxconnect.Client{Transport: fixture}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := client.Download(ctx, "https://192.0.2.10:7001", "local-token", "", nil)
	if err == nil {
		t.Fatal("cancelled download succeeded")
	}
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestBackupJSONAndSizeLimit(t *testing.T) {
	for _, mode := range []string{"oversized", "html", "invalid-json", "truncated"} {
		t.Run(mode, func(t *testing.T) {
			client := &nxconnect.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
				res := &http.Response{StatusCode: 200, Header: make(http.Header), ContentLength: -1, Body: io.NopCloser(strings.NewReader("<html>bad</html>"))}
				switch mode {
				case "oversized":
					res.ContentLength = nxconnect.BackupLimit + 1
				case "invalid-json":
					res.Header.Set("Content-Type", "application/json")
				case "truncated":
					res.Body = io.NopCloser(&brokenReader{})
				}
				return res, nil
			})}
			if _, err := client.Download(context.Background(), "https://server.example:7001", "local-token", "", nil); err == nil {
				t.Fatal("invalid response accepted")
			}
		})
	}
}

type brokenReader struct{}

func (*brokenReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
