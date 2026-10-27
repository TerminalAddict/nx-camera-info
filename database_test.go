package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Deliberately small synthetic database: no user data belongs in test fixtures.
func fixture(t *testing.T) (string, *sql.DB) {
	t.Helper()
	name := "camera #1 100% café.sqlite"
	if runtime.GOOS != "windows" {
		name = "camera #1 ? 100% café.sqlite"
	}
	path := filepath.Join(t.TempDir(), name)
	uri := databaseFileURL(filepath.ToSlash(path))
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	_, err = db.Exec(`
CREATE TABLE vms_resource(id INTEGER PRIMARY KEY, guid BLOB, parent_guid BLOB, name TEXT, url TEXT);
CREATE TABLE vms_camera(resource_ptr_id INTEGER, vendor TEXT, model TEXT, mac TEXT, physical_id TEXT, group_name TEXT);
CREATE TABLE vms_camera_user_attributes(camera_guid BLOB, camera_name TEXT, logical_id TEXT, group_name TEXT, schedule_enabled BOOLEAN, audio_enabled BOOLEAN);
CREATE TABLE vms_server(resource_ptr_id INTEGER, version TEXT);
CREATE TABLE vms_server_user_attributes(server_guid BLOB, server_name TEXT);
CREATE TABLE vms_resource_status(guid BLOB, status INTEGER);
CREATE TABLE vms_kvpair(id INTEGER PRIMARY KEY, resource_guid BLOB, name TEXT, value TEXT);
INSERT INTO vms_resource VALUES
 (1, x'00112233445566778899aabbccddeeff', x'10112233445566778899aabbccddeeff', 'Discovery name', 'http://url-user:URL_SECRET@192.0.2.10:8080/device?token=QUERY_SECRET#FRAGMENT_SECRET'),
 (2, x'02112233445566778899aabbccddeeff', x'10112233445566778899aabbccddeeff', 'Second camera', 'rtsp://[2001:db8::2]/live'),
 (3, x'03112233445566778899aabbccddeeff', NULL, 'Desktop', ''),
 (10, x'10112233445566778899aabbccddeeff', NULL, 'Server', 'http://192.0.2.1:7001');
INSERT INTO vms_camera VALUES
 (1, 'Example', 'One', '00:11:22:33:44:55', 'physical-1', ''),
 (2, 'Example', 'Two', NULL, 'physical-2', ''),
 (3, '', 'virtual desktop camera', NULL, '', '');
INSERT INTO vms_camera_user_attributes VALUES
 (x'00112233445566778899aabbccddeeff', 'Front door', '1', 'Building', 1, 0);
INSERT INTO vms_server VALUES(10, '6.1.2.42921');
INSERT INTO vms_server_user_attributes VALUES(x'10112233445566778899aabbccddeeff', 'Recording server');
INSERT INTO vms_resource_status VALUES(x'00112233445566778899aabbccddeeff', 2);
INSERT INTO vms_kvpair(resource_guid, name, value) VALUES
 (x'00112233445566778899aabbccddeeff', 'credentials', '` + demoCredentials + `'),
 (x'02112233445566778899aabbccddeeff', 'defaultCredentials', '` + demoCredentials + `'),
 (x'00112233445566778899aabbccddeeff', 'streamUrls', '{"1":"rtsp://viewer:STREAM_SECRET@192.0.2.10/live?auth=STREAM_TOKEN"}'),
 (x'03112233445566778899aabbccddeeff', 'driverClass', 'QnDesktopCameraResource');
`)
	if err != nil {
		t.Fatal(err)
	}
	return path, db
}

func TestDatabaseFileURL(t *testing.T) {
	for _, tc := range []struct{ path, want string }{
		{"/tmp/camera #1 ? 100% café.sqlite", "file:///tmp/camera%20%231%20%3F%20100%25%20caf%C3%A9.sqlite"},
		{"C:/Camera backups/ecs.sqlite", "file:///C:/Camera%20backups/ecs.sqlite"},
		{"d:/camera #1 100%.sqlite", "file:///d:/camera%20%231%20100%25.sqlite"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			uri := databaseFileURL(tc.path)
			if got := uri.String(); got != tc.want {
				t.Fatalf("database URI = %q, want %q", got, tc.want)
			}
			parsed, err := url.Parse(uri.String())
			if err != nil || parsed.Host != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
				t.Fatalf("filename escaped into another URI component: %v, %v", parsed, err)
			}
		})
	}
}

