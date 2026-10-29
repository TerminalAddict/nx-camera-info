// Package nxfixture provides synthetic Nx responses for tests and the guismoke
// build only. Production executables do not import it. No real credentials or
// databases belong in this fixture.
package nxfixture

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"nx-camera-info/internal/nxconnect"
)

const SiteID = "11111111-1111-4111-8111-111111111111"
const SharedID = "22222222-2222-4222-8222-222222222222"
const OrgID = "33333333-3333-4333-8333-333333333333"

type Fixture struct {
	mu        sync.Mutex
	mode      string
	data      []byte
	sequence  int
	confirmed map[string]bool
	calls     []string
}

func New(data []byte) *Fixture      { return &Fixture{data: data, confirmed: map[string]bool{}} }
func (f *Fixture) Mode(mode string) { f.mu.Lock(); defer f.mu.Unlock(); f.mode = mode }
func (f *Fixture) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.calls...)
}
func response(status int, body any) *http.Response {
	data, _ := json.Marshal(body)
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(data)), ContentLength: int64(len(data))}
}
func (f *Fixture) RoundTrip(r *http.Request) (*http.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	path := r.URL.Path
	host := r.URL.Hostname()
	if host == "" {
		host = r.Host
	}
	auth := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	f.calls = append(f.calls, r.Method+" "+host+path+" "+auth)
	if path == "/api/moduleInformation" {
		return response(200, map[string]any{"reply": nxconnect.Server{ID: "server-demo", Type: "Media Server", Name: "Demo server", SystemName: "Demo campus", Version: "6.1.2.42921", CloudSystemID: SiteID, CloudHost: "nxvms.com", Port: 7001}}), nil
	}
	if path == "/cdb/oauth2/token" && r.Method == "POST" {
		var body map[string]string
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			return response(400, map[string]string{"error": "invalid_request"}), nil
		}
		if body["username"] != "operator@example.invalid" || body["password"] != "demo-account-password" {
			return response(400, map[string]string{"error": "invalid_grant"}), nil
		}
		scope := body["scope"]
		kind := "cloud"
		if strings.HasPrefix(scope, "cloudSystemId=") && nxconnect.ValidSiteID(strings.TrimPrefix(scope, "cloudSystemId=")) {
			kind = "site"
		} else if scope != nxconnect.CloudURL+" cloudSystemId=*" {
			return response(400, map[string]string{"error": "invalid_request", "error_description": "Access scope with system Ids must not contain anything else"}), nil
		}
		f.sequence++
		token := fmt.Sprintf("%s-%d", kind, f.sequence)
		result := map[string]string{"access_token": token, "refresh_token": kind + "-refresh-" + fmt.Sprint(f.sequence)}
		if f.mode == "mfa" && kind == "site" {
			result["error"] = "secondFactorRequired"
			return response(403, result), nil
		}
		return response(200, result), nil
	}
	if strings.HasPrefix(path, "/cdb/account/self/2fa/totp/key/") {
		if !strings.HasPrefix(auth, "cloud-") || !strings.HasSuffix(path, "/123456") {
			return response(403, map[string]string{"error": "invalidTotp"}), nil
		}
		f.confirmed[r.URL.Query().Get("token")] = true
		return response(200, map[string]any{}), nil
	}
	if path == "/cdb/systems" {
		if !strings.HasPrefix(auth, "cloud-") {
			return response(401, map[string]string{"error": "notAuthorized"}), nil
		}
		owned := nxconnect.Site{ID: SiteID, Name: "Demo campus", Version: "6.1.2.42921"}
		switch r.URL.Query().Get("directlyShared") {
		case "true":
			return response(200, []nxconnect.Site{owned, {ID: SharedID, Name: "Shared warehouse", Version: "6.1.2.42921"}}), nil
		case "false":
			return response(200, []nxconnect.Site{{ID: OrgID, Name: "Organisation office", Version: "6.1.2.42921"}}), nil
		}
		return response(200, []nxconnect.Site{owned}), nil
	}
	if strings.HasPrefix(path, "/cdb/oauth2/token/") && r.Method == "DELETE" {
		if !strings.HasPrefix(auth, "cloud-") {
			return response(403, map[string]string{"error": "notAuthorized"}), nil
		}
		return response(200, map[string]any{}), nil
	}
	if path == "/rest/v4/login/sessions" && r.Method == "POST" {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["username"] != "admin" || body["password"] != "demo-local-password" {
			return response(401, map[string]string{"error": "badUsername"}), nil
		}
		return response(200, map[string]string{"token": "local-token"}), nil
	}
	if strings.HasPrefix(path, "/rest/v4/login/sessions/") && r.Method == "DELETE" {
		return response(200, map[string]any{}), nil
	}
	if path == "/rest/v4/site/database" {
		if f.mode == "denied" {
			return response(403, map[string]string{"error": "accessDenied"}), nil
		}
		if auth != "local-token" && !strings.HasPrefix(auth, "site-") {
			return response(401, map[string]string{"error": "auth_missingCredentials"}), nil
		}
		if f.mode == "mfa" && !f.confirmed[auth] {
			return response(403, map[string]string{"error": "secondFactorRequired"}), nil
		}
		if f.mode == "fresh" && strings.HasPrefix(auth, "site-") && f.sequence == 2 {
			return response(422, map[string]string{"error": "auth_sessionIsNotFresh"}), nil
		}
		if strings.Contains(host, ".relay.vmsproxy.com") {
			location := "https://" + strings.Replace(host, ".relay.vmsproxy.com", ".relay-test.vmsproxy.com", 1) + path
			if f.mode == "redirect-attack" {
				location = "https://example.invalid/stolen"
			}
			res := response(307, map[string]any{})
			res.Header.Set("Location", location)
			return res, nil
		}
		if f.mode == "slow" {
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/octet-stream"}}, ContentLength: -1, Body: slowBody{r.Context()}}, nil
		}
		data := f.data
		if f.mode == "corrupt" {
			data = []byte("<html>not a backup</html>")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/octet-stream"}}, Body: io.NopCloser(bytes.NewReader(data)), ContentLength: int64(len(data))}, nil
	}
	return response(404, map[string]string{"error": "notFound"}), nil
}

type slowBody struct{ ctx context.Context }

func (b slowBody) Read([]byte) (int, error) { <-b.ctx.Done(); return 0, b.ctx.Err() }
func (b slowBody) Close() error             { return nil }

func (f *Fixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	r.URL.Host = r.Host
	res, err := f.RoundTrip(r)
	if err != nil {
		http.Error(w, "fixture request failed", 500)
		return
	}
	defer res.Body.Close()
	for name, values := range res.Header {
		for _, value := range values {
			w.Header().Add(name, value)
		}
	}
	w.WriteHeader(res.StatusCode)
	if f.modeValue() == "slow" {
		if flush, ok := w.(http.Flusher); ok {
			flush.Flush()
		}
	}
	io.Copy(w, res.Body)
}
func (f *Fixture) modeValue() string { f.mu.Lock(); defer f.mu.Unlock(); return f.mode }
