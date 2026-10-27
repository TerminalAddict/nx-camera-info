// Package demodb creates synthetic camera data for screenshots and GUI checks.
// It contains no customer database records or real credentials.
package demodb

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

func Create(path string) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	file.Close()
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	absolute = filepath.ToSlash(absolute)
	if len(absolute) > 2 && absolute[1] == ':' {
		absolute = "/" + absolute
	}
	uri := (&url.URL{Scheme: "file", Path: absolute}).String()
	db, err := sql.Open("sqlite", uri)
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.Exec(`
CREATE TABLE vms_resource(id INTEGER PRIMARY KEY, guid BLOB, parent_guid BLOB, name TEXT, url TEXT);
CREATE TABLE vms_camera(resource_ptr_id INTEGER, vendor TEXT, model TEXT, mac TEXT, physical_id TEXT, group_name TEXT);
CREATE TABLE vms_camera_user_attributes(camera_guid BLOB, camera_name TEXT, logical_id TEXT, group_name TEXT, schedule_enabled BOOLEAN, audio_enabled BOOLEAN);
CREATE TABLE vms_server(resource_ptr_id INTEGER, version TEXT);
CREATE TABLE vms_server_user_attributes(server_guid BLOB, server_name TEXT);
CREATE TABLE vms_resource_status(guid BLOB, status INTEGER);
CREATE TABLE vms_kvpair(id INTEGER PRIMARY KEY, resource_guid BLOB, name TEXT, value TEXT);
INSERT INTO vms_resource VALUES (100, x'10000000000000000000000000000001', NULL, 'Campus recorder', 'http://192.0.2.1:7001');
INSERT INTO vms_server VALUES(100, '6.1.2.42921');
`)
	if err != nil {
		return err
	}
	rows := []struct{ name, vendor, model, group string }{
		{"Main entrance", "Axis", "P3265-LVE", "Ground floor"},
		{"Reception", "Hanwha", "QND-8080R", "Ground floor"},
		{"East corridor", "Hikvision", "DS-2CD2143", "First floor"},
		{"West corridor", "Axis", "M3085-V", "First floor"},
		{"Loading bay", "Hanwha", "QNO-7082R", "Exterior"},
		{"Car park · North", "Hikvision", "DS-2CD2T46", "Exterior"},
		{"Workshop", "Generic", "RTSP camera", "Service area"},
		{"Meeting room", "Axis", "M3065-V", "First floor"},
		{"Operator desktop", "", "virtual desktop camera", "Control room"},
	}
	for i, r := range rows {
		id := i + 1
		guid := make([]byte, 16)
		guid[15] = byte(id)
		_, err = db.Exec("INSERT INTO vms_resource VALUES(?, ?, x'10000000000000000000000000000001', ?, ?)", id, guid, r.name, fmt.Sprintf("http://192.0.2.%d", id+10))
		if err != nil {
			return err
		}
		_, err = db.Exec("INSERT INTO vms_camera VALUES(?,?,?,?,?,?)", id, r.vendor, r.model, fmt.Sprintf("02:00:00:00:00:%02x", id), fmt.Sprintf("demo-%d", id), r.group)
		if err != nil {
			return err
		}
		_, err = db.Exec("INSERT INTO vms_camera_user_attributes VALUES(?,?,?,?,1,0)", guid, r.name, fmt.Sprint(id), r.group)
		if err != nil {
			return err
		}
		if id <= 6 {
			// Independent synthetic vector: admin / demo-password.
			_, err = db.Exec("INSERT INTO vms_kvpair(resource_guid,name,value) VALUES(?,'credentials',?)", guid, "62dc2dc436557eb439f7be5f64b1ff747bd4e2a2d5fa7b2779e08ecc27645e72")
		} else if id == 8 {
			_, err = db.Exec("INSERT INTO vms_kvpair(resource_guid,name,value) VALUES(?,'credentials','')", guid)
		}
		if err != nil {
			return err
		}
	}
	return nil
}
