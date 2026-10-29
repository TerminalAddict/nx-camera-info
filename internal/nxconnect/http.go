// Package nxconnect reads Nx server metadata and configuration backups. It
// never restores a database or changes site configuration.
package nxconnect

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

const CloudURL = "https://nxvms.com"
const BackupLimit = 128 * 1024 * 1024
const jsonLimit = 16 * 1024 * 1024

var ErrCertificateChanged = errors.New("the server certificate changed; reconnect and verify the new certificate")

type NetworkError struct{}

func (*NetworkError) Error() string {
	return "connection failed or timed out; check the address, network and certificate"
}

type Certificate struct {
	Address     string `json:"address"`
	Fingerprint string `json:"fingerprint"`
	Subject     string `json:"subject"`
	Issuer      string `json:"issuer"`
	Expires     string `json:"expires"`
}
type CertificateError struct{ Certificate Certificate }

func (e *CertificateError) Error() string {
	return "the server certificate is not trusted; verify its fingerprint before continuing"
}

type APIError struct {
	Status int
	Code   string
	Stage  string
}

func (e *APIError) Error() string {
	message := fmt.Sprintf("Nx request failed (HTTP %d)", e.Status)
	switch {
	case e.MFA():
		message = "an authenticator code is required"
	case e.Fresh():
		message = "fresh authentication is required; sign in again"
	case e.Code == "invalidtotp" || e.Code == "invalid_totp":
		message = "the authenticator code was not accepted"
	case e.Code == "accountblocked" || e.Code == "account_blocked":
		message = "this Nx Cloud account is blocked; check its status in the cloud portal"
	case e.Code == "accountnotactivated" || e.Code == "account_not_activated":
		message = "this Nx Cloud account has not been activated"
	case e.Status == 401 || e.Code == "invalid_grant" || e.Code == "badusername":
		message = "sign-in was rejected; check the account and site access"
	case e.Status == 403:
		message = "permission denied; this account needs site backup administrator rights"
	case e.Status == 429:
		message = "Nx rate limit reached; wait before trying again"
	case e.Status == 502 || e.Status == 503 || e.Status == 504:
		message = "the site or cloud relay is unavailable"
	}
	return e.Stage + ": " + message
}
func (e *APIError) MFA() bool {
	return e.Code == "secondfactorrequired" || e.Code == "second_factor_required" || e.Code == "mfarequired" || e.Code == "mfa_required"
}
func (e *APIError) Fresh() bool {
	return e.Code == "userpasswordrequired" || e.Code == "freshsessionrequired" || e.Code == "auth_sessionisnotfresh"
}

// Client contains no credentials. Transport is replaceable for deterministic
// tests; production uses strict system trust for cloud requests.
type Client struct{ Transport http.RoundTripper }

func New() *Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.ResponseHeaderTimeout = 30 * time.Second
	tr.TLSHandshakeTimeout = 10 * time.Second
	return &Client{Transport: tr}
}

func NormalizeAddress(address string) (string, error) {
	address = strings.TrimSpace(address)
	if !strings.Contains(address, "://") {
		address = "https://" + address
	}
	u, err := url.Parse(address)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || u.Opaque != "" {
		return "", errors.New("enter an HTTPS server address or hostname and port, without passwords, paths or query parameters")
	}
	port := u.Port()
	if port == "" {
		port = "7001"
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return "", errors.New("enter a port between 1 and 65535")
	}
	u.Host, u.Path, u.RawPath = net.JoinHostPort(u.Hostname(), port), "", ""
	return u.String(), nil
}

func fingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	return strings.ToUpper(hex.EncodeToString(sum[:]))
}

