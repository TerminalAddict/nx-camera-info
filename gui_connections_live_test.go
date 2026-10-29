package main

import (
	"encoding/csv"
	"os"
	"strings"
	"testing"
	"time"
)

// Explicit opt-in only. Uses the first account from a local credentials CSV;
// downloads one accessible site into memory and never prints account/site data.
func TestLiveCloudMemoryBackup(t *testing.T) {
	path := os.Getenv("NX_LIVE_ACCOUNTS_CSV")
	if path == "" {
		t.Skip("set NX_LIVE_ACCOUNTS_CSV to opt into one real cloud backup")
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal("could not open credentials CSV")
	}
	defer f.Close()
	r := csv.NewReader(f)
	header, err := r.Read()
	if err != nil {
		t.Fatal("invalid CSV header")
	}
	row, err := r.Read()
	if err != nil {
		t.Fatal("CSV has no account")
	}
	var email, password string
	for i, key := range header {
		switch strings.TrimSpace(strings.TrimPrefix(key, "\ufeff")) {
		case "user":
			email = row[i]
		case "password":
			password = row[i]
		}
	}
	if email == "" || password == "" {
		t.Fatal("CSV needs user and password columns")
	}
	var session cameraSession
	m := newConnectionManager(&session)
	defer m.Shutdown()
	defer session.close()
	wait := func(stage string) ConnectionState {
		t.Helper()
		deadline := time.Now().Add(3 * time.Minute)
		for time.Now().Before(deadline) {
			s := m.State()
			if s.Stage == "error" {
				t.Fatal(s.Error)
			}
			if s.Stage == "mfa" {
				t.Fatal("live account requires interactive MFA; use the GUI")
			}
			if !s.Busy && s.Stage == stage {
				return s
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatal("live connection timed out")
		return ConnectionState{}
	}
	if err = m.Start(ConnectionRequest{Action: "cloud", Username: email, Password: password}, func() {}); err != nil {
		t.Fatal(err)
	}
	email, password = "", ""
	clear(row)
	s := wait("sites")
	if len(s.Sites) == 0 {
		t.Fatal("account has no sites")
	}
	if err = m.Start(ConnectionRequest{Action: "open", SiteID: s.Sites[0].ID}, func() {}); err != nil {
		t.Fatal(err)
	}
	wait("ready")
	view, err := session.view(ViewOptions{})
	if err != nil || view.Source.Kind != "server" || len(session.backup) == 0 {
		t.Fatal("live backup did not open in memory")
	}
	if view.Options.Reveal {
		t.Fatal("live backup opened with passwords revealed")
	}
	session.close()
	if len(session.backup) != 0 {
		t.Fatal("live backup retained after closing")
	}
}
