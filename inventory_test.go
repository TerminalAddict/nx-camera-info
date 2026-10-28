package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nx-camera-info/internal/demodb"
)

func inventoryFixture(t *testing.T) (string, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "inventory.sqlite")
	if err := demodb.Create(path); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", databaseFileURL(filepath.ToSlash(path)).String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return path, db
}

func TestInventorySnapshot(t *testing.T) {
	path, db := inventoryFixture(t)
	_, err := db.Exec(`ALTER TABLE vms_users ADD COLUMN hash TEXT;
UPDATE vms_users SET hash='USER_HASH_MUST_NOT_EXPORT';
INSERT INTO vms_kvpair(resource_guid,name,value) VALUES
(x'10000000000000000000000000000001','cloudAuthKey','CLOUD_KEY_MUST_NOT_EXPORT');
ALTER TABLE vms_server ADD COLUMN auth_key TEXT;
UPDATE vms_server SET auth_key='SERVER_KEY_MUST_NOT_EXPORT';`)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	r, err := readReport(context.Background(), Options{DB: path, Filter: "no camera matches", ShowPasswords: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Cameras) != 0 || len(r.Users) != 3 || len(r.Servers) != 1 || len(r.Storage) != 3 || len(r.Licenses) != 2 {
		t.Fatalf("missing non-camera data: %+v", r.Summary)
	}
	if r.Users[0].Account != "admin" || r.Users[0].Role != "Administrator" || r.Users[1].Type != "Cloud" || r.Users[2].Role != "Power User" || r.Users[2].Email != "" {
		t.Fatalf("users: %+v", r.Users)
	}
	s := r.Servers[0]
	if r.System.Name != "Demo campus" || s.Name != "Campus recorder" || s.FullVersion != "6.1.2.42921-demo-default" || s.MemoryBytes == nil || *s.MemoryBytes != 8589934592 || s.TimeZone != "Pacific/Auckland" || len(s.LANDiscoveryAddresses) != 1 {
		t.Fatalf("system/server: %+v %+v", r.System, s)
	}
	if r.Storage[0].RecordingEnabled == nil || *r.Storage[0].RecordingEnabled || r.Storage[2].Backup == nil || !*r.Storage[2].Backup || *r.Storage[1].StoredSpaceBytes != 4000000000000 {
		t.Fatalf("storage: %+v", r.Storage)
	}
	l := r.Licenses[0]
	if l.Key != "DEMO-ONLY-0000-0001" || l.Channels == nil || *l.Channels != 8 || l.ActivationsRemaining != nil || l.DeactivationsRemaining == nil || *l.DeactivationsRemaining != 3 || l.Fields["SIGNATURE2"] != "DEMO-NOT-A-VALID-LICENCE" || !strings.Contains(l.LicenseBlock, "COUNT=8") {
		t.Fatalf("licence: %+v", l)
	}
	for _, format := range []string{"json", "csv", "table"} {
		var out bytes.Buffer
		if err := render(&out, r, Options{Format: format}); err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{"USER_HASH_MUST_NOT_EXPORT", "CLOUD_KEY_MUST_NOT_EXPORT", "SERVER_KEY_MUST_NOT_EXPORT"} {
			if strings.Contains(out.String(), secret) {
				t.Fatalf("%s leaked %s", format, secret)
			}
		}
		if !strings.Contains(out.String(), "DEMO-ONLY-0000-0001") || !strings.Contains(out.String(), "Demo campus") {
			t.Fatalf("%s omitted inventory", format)
		}
	}
	after, _ := os.ReadFile(path)
	if sha256.Sum256(before) != sha256.Sum256(after) {
		t.Fatal("database changed")
	}
}

func TestLicenseDeactivations(t *testing.T) {
	for _, tc := range []struct {
		name, class, expiry, counter string
		used, remaining              string
		eligible, explanation        string
	}{
		{"absent defaults to zero", "digital", "", "absent", "0", "3", "true", "defaults to zero"},
		{"explicit zero", "starter", "", "0", "0", "3", "true", "Calculated"},
		{"one used edge", "edge", "", "1", "1", "2", "true", "Calculated"},
		{"one used starter", "starter", "", "1", "1", "2", "true", "Calculated"},
		{"one used digital", "digital", "", "1", "1", "2", "true", "Calculated"},
		{"two used", "digital", "", "2", "2", "1", "true", "Calculated"},
		{"exhausted", "digital", "", "3", "3", "0", "true", "Calculated"},
		{"above maximum", "digital", "", "4", "4", "0", "true", "Calculated"},
		{"whitespace", "digital", "", " 1 ", "1", "2", "true", "Calculated"},
		{"invalid counter", "digital", "", "broken", "", "", "", "invalid"},
		{"negative counter", "digital", "", "-1", "", "", "", "invalid"},
		{"empty counter", "digital", "", "", "", "", "", "invalid"},
		{"overflow counter", "digital", "", "2147483648", "", "", "", "invalid"},
		{"NVR ineligible", "nvr", "", "1", "1", "", "false", "Not applicable"},
		{"time limited", "digital", "2099-01-01", "1", "1", "", "false", "Not applicable"},
		{"missing class", "", "", "1", "1", "", "", "missing"},
		{"missing expiry", "digital", "absent", "1", "1", "", "", "missing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path, db := inventoryFixture(t)
			block := "CLASS=" + tc.class + "\nCOUNT=99\n"
			if tc.expiry != "absent" {
				block += "EXPIRATION=" + tc.expiry + "\n"
			}
			if tc.counter != "absent" {
				block += "DEACTIVATIONS=" + tc.counter + "\n"
			}
			if _, err := db.Exec("UPDATE vms_license SET license_block=? WHERE id=1", block); err != nil {
				t.Fatal(err)
			}
			r, err := readReport(context.Background(), Options{DB: path})
			if err != nil {
				t.Fatal(err)
			}
			l := r.Licenses[0]
			data, err := record("licenses", l.ID, l)
			if err != nil {
				t.Fatal(err)
			}
			values := map[string]string{}
			for i, key := range data.fields {
				values[key] = data.values[i]
			}
			if values["deactivations_used"] != tc.used || values["deactivations_remaining"] != tc.remaining || values["deactivation_eligible"] != tc.eligible || l.ActivationsRemaining != nil || !strings.Contains(l.DeactivationInformation, tc.explanation) || !strings.Contains(l.DeactivationInformation, "online") {
				t.Fatalf("unexpected deactivation report: %+v", values)
			}
		})
	}
}