// A pinned certificate is accepted only for this client/address, and only
// after the UI has asked the user to trust the exact SHA-256 fingerprint.
func (c *Client) Local(address, pin string) *Client {
	if _, ok := c.Transport.(*http.Transport); !ok {
		return c
	} // injected test transport
	u, _ := url.Parse(address)
	tr := c.Transport.(*http.Transport).Clone()
	tr.Proxy = nil                                                                           // A local server connection must not send credentials through an environment proxy.
	tr.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: true, // verified below
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("server did not supply a certificate")
			}
			cert := cs.PeerCertificates[0]
			if pin != "" {
				if fingerprint(cert) == pin {
					return nil
				}
				return ErrCertificateChanged
			}
			intermediates := x509.NewCertPool()
			for _, crt := range cs.PeerCertificates[1:] {
				intermediates.AddCert(crt)
			}
			if _, err := cert.Verify(x509.VerifyOptions{DNSName: u.Hostname(), Intermediates: intermediates}); err == nil {
				return nil
			}
			return &CertificateError{Certificate{Address: address, Fingerprint: fingerprint(cert), Subject: cert.Subject.String(), Issuer: cert.Issuer.String(), Expires: cert.NotAfter.Format(time.RFC3339)}}
		},
	}
	return &Client{Transport: tr}
}
func (c *Client) Close() {
	if tr, ok := c.Transport.(interface{ CloseIdleConnections() }); ok {
		tr.CloseIdleConnections()
	}
}