func TestReadReport(t *testing.T) {
	path, _ := fixture(t)
	before, _ := os.ReadFile(path)
	report, err := readReport(context.Background(), Options{DB: path})
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.TotalRecords != 3 || report.Summary.VirtualOmitted != 1 || report.Summary.Recovered != 2 || len(report.Cameras) != 2 {
		t.Fatalf("bad summary: %+v", report.Summary)
	}
	c := report.Cameras[0]
	if c.Name != "Front door" || c.ServerName != "Recording server" || c.Host != "192.0.2.10" || c.Port != "8080" || c.Username != "admin" || c.Password != "[redacted]" {
		t.Fatalf("bad joined camera: %+v", c)
	}
	if c.ID != "00112233-4455-6677-8899-aabbccddeeff" || c.ScheduleEnabled == nil || !*c.ScheduleEnabled || c.AudioEnabled == nil || *c.AudioEnabled {
		t.Fatalf("bad metadata: %+v", c)
	}
	encoded, _ := json.Marshal(report)
	for _, secret := range []string{"demo-password", "URL_SECRET", "QUERY_SECRET", "FRAGMENT_SECRET", "STREAM_SECRET", "STREAM_TOKEN", demoCredentials} {
		if bytes.Contains(encoded, []byte(secret)) {
			t.Fatalf("masked output exposed %q", secret)
		}
	}
	if report.Cameras[1].IP != "2001:db8::2" || report.Cameras[1].Port != "554" || report.Cameras[1].CredentialSource != "defaultCredentials" {
		t.Fatalf("bad fallback or IPv6: %+v", report.Cameras[1])
	}
	after, _ := os.ReadFile(path)
	if sha256.Sum256(before) != sha256.Sum256(after) {
		t.Fatal("database changed")
	}
	readOnly, err := openDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	defer readOnly.Close()
	if _, err := readOnly.Exec("DELETE FROM vms_camera"); err == nil {
		t.Fatal("database connection allowed a write")
	}
}

func TestFilterVirtualAndReveal(t *testing.T) {
	path, _ := fixture(t)
	report, err := readReport(context.Background(), Options{DB: path, Filter: "FRONT", ShowPasswords: true})
	if err != nil || len(report.Cameras) != 1 || report.Cameras[0].Password != "demo-password" || !strings.Contains(report.Cameras[0].URL, "URL_SECRET") {
		t.Fatalf("filter/reveal failed: %+v %v", report, err)
	}
	report, err = readReport(context.Background(), Options{DB: path, IncludeVirtual: true})
	if err != nil || len(report.Cameras) != 3 || report.Summary.Missing != 1 || report.Summary.VirtualOmitted != 0 {
		t.Fatalf("virtual inclusion failed: %+v %v", report.Summary, err)
	}
	report, err = readReport(context.Background(), Options{DB: path, Filter: "no such camera"})
	if err != nil || len(report.Cameras) != 0 {
		t.Fatalf("empty filter result failed: %+v %v", report.Summary, err)
	}
}

func TestWALSnapshot(t *testing.T) {
	path, writer := fixture(t)
	if _, err := writer.Exec("PRAGMA journal_mode=WAL; PRAGMA wal_autocheckpoint=0; UPDATE vms_resource SET name='WAL camera' WHERE id=2"); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path + "-wal"); err != nil || info.Size() == 0 {
		t.Fatalf("test needs committed WAL data: %v", err)
	}
	report, err := readReport(context.Background(), Options{DB: path, Filter: "WAL camera"})
	if err != nil || len(report.Cameras) != 1 {
		t.Fatalf("committed WAL content not read: %+v %v", report.Summary, err)
	}
}

