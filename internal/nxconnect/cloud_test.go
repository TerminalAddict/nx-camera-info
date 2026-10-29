package nxconnect

import (
	"encoding/json"
	"testing"
)

func TestCloudSiteResponseShapes(t *testing.T) {
	for _, raw := range []string{`[]`, `  []  `, `{"systems":[]}`, `{"sites":[]}`, `{"reply":{"systems":[]}}`} {
		if rows, err := parseSites(json.RawMessage(raw)); err != nil || len(rows) != 0 {
			t.Fatalf("supported list %s: %v", raw, err)
		}
	}
	for _, raw := range []string{`null`, `{}`, `{"systems":null}`, `{"systems":[null]}`, `{"systems":{}}`, `{"error":"notAuthorized"}`} {
		_, err := parseSites(json.RawMessage(raw))
		if err == nil {
			t.Fatalf("ambiguous/invalid list accepted: %s", raw)
		}
	}
}