func ValidSiteID(id string) bool {
	v, err := uuid.Parse(id)
	return err == nil && v != uuid.Nil && v.String() == id
}
func relayURL(siteID string) string { return "https://" + siteID + ".relay.vmsproxy.com" }
func trustedRelay(u *url.URL, siteID string) bool {
	if u.Scheme != "https" || u.User != nil || u.Fragment != "" || (u.Port() != "" && u.Port() != "443") || !ValidSiteID(siteID) {
		return false
	}
	host := u.Hostname()
	if host == siteID+".relay.vmsproxy.com" {
		return true
	}
	prefix := siteID + ".relay-"
	if !strings.HasPrefix(host, prefix) || !strings.HasSuffix(host, ".vmsproxy.com") {
		return false
	}
	region := strings.TrimSuffix(strings.TrimPrefix(host, prefix), ".vmsproxy.com")
	if region == "" {
		return false
	}
	for _, r := range region {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}

func (c *Client) request(ctx context.Context, method, address, token string, body any, siteID string) (*http.Response, error) {
	var data []byte
	if body != nil {
		var err error
		data, err = json.Marshal(body)
		if err != nil {
			return nil, errors.New("could not prepare Nx request")
		}
		defer clear(data)
	}
	u, err := url.Parse(address)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" {
		return nil, errors.New("refused an invalid HTTPS endpoint")
	}
	if siteID != "" && !trustedRelay(u, siteID) {
		return nil, errors.New("refused an untrusted cloud relay")
	}
	client := &http.Client{Transport: c.Transport, Timeout: 5 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for hop := 0; hop < 6; hop++ {
		// Transport may still read/close the request body after Do returns on
		// cancellation. Give it an immutable copy before clearing our buffer.
		req, err := http.NewRequestWithContext(ctx, method, address, strings.NewReader(string(data)))
		if err != nil {
			return nil, errors.New("could not prepare Nx request")
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "NX-Camera-Info/0.6")
		if strings.Contains(req.URL.Path, "/database") || req.URL.Path == "/ec2/dumpDatabase" {
			req.Header.Set("Accept", "application/octet-stream")
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res, err := client.Do(req)
		if err != nil {
			if errors.Is(err, ErrCertificateChanged) {
				return nil, ErrCertificateChanged
			}
			var cert *CertificateError
			if errors.As(err, &cert) {
				return nil, cert
			}
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, &NetworkError{}
		}
		if res.StatusCode < 300 || res.StatusCode > 399 {
			return res, nil
		}
		res.Body.Close()
		location, err := res.Location()
		if err != nil || method != "GET" || siteID == "" || !trustedRelay(location, siteID) {
			return nil, errors.New("refused an unexpected redirect; no credentials were forwarded")
		}
		address = location.String()
	}
	return nil, errors.New("too many cloud relay redirects")
}

func readLimited(res *http.Response, limit int64) ([]byte, error) {
	defer res.Body.Close()
	if res.ContentLength > limit {
		return nil, errors.New("Nx response exceeds the size limit")
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil {
		clear(b)
		return nil, errors.New("download was interrupted or incomplete")
	}
	if int64(len(b)) > limit {
		clear(b)
		return nil, errors.New("Nx response exceeds the size limit")
	}
	return b, nil
}
func responseError(status int, data []byte, stage string) *APIError {
	var obj map[string]json.RawMessage
	json.Unmarshal(data, &obj)
	var code string
	for _, key := range []string{"errorId", "error", "code"} {
		if json.Unmarshal(obj[key], &code) == nil && code != "" && code != "0" && code != "ok" {
			break
		}
		code = ""
	}
	return &APIError{Status: status, Code: strings.ToLower(code), Stage: stage}
}
func (c *Client) json(ctx context.Context, method, address, token string, body any, siteID, stage string, target any) error {
	res, err := c.request(ctx, method, address, token, body, siteID)
	if err != nil {
		return err
	}
	data, err := readLimited(res, jsonLimit)
	if err != nil {
		return err
	}
	defer clear(data)
	apiErr := responseError(res.StatusCode, data, stage)
	if res.StatusCode < 200 || res.StatusCode >= 300 || apiErr.Code != "" {
		return apiErr
	}
	if target == nil || len(data) == 0 {
		return nil
	}
	if json.Unmarshal(data, target) != nil {
		return errors.New(stage + ": Nx returned an unexpected response")
	}
	return nil
}

type Progress func(stage string, received, total int64)

func (c *Client) Download(ctx context.Context, address, token, siteID string, progress Progress) ([]byte, error) {
	if progress != nil {
		progress("downloading", 0, 0)
	}
	paths := []string{"/rest/v4/site/database", "/rest/v3/system/database", "/rest/v2/system/database", "/rest/v1/system/database", "/ec2/dumpDatabase"}
	for _, path := range paths {
		res, err := c.request(ctx, "GET", address+path, token, nil, siteID)
		if err != nil {
			return nil, err
		}
		if res.StatusCode == 404 || res.StatusCode == 405 || res.StatusCode == 501 {
			res.Body.Close()
			continue
		}
		if res.StatusCode != 200 {
			data, err := readLimited(res, jsonLimit)
			if err != nil {
				return nil, err
			}
			e := responseError(res.StatusCode, data, "Download backup")
			clear(data)
			return nil, e
		}
		// JSON/base64 needs a larger wire allowance, but the decoded snapshot is
		// still capped at 128 MiB. No response is ever written to a temp file.
		limit := int64(BackupLimit)
		isJSON := strings.Contains(strings.ToLower(res.Header.Get("Content-Type")), "json")
		if isJSON {
			limit = BackupLimit*4/3 + 1024
		}
		if res.ContentLength > limit {
			res.Body.Close()
			return nil, errors.New("backup exceeds the 128 MiB memory limit")
		}
		var out bytes.Buffer
		buf := make([]byte, 64*1024)
		for {
			n, e := res.Body.Read(buf)
			if int64(out.Len()+n) > limit {
				res.Body.Close()
				clear(out.Bytes())
				clear(buf)
				return nil, errors.New("backup exceeds the 128 MiB memory limit")
			}
			out.Write(buf[:n])
			if progress != nil {
				progress("downloading", int64(out.Len()), res.ContentLength)
			}
			if e != nil {
				res.Body.Close()
				clear(buf)
				if e != io.EOF {
					clear(out.Bytes())
					return nil, errors.New("backup download was interrupted; try again")
				}
				break
			}
		}
		data := out.Bytes()
		if isJSON {
			var obj struct {
				Data string `json:"data"`
			}
			if json.Unmarshal(data, &obj) != nil || obj.Data == "" {
				clear(data)
				return nil, errors.New("Nx returned an invalid database response")
			}
			decoded, err := base64.StdEncoding.DecodeString(obj.Data)
			clear(data)
			if err != nil || len(decoded) > BackupLimit {
				clear(decoded)
				return nil, errors.New("Nx returned an invalid or oversized backup")
			}
			data = decoded
		}
		if len(data) < 100 || !bytes.Equal(data[:16], []byte("SQLite format 3\x00")) {
			clear(data)
			return nil, errors.New("this server returned a backup format that NX Camera Info cannot open")
		}
		return data, nil
	}
	return nil, errors.New("this server has no supported database backup endpoint")
}
