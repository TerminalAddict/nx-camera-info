package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	_ "modernc.org/sqlite"
)

type Camera struct {
	ID               string            `json:"id"`
	Name             string            `json:"name"`
	Host             string            `json:"host"`
	IP               string            `json:"ip"`
	Port             string            `json:"port"`
	URL              string            `json:"url"`
	Username         string            `json:"username"`
	Password         string            `json:"password"`
	HasPassword      bool              `json:"has_password"`
	CredentialSource string            `json:"credential_source"`
	CredentialStatus string            `json:"credential_status"`
	CredentialError  string            `json:"credential_error,omitempty"`
	Vendor           string            `json:"vendor"`
	Model            string            `json:"model"`
	MAC              string            `json:"mac"`
	Serial           string            `json:"serial"`
	Firmware         string            `json:"firmware"`
	PhysicalID       string            `json:"physical_id"`
	LogicalID        string            `json:"logical_id"`
	Group            string            `json:"group"`
	ServerID         string            `json:"server_id"`
	ServerName       string            `json:"server_name"`
	ServerURL        string            `json:"server_url"`
	ServerVersion    string            `json:"server_version"`
	HTTPPort         string            `json:"http_port"`
	MediaPort        string            `json:"media_port"`
	ONVIFURL         string            `json:"onvif_url"`
	StreamURLs       map[string]string `json:"stream_urls,omitempty"`
	Driver           string            `json:"driver"`
	Virtual          bool              `json:"virtual"`
	StatusCode       *int              `json:"stored_status_code"`
	ScheduleEnabled  *bool             `json:"schedule_enabled"`
	AudioEnabled     *bool             `json:"audio_enabled"`
	Warnings         []string          `json:"warnings,omitempty"`
}

type Summary struct {
	TotalRecords      int      `json:"total_camera_records"`
	VirtualOmitted    int      `json:"virtual_records_omitted"`
	Matched           int      `json:"matched_records"`
	Recovered         int      `json:"credentials_recovered"`
	Empty             int      `json:"empty_credentials"`
	Missing           int      `json:"credentials_not_stored"`
	DecodeErrors      int      `json:"credential_decode_errors"`
	ServerVersions    []string `json:"server_versions"`
	PasswordsRevealed bool     `json:"passwords_revealed"`
}

type Report struct {
	Summary Summary  `json:"summary"`
	Cameras []Camera `json:"cameras"`
}

type Options struct {
	DB             string
	Format         string
	Output         string
	Filter         string
	ShowPasswords  bool
	IncludeVirtual bool
	SummaryOnly    bool
}

func openDatabase(path string) (*sql.DB, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("database must be a regular file")
	}
	uri := &url.URL{Scheme: "file", Path: filepath.ToSlash(absolute)}
	query := url.Values{"mode": {"ro"}, "_pragma": {"query_only(1)", "busy_timeout(5000)"}}
	uri.RawQuery = query.Encode()
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