func TestLicenseDeactivationExports(t *testing.T) {
	path, _ := inventoryFixture(t)
	var session cameraSession
	if _, err := session.open(path); err != nil {
		t.Fatal(err)
	}
	for _, section := range []string{"licenses", "all"} {
		for _, format := range []string{"json", "csv", "table"} {
			var out, stderr bytes.Buffer
			if code := run([]string{"--db", path, "--section", section, "--format", format}, &out, &stderr); code != 0 {
				t.Fatalf("%s %s: %d %s", section, format, code, stderr.String())
			}
			data := out.Bytes()
			if format != "table" {
				file := filepath.Join(t.TempDir(), "export."+format)
				if _, err := session.export(file, format, ViewOptions{Section: section}); err != nil {
					t.Fatal(err)
				}
				guiData, err := os.ReadFile(file)
				if err != nil || !bytes.Equal(data, guiData) {
					t.Fatalf("GUI and CLI %s %s exports disagree: %v", section, format, err)
				}
			}
			switch format {
			case "json":
				var r Report
				if err := json.Unmarshal(data, &r); err != nil {
					t.Fatal(err)
				}
				if len(r.Licenses) != 2 || r.Licenses[0].DeactivationsRemaining == nil || *r.Licenses[0].DeactivationsRemaining != 3 || r.Licenses[1].DeactivationsRemaining == nil || *r.Licenses[1].DeactivationsRemaining != 2 {
					t.Fatal("JSON lost remaining deactivation counts")
				}
			case "csv":
				rows, err := csv.NewReader(bytes.NewReader(data)).ReadAll()
				if err != nil {
					t.Fatal(err)
				}
				remaining := []string{}
				if section == "all" {
					for _, row := range rows[1:] {
						if row[0] == "licenses" && row[2] == "deactivations_remaining" {
							remaining = append(remaining, row[3])
						}
					}
				} else {
					for i, field := range rows[0] {
						if field == "deactivations_remaining" {
							for _, row := range rows[1:] {
								remaining = append(remaining, row[i])
							}
						}
					}
				}
				if strings.Join(remaining, ",") != "3,2" {
					t.Fatalf("CSV remaining counts: %v", remaining)
				}
			case "table":
				remaining := []string{}
				for _, line := range strings.Split(string(data), "\n") {
					if strings.HasPrefix(line, "deactivations remaining ") {
						remaining = append(remaining, strings.TrimSpace(strings.TrimPrefix(line, "deactivations remaining")))
					}
				}
				if strings.Join(remaining, ",") != "3,2" {
					t.Fatalf("table remaining counts: %v", remaining)
				}
			}
		}
	}
}

