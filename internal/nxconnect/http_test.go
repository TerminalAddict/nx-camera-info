package nxconnect

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type deferredBodyTransport struct{ body io.ReadCloser }

func (t *deferredBodyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// RoundTripper is allowed to close/read a request body asynchronously,
	// including when a cancelled request has already returned to its caller.
	t.body = req.Body
	return &http.Response{StatusCode: 401, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`))}, nil
}
func TestRequestBodyLifetimeAfterTransportReturns(t *testing.T) {
	tr := &deferredBodyTransport{}
	client := &Client{Transport: tr}
	res, err := client.request(context.Background(), "POST", CloudURL+"/cdb/oauth2/token", "", map[string]string{"password": "synthetic-password"}, "")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	defer tr.body.Close()
	data, err := io.ReadAll(tr.body)
	if err != nil || !strings.Contains(string(data), "synthetic-password") {
		t.Fatal("request buffer was modified while the transport still owned it")
	}
}