// All reads run in one SQLite transaction, so camera records and properties
// come from the same snapshot. mode=ro also honours any existing SQLite WAL.
func readReport(ctx context.Context, opts Options) (Report, error) {
	report := Report{Cameras: []Camera{}}
	report.Summary.PasswordsRevealed = opts.ShowPasswords
	db, err := openDatabase(opts.DB)
	if err != nil {
		return report, err
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return report, fmt.Errorf("read database: %w", err)
	}
	defer tx.Rollback()

	properties, err := readProperties(ctx, tx)
	if err != nil {
		return report, fmt.Errorf("read camera properties (expected Nx Witness ecs.sqlite schema): %w", err)
	}
	rows, err := tx.QueryContext(ctx, `
SELECT hex(r.guid), coalesce(nullif(a.camera_name, ''), r.name, ''),
       coalesce(r.url, ''), coalesce(c.vendor, ''), coalesce(c.model, ''),
       coalesce(c.mac, ''), coalesce(c.physical_id, ''), coalesce(a.logical_id, ''),
       coalesce(nullif(a.group_name, ''), c.group_name, ''), hex(r.parent_guid),
       coalesce(nullif(sa.server_name, ''), sr.name, ''), coalesce(sr.url, ''),
       coalesce(s.version, ''), st.status, a.schedule_enabled, a.audio_enabled
FROM vms_camera c
JOIN vms_resource r ON r.id = c.resource_ptr_id
LEFT JOIN vms_camera_user_attributes a ON a.camera_guid = r.guid
LEFT JOIN vms_resource sr ON sr.guid = r.parent_guid
LEFT JOIN vms_server s ON s.resource_ptr_id = sr.id
LEFT JOIN vms_server_user_attributes sa ON sa.server_guid = sr.guid
LEFT JOIN vms_resource_status st ON st.guid = r.guid
ORDER BY lower(coalesce(nullif(a.camera_name, ''), r.name, '')), hex(r.guid)`)
	if err != nil {
		return report, fmt.Errorf("read cameras (expected Nx Witness 6.1 database schema): %w", err)
	}
	defer rows.Close()
	versions := map[string]bool{}
	for rows.Next() {
		var cam Camera
		var id, serverID string
		var status sql.NullInt64
		var schedule, audio sql.NullBool
		if err := rows.Scan(&id, &cam.Name, &cam.URL, &cam.Vendor, &cam.Model,
			&cam.MAC, &cam.PhysicalID, &cam.LogicalID, &cam.Group, &serverID,
			&cam.ServerName, &cam.ServerURL, &cam.ServerVersion, &status, &schedule, &audio); err != nil {
			return report, err
		}
		report.Summary.TotalRecords++
		if cam.ServerVersion != "" {
			versions[cam.ServerVersion] = true
		}
		cam.ID, cam.ServerID = formatUUID(id), formatUUID(serverID)
		if status.Valid {
			n := int(status.Int64)
			cam.StatusCode = &n
		}
		if schedule.Valid {
			v := schedule.Bool
			cam.ScheduleEnabled = &v
		}
		if audio.Valid {
			v := audio.Bool
			cam.AudioEnabled = &v
		}
		props := properties[id]
		cam.Driver = props["driverClass"]
		cam.Virtual = cam.Driver == "QnDesktopCameraResource" || strings.EqualFold(cam.Model, "virtual desktop camera")
		if cam.Virtual && !opts.IncludeVirtual {
			report.Summary.VirtualOmitted++
			continue
		}
		cam.Host, cam.Port = address(cam.URL)
		if ip := net.ParseIP(cam.Host); ip != nil {
			cam.IP = ip.String()
		}
		if !matches(cam, opts.Filter) {
			continue
		}
		cam.Firmware, cam.Serial = props["firmware"], props["serialNumber"]
		cam.HTTPPort, cam.MediaPort = props["http_port"], props["mediaPort"]
		cam.ONVIFURL = props["DeviceUrl"]
		if value := props["streamUrls"]; value != "" {
			if err := json.Unmarshal([]byte(value), &cam.StreamURLs); err != nil {
				cam.StreamURLs = nil
				cam.Warnings = append(cam.Warnings, "streamUrls has an unsupported format")
			}
		}
		setCredentials(&cam, props, opts.ShowPasswords)
		if !opts.ShowPasswords {
			cam.URL = redactURL(cam.URL)
			cam.ServerURL = redactURL(cam.ServerURL)
			cam.ONVIFURL = redactURL(cam.ONVIFURL)
			for key, value := range cam.StreamURLs {
				cam.StreamURLs[key] = redactURL(value)
			}
		}
		report.Cameras = append(report.Cameras, cam)
		switch cam.CredentialStatus {
		case "recovered":
			report.Summary.Recovered++
		case "empty":
			report.Summary.Empty++
		case "not_stored":
			report.Summary.Missing++
		case "decode_error":
			report.Summary.DecodeErrors++
		}
	}
	if err := rows.Err(); err != nil {
		return report, err
	}
	report.Summary.Matched = len(report.Cameras)
	report.Summary.ServerVersions = make([]string, 0, len(versions))
	for version := range versions {
		report.Summary.ServerVersions = append(report.Summary.ServerVersions, version)
	}
	sort.Strings(report.Summary.ServerVersions)
	return report, tx.Commit()
}