func TestInheritedRolesAndDiscovery(t *testing.T) {
	path, db := inventoryFixture(t)
	_, err := db.Exec(`INSERT INTO vms_user_groups VALUES
(x'50000000000000000000000000000001','Operators',x'0000000000000000000010000000000350000000000000000000000000000002'),
(x'50000000000000000000000000000002','Nested',x'50000000000000000000000000000001');
UPDATE vms_users SET groupIds=x'50000000000000000000000000000001',isEnabled=0 WHERE id=202;
INSERT INTO vms_mserver_discovery VALUES
('{10000000-0000-0000-0000-000000000001}','https://192.168.50.11:7001',1),
('10000000000000000000000000000001','https://203.0.113.10:7001',0);
INSERT INTO vms_resource VALUES(101,x'10000000000000000000000000000002',NULL,'Spare server','https://192.0.2.2:7001');
INSERT INTO vms_server(resource_ptr_id,version) VALUES(101,'6.0.0.1');`)
	if err != nil {
		t.Fatal(err)
	}
	r, err := readReport(context.Background(), Options{DB: path})
	if err != nil {
		t.Fatal(err)
	}
	u := r.Users[2]
	if u.Role != "Operators, Viewer, Nested" || u.Enabled == nil || *u.Enabled {
		t.Fatalf("inherited groups: %+v", u)
	}
	if len(r.Servers) != 2 || len(r.Summary.ServerVersions) != 2 || len(r.Servers[0].Discovery) != 3 || len(r.Servers[0].LANDiscoveryAddresses) != 1 {
		t.Fatalf("server discovery: %+v", r.Servers)
	}
}

