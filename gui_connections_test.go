package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"nx-camera-info/internal/nxconnect"
	"nx-camera-info/internal/nxfixture"
)

func waitConnection(t *testing.T, m *connectionManager, stage string) ConnectionState {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		s := m.State()
		if s.Stage == stage && (stage == "mfa" || stage == "certificate" || !s.Busy) {
			return s
		}
		if s.Stage == "error" && stage != "error" {
			t.Fatal(s.Error)
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("connection did not reach %s: %+v", stage, m.State())
	return ConnectionState{}
}
func TestConnectionCloudOpenCancelAndClose(t *testing.T) {
	_, db := fixture(t)
	data := snapshotBytes(t, db)
	f := nxfixture.New(data)
	var session cameraSession
	m := newConnectionManager(&session)
	m.client = &nxconnect.Client{Transport: f}
	defer m.Shutdown()
	start := func(req ConnectionRequest) {
		t.Helper()
		if err := m.Start(req, func() {}); err != nil {
			t.Fatal(err)
		}
	}
	start(ConnectionRequest{Action: "cloud", Username: "operator@example.invalid", Password: "demo-account-password"})
	if s := waitConnection(t, m, "sites"); len(s.Sites) != 3 {
		t.Fatal("missing shared sites")
	}
	start(ConnectionRequest{Action: "open", SiteID: nxfixture.SharedID})
	waitConnection(t, m, "ready")
	view, err := session.view(ViewOptions{})
	if err != nil || view.Source.Route != "Cloud relay" || view.Filename != "Shared warehouse.db" {
		t.Fatalf("opened cloud report: %v %+v", err, view.Source)
	}
	if m.cloud != nil {
		t.Fatal("cloud credentials retained after opening")
	}
	f.Mode("denied")
	start(ConnectionRequest{Action: "open", Address: "192.0.2.10", Auth: "local", Username: "admin", Password: "demo-local-password"})
	waitConnection(t, m, "error")
	if after, _ := session.view(ViewOptions{}); after.Filename != view.Filename {
		t.Fatal("failed connection discarded existing report")
	}
	f.Mode("slow")
	start(ConnectionRequest{Action: "open", Address: "192.0.2.10", Auth: "local", Username: "admin", Password: "demo-local-password"})
	time.Sleep(30 * time.Millisecond)
	m.Cancel()
	session.close()
	waitConnection(t, m, "cancelled")
	if _, err := session.view(ViewOptions{}); err == nil {
		t.Fatal("cancelled connection reopened a closed report")
	}
	if !strings.Contains(strings.Join(f.Calls(), "\n"), "DELETE 192.0.2.10/rest/v4/login/sessions/local-token") {
		t.Fatal("cancel skipped logout")
	}
}
func TestConnectionMFAPromptAndDiscovery(t *testing.T) {
	_, db := fixture(t)
	f := nxfixture.New(snapshotBytes(t, db))
	f.Mode("mfa")
	var session cameraSession
	m := newConnectionManager(&session)
	m.client = &nxconnect.Client{Transport: f}
	defer m.Shutdown()
	m.discover = func(context.Context) ([]nxconnect.Server, error) {
		return []nxconnect.Server{{Name: "Demo", Address: "https://192.0.2.10:7001"}}, nil
	}
	if err := m.Start(ConnectionRequest{Action: "discover"}, func() {}); err != nil {
		t.Fatal(err)
	}
	if len(waitConnection(t, m, "discovered").Servers) != 1 {
		t.Fatal("discovery result missing")
	}
	if err := m.Start(ConnectionRequest{Action: "open", Address: "192.0.2.10", Auth: "cloud", Username: "operator@example.invalid", Password: "demo-account-password"}, func() {}); err != nil {
		t.Fatal(err)
	}
	waitConnection(t, m, "mfa")
	if err := m.Start(ConnectionRequest{Action: "discover"}, func() {}); err == nil {
		t.Fatal("allowed overlapping connection")
	}
	if err := m.Answer("123456"); err != nil {
		t.Fatal(err)
	}
	waitConnection(t, m, "ready")
	session.close()
}