func readProperties(ctx context.Context, tx *sql.Tx) (map[string]map[string]string, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT hex(k.resource_guid), k.name, k.value
FROM vms_kvpair k
JOIN vms_resource r ON r.guid = k.resource_guid
JOIN vms_camera c ON c.resource_ptr_id = r.id
WHERE k.name IN ('credentials', 'defaultCredentials', 'firmware', 'serialNumber',
                'http_port', 'mediaPort', 'DeviceUrl', 'streamUrls', 'driverClass')
ORDER BY k.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]map[string]string{}
	for rows.Next() {
		var id, name, value string
		if err := rows.Scan(&id, &name, &value); err != nil {
			return nil, err
		}
		if result[id] == nil {
			result[id] = map[string]string{}
		}
		if previous, exists := result[id][name]; exists && previous != value {
			return nil, fmt.Errorf("conflicting duplicate camera property %q for %s", name, formatUUID(id))
		}
		result[id][name] = value
	}
	return result, rows.Err()
}

func setCredentials(cam *Camera, props map[string]string, reveal bool) {
	// Matches QnVirtualCameraResource::getAuth(): explicit credentials take
	// precedence, including an explicitly empty value. Only absence falls back.
	encoded, found := props["credentials"]
	cam.CredentialSource = "credentials"
	if !found {
		encoded, found = props["defaultCredentials"]
		cam.CredentialSource = "defaultCredentials"
	}
	if !found {
		cam.CredentialSource, cam.CredentialStatus = "", "not_stored"
		return
	}
	username, password, err := decodeCredentials(encoded)
	if err != nil {
		cam.CredentialStatus, cam.CredentialError = "decode_error", err.Error()
		return
	}
	cam.Username, cam.Password = username, password
	cam.HasPassword = password != ""
	cam.CredentialStatus = "recovered"
	if username == "" && password == "" {
		cam.CredentialStatus = "empty"
	}
	if !reveal && cam.HasPassword {
		cam.Password = "[redacted]"
	}
}

func formatUUID(s string) string {
	s = strings.ToLower(s)
	if len(s) != 32 {
		return s
	}
	return s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:]
}

func address(raw string) (host, port string) {
	if raw == "" {
		return "", ""
	}
	if !strings.Contains(raw, "://") {
		raw = "//" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", ""
	}
	host, port = u.Hostname(), u.Port()
	if port == "" {
		switch strings.ToLower(u.Scheme) {
		case "http":
			port = "80"
		case "https":
			port = "443"
		case "rtsp":
			port = "554"
		}
	}
	return host, port
}

// URLs can carry separate credentials or tokens. In the masked view, remove
// userinfo, query and fragment entirely. Unknown URL formats are suppressed.
func redactURL(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Scheme == "" {
		return "[redacted URL]"
	}
	u.User, u.RawQuery, u.Fragment, u.RawFragment, u.ForceQuery = nil, "", "", "", false
	return u.String()
}

func matches(cam Camera, filter string) bool {
	filter = strings.ToLower(filter)
	for _, value := range []string{cam.ID, cam.Name, cam.Host, cam.IP, cam.Vendor,
		cam.Model, cam.MAC, cam.ServerID, cam.ServerName, cam.Group, cam.LogicalID} {
		if strings.Contains(strings.ToLower(value), filter) {
			return true
		}
	}
	return false
}

func nullableInt(v *int) string {
	if v == nil {
		return ""
	}
	return strconv.Itoa(*v)
}

func nullableBool(v *bool) string {
	if v == nil {
		return ""
	}
	return strconv.FormatBool(*v)
}