func TestInventoryExportSections(t *testing.T) {
	path, _ := inventoryFixture(t)
	for _, section := range append([]string{"all"}, reportSections...) {
		for _, format := range []string{"table", "csv", "json"} {
			var out, stderr bytes.Buffer
			if code := run([]string{"--db", path, "--section", section, "--format", format}, &out, &stderr); code != 0 {
				t.Fatalf("%s %s: %d %s", section, format, code, stderr.String())
			}
			if format == "csv" {
				rows, err := csv.NewReader(strings.NewReader(out.String())).ReadAll()
				if err != nil || len(rows) < 2 {
					t.Fatalf("%s invalid CSV: %v", section, err)
				}
				if section == "all" && strings.Join(rows[0], ",") != "record_type,record_id,field,value" {
					t.Fatal("wrong report CSV schema")
				}
				if section == "licenses" && len(rows) != 3 {
					t.Fatal("licences CSV must contain one row per licence, including multiline blocks")
				}
			}
			if format == "json" {
				var result map[string]json.RawMessage
				if err := json.Unmarshal(out.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				if section != "all" {
					if _, ok := result[section]; !ok {
						t.Fatalf("missing %s JSON", section)
					}
				}
			}
			if section == "users" && !strings.Contains(out.String(), "Power User") {
				t.Fatal("user role missing from export")
			}
		}
	}
	var out, stderr bytes.Buffer
	if run([]string{"--db", path, "--section", "bogus"}, &out, &stderr) != 1 {
		t.Fatal("invalid section accepted")
	}
}

func TestInventoryGUIExportAndRefresh(t *testing.T) {
	path, db := inventoryFixture(t)
	var s cameraSession
	view, err := s.open(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Report.Users) != 3 || view.Report.Summary.Licenses != 2 {
		t.Fatal("GUI missing inventory")
	}
	if _, err := db.Exec("UPDATE vms_users SET email='changed@example.invalid' WHERE id=201"); err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"csv", "json"} {
		file := filepath.Join(t.TempDir(), "export."+format)
		if _, err := s.export(file, format, ViewOptions{Filter: "no cameras", Section: "users"}); err != nil {
			t.Fatal(err)
		}
		data, _ := os.ReadFile(file)
		if !strings.Contains(string(data), "admin@example.invalid") || strings.Contains(string(data), "changed@example.invalid") || strings.Contains(string(data), "DEMO-ONLY") {
			t.Fatal("GUI export ignored section or snapshot")
		}
	}
	view, err = s.refresh(ViewOptions{})
	if err != nil || view.Report.Users[0].Email != "changed@example.invalid" {
		t.Fatal("inventory did not refresh", err)
	}
	s.close()
	if _, err := s.view(ViewOptions{}); err == nil {
		t.Fatal("closed inventory remained accessible")
	}
}

func TestInventoryMissingTables(t *testing.T) {
	path, _ := fixture(t)
	r, err := readReport(context.Background(), Options{DB: path})
	if err != nil || len(r.Cameras) != 2 || len(r.Warnings) == 0 || r.Users == nil || r.Licenses == nil {
		t.Fatalf("optional tables must retain camera access with clear notes: %+v %v", r, err)
	}
}

func TestInventoryDuplicatePropertiesAndMalformedGroups(t *testing.T) {
	path, db := inventoryFixture(t)
	if _, err := db.Exec("UPDATE vms_users SET groupIds=x'ff' WHERE id=202"); err != nil {
		t.Fatal(err)
	}
	r, err := readReport(context.Background(), Options{DB: path})
	if err != nil || r.Users[2].Role != "Unknown" || len(r.Warnings) != 1 {
		t.Fatalf("malformed groups must be reported: %+v %v", r.Users, err)
	}
	if _, err := db.Exec("INSERT INTO vms_kvpair(resource_guid,name,value) SELECT resource_guid,name,'conflict' FROM vms_kvpair WHERE name='fullVersion'"); err != nil {
		t.Fatal(err)
	}
	if _, err := readReport(context.Background(), Options{DB: path}); err == nil || !strings.Contains(err.Error(), "conflicting duplicate inventory property") {
		t.Fatalf("ambiguous inventory accepted: %v", err)
	}
}

func TestUserReportDoesNotFailForCameraCredentials(t *testing.T) {
	path, db := inventoryFixture(t)
	if _, err := db.Exec("UPDATE vms_kvpair SET value='invalid' WHERE name='credentials'"); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	if code := run([]string{"--db", path, "--section", "users"}, &out, &stderr); code != 0 || !strings.Contains(out.String(), "Administrator") {
		t.Fatalf("user report depends on camera credential recovery: %d %s", code, stderr.String())
	}
}
