package main

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func snapshotBytes(t *testing.T, db *sql.DB) []byte {
	t.Helper()
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var data []byte
	err = conn.Raw(func(driver any) error {
		var err error
		data, err = driver.(interface{ Serialize() ([]byte, error) }).Serialize()
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return data
}
func TestMemoryBackupLifecycle(t *testing.T) {
	_, db := fixture(t)
	data := snapshotBytes(t, db)
	original := bytes.Clone(data)
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	t.Setenv("TEMP", tmp)
	var session cameraSession
	ctx := context.Background()
	if err := session.openBackup(ctx, data, "Demo site.db", DatabaseSource{Kind: "server", Route: "Cloud relay"}); err != nil {
		t.Fatal(err)
	}
	view, err := session.view(ViewOptions{})
	if err != nil || len(view.Report.Cameras) != 2 || view.Report.Cameras[0].Password != "[redacted]" {
		t.Fatalf("masked memory view: %v", err)
	}
	if !bytes.Equal(data, original) {
		t.Fatal("opening changed original backup bytes")
	}
	if files, _ := os.ReadDir(tmp); len(files) != 0 {
		t.Fatal("memory reader created temporary files")
	}
	if _, err := session.refresh(ViewOptions{}); err == nil {
		t.Fatal("remote refresh silently read a file")
	}
	bad := []byte("not a database")
	if err := session.openBackup(ctx, bad, "bad", DatabaseSource{}); err == nil {
		t.Fatal("invalid backup accepted")
	}
	if view, _ := session.view(ViewOptions{}); view.Filename != "Demo site.db" {
		t.Fatal("failed open lost prior snapshot")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := session.openBackup(cancelled, bytes.Clone(original), "cancelled", DatabaseSource{}); err == nil {
		t.Fatal("cancelled backup opened")
	}
	path := filepath.Join(tmp, "saved.db")
	if err := session.saveBackup(path); err != nil {
		t.Fatal(err)
	}
	if saved, _ := os.ReadFile(path); !bytes.Equal(saved, original) {
		t.Fatal("saved backup changed")
	}
	if err := session.saveBackup(path); err == nil {
		t.Fatal("overwrote backup")
	}
	session.close()
	if session.backup != nil || session.snapshot.Cameras != nil {
		t.Fatal("close retained backup or report")
	}
	if !bytes.Equal(data, make([]byte, len(data))) {
		t.Fatal("close did not clear owned download buffer")
	}
	if _, err := session.view(ViewOptions{}); err == nil {
		t.Fatal("closed data still available")
	}
}

func TestMemoryBackupWALHeaderAndCorruption(t *testing.T) {
	_, db := fixture(t)
	data := snapshotBytes(t, db)
	data[18], data[19] = 2, 2
	if _, err := readMemoryReport(context.Background(), data); err != nil {
		t.Fatalf("WAL marker snapshot: %v", err)
	}
	if data[18] != 2 || data[19] != 2 {
		t.Fatal("modified source header")
	}
	data[100] = 255
	if _, err := readMemoryReport(context.Background(), data); err == nil {
		t.Fatal("corrupt SQLite accepted")
	}
}
