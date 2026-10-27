package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGUISnapshotRevealFilterAndExport(t *testing.T) {
	path, db := fixture(t)
	var session cameraSession
	view, err := session.open(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Report.Cameras) != 2 || view.Report.Summary.VirtualOmitted != 1 {
		t.Fatal("incorrect initial view")
	}
	masked, _ := json.Marshal(view)
	for _, secret := range []string{"demo-password", "URL_SECRET", "QUERY_SECRET", "STREAM_SECRET"} {
		if strings.Contains(string(masked), secret) {
			t.Fatalf("masked GUI leaked %s", secret)
		}
	}
	revealed, err := session.view(ViewOptions{Reveal: true, Filter: "front"})
	if err != nil || len(revealed.Report.Cameras) != 1 || revealed.Report.Cameras[0].Password != "demo-password" {
		t.Fatalf("reveal/filter: %+v %v", revealed, err)
	}
	if _, err := db.Exec("UPDATE vms_resource SET url='http://changed.example' WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"json", "csv"} {
		output := filepath.Join(t.TempDir(), "export."+format)
		count, err := session.export(output, format, ViewOptions{Filter: "front"})
		if err != nil || count != 1 {
			t.Fatalf("export: %d %v", count, err)
		}
		data, _ := os.ReadFile(output)
		for _, forbidden := range []string{"demo-password", "URL_SECRET", "changed.example", "Second camera"} {
			if strings.Contains(string(data), forbidden) {
				t.Fatalf("export did not preserve masked filtered snapshot: %s", forbidden)
			}
		}
		if _, err := session.export(output, format, ViewOptions{Reveal: true}); err == nil {
			t.Fatal("overwrote export")
		}
	}
	revealed, _ = session.view(ViewOptions{Reveal: true})
	if revealed.Report.Cameras[0].StreamURLs["1"] == "" || !strings.Contains(revealed.Report.Cameras[0].StreamURLs["1"], "STREAM_SECRET") {
		t.Fatal("masking changed original snapshot")
	}
	refreshed, err := session.refresh(ViewOptions{Filter: "changed.example"})
	if err != nil || len(refreshed.Report.Cameras) != 1 {
		t.Fatalf("refresh: %+v %v", refreshed, err)
	}
	if _, err := session.open(filepath.Join(t.TempDir(), "missing.sqlite")); err == nil {
		t.Fatal("accepted missing database")
	}
	if _, err := session.view(ViewOptions{}); err != nil {
		t.Fatal("failed open discarded previous database")
	}
	session.close()
	if _, err := session.view(ViewOptions{Reveal: true}); err == nil {
		t.Fatal("closed snapshot still available")
	}
}