func TestDuplicatePropertiesFailClearly(t *testing.T) {
	path, db := fixture(t)
	if _, err := db.Exec("INSERT INTO vms_kvpair(resource_guid,name,value) SELECT resource_guid,name,'bad' FROM vms_kvpair WHERE name='credentials'"); err != nil {
		t.Fatal(err)
	}
	_, err := readReport(context.Background(), Options{DB: path})
	if err == nil || !strings.Contains(err.Error(), "conflicting duplicate") {
		t.Fatalf("ambiguous credentials accepted: %v", err)
	}
}

func TestCLIFormatsAndFailures(t *testing.T) {
	path, db := fixture(t)
	for _, format := range []string{"table", "json", "csv"} {
		var out, stderr bytes.Buffer
		if code := run([]string{"--db", path, "--format", format}, &out, &stderr); code != 0 {
			t.Fatalf("%s: exit %d: %s", format, code, stderr.String())
		}
		if strings.Contains(out.String(), "demo-password") {
			t.Fatalf("%s revealed a password without opt-in", format)
		}
		if format == "csv" {
			records, err := csv.NewReader(strings.NewReader(out.String())).ReadAll()
			if err != nil || len(records) != 3 || len(records[0]) != 34 {
				t.Fatalf("invalid CSV: %v", err)
			}
		}
	}
	var out, stderr bytes.Buffer
	if code := run([]string{"--db", path, "--summary", "--format", "json"}, &out, &stderr); code != 0 || strings.Contains(out.String(), "Front door") {
		t.Fatalf("summary includes camera data: %d %s", code, out.String())
	}
	if _, err := db.Exec("UPDATE vms_kvpair SET value='bad' WHERE name='credentials'"); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if code := run([]string{"--db", path, "--format", "json"}, &out, &stderr); code != 2 || !strings.Contains(out.String(), "decode_error") {
		t.Fatalf("partial recovery must exit 2 and retain results, got %d", code)
	}
	for _, args := range [][]string{{"--format", "bogus"}, {"--summary", "--format", "csv"}, {"extra"}, {"--db", filepath.Join(t.TempDir(), "missing.sqlite")}} {
		if code := run(args, &out, &stderr); code != 1 {
			t.Fatalf("bad arguments accepted: %v", args)
		}
	}
	if terminalText("camera\x1b[2J\tname") == "camera\x1b[2J\tname" {
		t.Fatal("terminal control characters were not escaped")
	}
}

func TestOutputFileProtection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "export.json")
	if err := writeNewFile(path, []byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := writeNewFile(path, []byte("second")); err == nil {
		t.Fatal("existing file overwritten")
	}
	content, _ := os.ReadFile(path)
	if string(content) != "first" {
		t.Fatal("existing file changed")
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(path)
		if info.Mode().Perm() != 0600 {
			t.Fatalf("export permissions: %v", info.Mode())
		}
		link := filepath.Join(t.TempDir(), "link")
		if err := os.Symlink(path, link); err != nil {
			t.Fatal(err)
		}
		if err := writeNewFile(link, []byte("replacement")); err == nil {
			t.Fatal("followed an existing output symlink")
		}
	}
	missing := filepath.Join(t.TempDir(), "not-a-database.sqlite")
	if _, err := openDatabase(missing); err == nil {
		t.Fatal("missing input accepted")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("missing input was created")
	}
}

func TestAddressAndURLRedaction(t *testing.T) {
	for _, tc := range []struct{ raw, host, port string }{
		{"https://camera.local/path", "camera.local", "443"},
		{"192.0.2.3:9000", "192.0.2.3", "9000"},
		{"rtsp://[2001:db8::1]:8554/live", "2001:db8::1", "8554"},
		{"", "", ""},
	} {
		host, port := address(tc.raw)
		if host != tc.host || port != tc.port {
			t.Fatalf("address %q: %q %q", tc.raw, host, port)
		}
	}
	if got := redactURL("rtsp://u:p@[2001:db8::1]:554/live?token=secret#secret"); got != "rtsp://[2001:db8::1]:554/live" {
		t.Fatal(got)
	}
	if got := redactURL("malformed%password"); got != "[redacted URL]" {
		t.Fatal("malformed URL exposed")
	}
}
