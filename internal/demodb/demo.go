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
	return addInventory(db)
}

func addInventory(db *sql.DB) error {
	_, err := db.Exec(`
ALTER TABLE vms_server ADD COLUMN net_addr_list TEXT;
ALTER TABLE vms_server ADD COLUMN os_info TEXT;
ALTER TABLE vms_server_user_attributes ADD COLUMN max_cameras INTEGER;
ALTER TABLE vms_server_user_attributes ADD COLUMN redundancy BOOLEAN;
UPDATE vms_server SET net_addr_list='192.0.2.1:7001;[2001:db8::1]:7001', os_info='{"platform":"linux_x64","variant":"ubuntu","variantVersion":"24.04"}';
INSERT INTO vms_server_user_attributes VALUES(x'10000000000000000000000000000001','Campus recorder',128,0);
CREATE TABLE vms_users(id INTEGER PRIMARY KEY,type INTEGER,fullName TEXT,email TEXT,isEnabled BOOLEAN,groupIds BLOB,permissions INTEGER);
CREATE TABLE vms_user_groups(id BLOB,name TEXT,parentGroupIds BLOB);
CREATE TABLE vms_mserver_discovery(server_id TEXT,url TEXT,ignore BOOLEAN);
CREATE TABLE vms_storage(resource_ptr_id INTEGER,space_limit INTEGER,used_for_writing BOOLEAN,storage_type TEXT,backup BOOLEAN);
CREATE TABLE vms_license(id INTEGER PRIMARY KEY,license_key TEXT,license_block TEXT);
INSERT INTO vms_resource VALUES
 (201,x'20000000000000000000000000000001',NULL,'admin',''),
 (202,x'20000000000000000000000000000002',NULL,'Taylor',''),
 (203,x'20000000000000000000000000000003',NULL,'operator@example.invalid',''),
 (301,x'30000000000000000000000000000001',x'10000000000000000000000000000001','System storage','/opt/networkoptix/mediaserver/var/data'),
 (302,x'30000000000000000000000000000002',x'10000000000000000000000000000001','Recording disk','/mnt/recordings/HD Witness Media'),
 (303,x'30000000000000000000000000000003',x'10000000000000000000000000000001','Backup disk','/mnt/backup/HD Witness Media');
INSERT INTO vms_users VALUES
 (201,0,'Campus administrator','admin@example.invalid',1,x'00000000000000000000100000000000',0),
 (202,0,'','',1,x'00000000000000000000100000000001',0),
 (203,2,'Campus operations','operator@example.invalid',1,x'00000000000000000000100000000000',0);
INSERT INTO vms_mserver_discovery VALUES('10000000000000000000000000000001','https://192.168.50.10:7001',0);
INSERT INTO vms_storage VALUES
 (301,10737418240,0,'local',0),
 (302,32212254720,1,'local',0),
 (303,32212254720,1,'local',1);
INSERT INTO vms_kvpair(resource_guid,name,value) VALUES
 (x'20000000000000000000000000000001','systemName','Demo campus'),
 (x'20000000000000000000000000000001','localSystemId','40000000-0000-0000-0000-000000000001'),
 (x'20000000000000000000000000000001','cloudAccountName','operator@example.invalid'),
 (x'10000000000000000000000000000001','fullVersion','6.1.2.42921-demo-default'),
 (x'10000000000000000000000000000001','systemRuntime','Ubuntu 24.04 LTS'),
 (x'10000000000000000000000000000001','cpuArchitecture','x86_64'),
 (x'10000000000000000000000000000001','cpuModelName','Demo recorder CPU'),
 (x'10000000000000000000000000000001','physicalMemory','8589934592'),
 (x'10000000000000000000000000000001','timeZoneInformation','{"timeZoneId":"Pacific/Auckland","timeZoneOffsetMs":"46800000"}'),
 (x'30000000000000000000000000000001','space','64000000000'),
 (x'30000000000000000000000000000002','space','4000000000000'),
 (x'30000000000000000000000000000003','space','4000000000000'),
 (x'30000000000000000000000000000002','storageArchiveMode','exclusive'),
 (x'30000000000000000000000000000002','persistentStorageStatusFlags','dbReady');
INSERT INTO vms_license VALUES
 (1,'DEMO-ONLY-0000-0001','NAME=hdwitness
SERIAL=DEMO-ONLY-0000-0001
COUNT=8
CLASS=starter
VERSION=6.1.2.42921
BRAND=hdwitness
HWID=DEMO-HARDWARE-ID
EXPIRATION=
COMPANY=Example Campus
SUPPORT=Example partner
SIGNATURE2=DEMO-NOT-A-VALID-LICENCE'),
 (2,'DEMO-ONLY-0000-0002','NAME=hdwitness
SERIAL=DEMO-ONLY-0000-0002
COUNT=1
CLASS=digital
VERSION=6.1.2.42921
BRAND=hdwitness
HWID=DEMO-HARDWARE-ID
EXPIRATION=
COMPANY=Example Campus
SUPPORT=
SIGNATURE2=DEMO-NOT-A-VALID-LICENCE');
`)
	return err
}
