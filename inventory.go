package main

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"
)

const activationAvailability = "Remaining activations are unavailable from the database; licensed channels are capacity, not an activation counter"

// Nx 6.1.2 QnLicense::deactivationsCountLeft uses a maximum of three.
// An omitted DEACTIVATIONS field defaults to zero deactivations used.
const maximumLicenseDeactivations int64 = 3

type SystemInfo struct {
	Name              string            `json:"name"`
	LocalID           string            `json:"local_id"`
	CloudID           string            `json:"cloud_id"`
	CloudAccount      string            `json:"cloud_account"`
	CloudHost         string            `json:"cloud_host"`
	LatestTransaction string            `json:"latest_transaction"`
	Properties        map[string]string `json:"properties"`
}

type UserInfo struct {
	ID                string   `json:"id"`
	Account           string   `json:"account"`
	Type              string   `json:"type"`
	DisplayName       string   `json:"display_name"`
	Role              string   `json:"role"`
	Email             string   `json:"email"`
	Enabled           *bool    `json:"enabled"`
	GroupIDs          []string `json:"group_ids"`
	Groups            []string `json:"groups"`
	DirectPermissions *int64   `json:"direct_permissions"`
}

type ServerInfo struct {
	ID                    string             `json:"id"`
	Name                  string             `json:"name"`
	URL                   string             `json:"url"`
	PublicIP              string             `json:"public_ip"`
	NetworkAddresses      []string           `json:"network_addresses"`
	LANDiscoveryAddresses []string           `json:"lan_discovery_addresses"`
	Discovery             []DiscoveryAddress `json:"discovery"`
	Version               string             `json:"version"`
	FullVersion           string             `json:"full_version"`
	OperatingSystem       string             `json:"operating_system"`
	OSInfo                string             `json:"os_info"`
	Architecture          string             `json:"architecture"`
	CPU                   string             `json:"cpu"`
	MemoryBytes           *int64             `json:"memory_bytes"`
	TimeZone              string             `json:"time_zone"`
	TimeZoneOffsetMS      *int64             `json:"time_zone_offset_ms"`
	MaxCameras            *int64             `json:"max_cameras"`
	Redundancy            *bool              `json:"redundancy"`
	Properties            map[string]string  `json:"properties"`
}

type DiscoveryAddress struct {
	URL     string `json:"url"`
	Ignored bool   `json:"ignored"`
}

type StorageInfo struct {
	ID               string            `json:"id"`
	Name             string            `json:"name"`
	Path             string            `json:"path"`
	ServerID         string            `json:"server_id"`
	ServerName       string            `json:"server_name"`
	Type             string            `json:"type"`
	RecordingEnabled *bool             `json:"recording_enabled"`
	Backup           *bool             `json:"backup"`
	StoredSpaceBytes *int64            `json:"stored_space_bytes"`
	SpaceLimitBytes  *int64            `json:"space_limit_bytes"`
	ArchiveMode      string            `json:"archive_mode"`
	StatusFlags      string            `json:"status_flags"`
	Properties       map[string]string `json:"properties"`
}

type LicenseInfo struct {
	ID                      string            `json:"id"`
	Key                     string            `json:"key"`
	Serial                  string            `json:"serial"`
	Name                    string            `json:"name"`
	Channels                *int64            `json:"channels"`
	Class                   string            `json:"class"`
	Version                 string            `json:"version"`
	Brand                   string            `json:"brand"`
	HardwareID              string            `json:"hardware_id"`
	Expiration              string            `json:"expiration"`
	Company                 string            `json:"company"`
	Support                 string            `json:"support"`
	ActivationsRemaining    *int64            `json:"activations_remaining"`
	DeactivationsUsed       *int64            `json:"deactivations_used"`
	DeactivationsRemaining  *int64            `json:"deactivations_remaining"`
	DeactivationEligible    *bool             `json:"deactivation_eligible"`
	DeactivationInformation string            `json:"deactivation_information"`
	ActivationInformation   string            `json:"activation_information"`
	Fields                  map[string]string `json:"fields"`
	LicenseBlock            string            `json:"license_block"`
}

// These properties are selected explicitly. Never export server auth keys,
// cloud tokens, user password hashes, or arbitrary plugin credentials.
var inventoryPropertyNames = []string{
	"systemName", "localSystemId", "cloudSystemID", "cloudAccountName", "cloudHost",
	"auditTrailEnabled", "autoDiscoveryEnabled", "trafficEncryptionForced", "backupSettings",
	"fullVersion", "systemRuntime", "osInfo", "cpuArchitecture", "cpuModelName", "physicalMemory",
	"publicIp", "networkInterfaces", "timeZoneInformation", "timezoneUtcOffset", "hddList",
	"productNameShort", "flavor", "beta", "portForwardingConfigurations", "metadataStorageId",
	"space", "storageArchiveMode", "persistentStorageStatusFlags", "storageInfo",
	"forceAnalyticsDbStoragePermissions",
}

// Optional inventory tables/columns differ across database versions. Discover
// their schema inside the same read transaction, selecting only known fields.
func inventoryRows(ctx context.Context, tx *sql.Tx, table string, fields ...string) ([]map[string]any, bool, error) {
	rows, err := tx.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return nil, false, err
	}
	columns := map[string]bool{}
	for rows.Next() {
		var cid, notnull, pk int
		var name, kind string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &kind, &notnull, &defaultValue, &pk); err != nil {
			rows.Close()
			return nil, false, err
		}
		columns[name] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, false, err
	}
	if len(columns) == 0 {
		return nil, false, nil
	}
	selects := make([]string, len(fields))
	for i, f := range fields {
		if columns[f] {
			selects[i] = `"` + f + `"`
		} else {
			selects[i] = "NULL"
		}
	}
	rows, err = tx.QueryContext(ctx, "SELECT "+strings.Join(selects, ",")+" FROM "+table)
	if err != nil {
		return nil, true, err
	}
	defer rows.Close()
	result := []map[string]any{}
	for rows.Next() {
		values := make([]any, len(fields))
		pointers := make([]any, len(fields))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			return nil, true, err
		}
		row := map[string]any{}
		for i, f := range fields {
			row[f] = values[i]
		}
		result = append(result, row)
	}
	return result, true, rows.Err()
}

func storedString(v any) string {
	if v == nil {
		return ""
	}
	if b, ok := v.([]byte); ok {
		return string(b)
	}
	return fmt.Sprint(v)
}
func storedInt(v any) *int64 {
	if v == nil {
		return nil
	}
	if n, ok := v.(float64); ok {
		// JSON numeric settings must be integral and within the exact integer
		// range before converting. Most Nx properties are decimal strings.
		if n < -(1<<53) || n > 1<<53 || n != float64(int64(n)) {
			return nil
		}
		value := int64(n)
		return &value
	}
	n, err := strconv.ParseInt(storedString(v), 10, 64)
	if err != nil {
		return nil
	}
	return &n
}
func storedBool(v any) *bool {
	if v == nil {
		return nil
	}
	b, err := strconv.ParseBool(storedString(v))
	if err != nil {
		return nil
	}
	return &b
}
func storedUUID(v any) string {
	if b, ok := v.([]byte); ok && len(b) == 16 {
		return formatUUID(hex.EncodeToString(b))
	}
	s := strings.Trim(storedString(v), "{}")
	return formatUUID(strings.ReplaceAll(strings.ToLower(s), "-", ""))
}

func groupIDs(v any) ([]string, error) {
	result := []string{}
	if v == nil || len(storedString(v)) == 0 {
		return result, nil
	}
	if b, ok := v.([]byte); ok && len(b)%16 == 0 {
		for i := 0; i < len(b); i += 16 {
			result = append(result, storedUUID(b[i:i+16]))
		}
		return result, nil
	}
	var ids []string
	if err := json.Unmarshal([]byte(storedString(v)), &ids); err != nil {
		return result, fmt.Errorf("unsupported group ID encoding")
	}
	for _, id := range ids {
		result = append(result, storedUUID(id))
	}
	return result, nil
}

func readInventory(ctx context.Context, tx *sql.Tx, report *Report) error {
	report.Users = []UserInfo{}
	report.Servers = []ServerInfo{}
	report.Storage = []StorageInfo{}
	report.Licenses = []LicenseInfo{}
	read := func(table string, fields ...string) ([]map[string]any, error) {
		r, present, err := inventoryRows(ctx, tx, table, fields...)
		if !present && err == nil {
			report.Warnings = append(report.Warnings, table+" is not present; this section is unavailable")
		}
		return r, err
	}
	resources, err := read("vms_resource", "id", "guid", "parent_guid", "name", "url")
	if err != nil {
		return err
	}
	byID := map[string]map[string]any{}
	for _, r := range resources {
		byID[storedString(r["id"])] = r
	}
	properties, err := readInventoryProperties(ctx, tx)
	if err != nil {
		return err
	}
	props := func(id string) map[string]string {
		if p := properties[id]; p != nil {
			return p
		}
		return map[string]string{}
	}
	// System settings belong to the root admin resource. Detect them by their
	// property names rather than assuming a fixed resource ID or account name.
	systemKeys := []string{"systemName", "localSystemId", "cloudSystemID", "cloudAccountName", "cloudHost", "auditTrailEnabled", "autoDiscoveryEnabled", "trafficEncryptionForced", "backupSettings"}
	system := map[string]string{}
	for _, p := range properties {
		for _, k := range systemKeys {
			if v, ok := p[k]; ok {
				if old, exists := system[k]; exists && old != v {
					return fmt.Errorf("conflicting system property %q", k)
				}
				system[k] = v
			}
		}
	}
	report.System = SystemInfo{Name: system["systemName"], LocalID: system["localSystemId"], CloudID: system["cloudSystemID"], CloudAccount: system["cloudAccountName"], CloudHost: system["cloudHost"], Properties: system}
	var newest sql.NullInt64
	var hasHistory int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='transaction_log'").Scan(&hasHistory); err != nil {
		return err
	}
	if hasHistory > 0 {
		if err := tx.QueryRowContext(ctx, "SELECT max(timestamp) FROM transaction_log").Scan(&newest); err != nil {
			return err
		}
		if newest.Valid {
			report.System.LatestTransaction = time.UnixMilli(newest.Int64).UTC().Format(time.RFC3339Nano)
		}
	}

	groups, err := read("vms_user_groups", "id", "name", "parentGroupIds")
	if err != nil {
		return err
	}
	groupNames := map[string]string{}
	parents := map[string][]string{}
	for i, name := range []string{"Administrator", "Power User", "Advanced Viewer", "Viewer", "Live Viewer", "Health Viewer"} {
		groupNames[fmt.Sprintf("00000000-0000-0000-0000-10000000000%d", i)] = name
	}
	for _, g := range groups {
		id := storedUUID(g["id"])
		if _, builtin := groupNames[id]; !builtin {
			groupNames[id] = storedString(g["name"])
		}
		ids, e := groupIDs(g["parentGroupIds"])
		if e != nil {
			report.Warnings = append(report.Warnings, "Cannot decode parent groups for "+id)
		}
		parents[id] = ids
	}
	users, err := read("vms_users", "id", "type", "fullName", "email", "isEnabled", "groupIds", "permissions")
	if err != nil {
		return err
	}
	for _, row := range users {
		r := byID[storedString(row["id"])]
		u := UserInfo{ID: storedUUID(r["guid"]), Account: storedString(r["name"]), DisplayName: storedString(row["fullName"]), Email: storedString(row["email"]), Enabled: storedBool(row["isEnabled"]), DirectPermissions: storedInt(row["permissions"]), Groups: []string{}}
		u.Type = map[string]string{"0": "Local", "1": "LDAP", "2": "Cloud", "3": "Temporary local"}[storedString(row["type"])]
		if u.Type == "" {
			u.Type = "Unknown (" + storedString(row["type"]) + ")"
		}
		ids, e := groupIDs(row["groupIds"])
		u.GroupIDs = ids
		if e != nil {
			report.Warnings = append(report.Warnings, "Cannot decode groups for account "+u.Account)
			u.Role = "Unknown"
		}
		seen := map[string]bool{}
		var visit func(string)
		visit = func(id string) {
			if seen[id] {
				return
			}
			seen[id] = true
			name := groupNames[id]
			if name == "" {
				name = "Unknown group " + id
			}
			u.Groups = append(u.Groups, name)
			for _, parent := range parents[id] {
				visit(parent)
			}
		}
		for _, id := range ids {
			visit(id)
		}
		if len(u.Groups) > 0 {
			u.Role = strings.Join(u.Groups, ", ")
		} else if u.Role == "" && u.DirectPermissions != nil && *u.DirectPermissions != 0 {
			u.Role = "Custom permissions"
		}
		report.Users = append(report.Users, u)
	}
	sort.Slice(report.Users, func(i, j int) bool {
		return strings.ToLower(report.Users[i].Account) < strings.ToLower(report.Users[j].Account)
	})

	serverAttrs, err := read("vms_server_user_attributes", "server_guid", "server_name", "max_cameras", "redundancy", "backup_bitrate_bytes_per_second", "location_id")
	if err != nil {
		return err
	}
	attrs := map[string]map[string]any{}
	for _, r := range serverAttrs {
		attrs[storedUUID(r["server_guid"])] = r
	}
	discovery, err := read("vms_mserver_discovery", "server_id", "url", "ignore")
	if err != nil {
		return err
	}
	servers, err := read("vms_server", "resource_ptr_id", "version", "net_addr_list", "system_info", "os_info", "flags")
	if err != nil {
		return err
	}
	serverNames := map[string]string{}
	for _, row := range servers {
		r := byID[storedString(row["resource_ptr_id"])]
		id := storedUUID(r["guid"])
		a := attrs[id]
		p := props(id)
		s := ServerInfo{ID: id, Name: storedString(a["server_name"]), URL: redactURL(storedString(r["url"])), Version: storedString(row["version"]), FullVersion: p["fullVersion"], PublicIP: p["publicIp"], OperatingSystem: p["systemRuntime"], OSInfo: storedString(row["os_info"]), Architecture: p["cpuArchitecture"], CPU: p["cpuModelName"], MemoryBytes: storedInt(p["physicalMemory"]), MaxCameras: storedInt(a["max_cameras"]), Redundancy: storedBool(a["redundancy"]), Properties: p, NetworkAddresses: []string{}, LANDiscoveryAddresses: []string{}, Discovery: []DiscoveryAddress{}}
		if s.Name == "" {
			s.Name = storedString(r["name"])
		}
		for _, address := range strings.Split(storedString(row["net_addr_list"]), ";") {
			if address != "" {
				s.NetworkAddresses = append(s.NetworkAddresses, address)
			}
		}
		for _, d := range discovery {
			if storedUUID(d["server_id"]) != id {
				continue
			}
			u := redactURL(storedString(d["url"]))
			ignored := storedBool(d["ignore"])
			item := DiscoveryAddress{URL: u, Ignored: ignored != nil && *ignored}
			s.Discovery = append(s.Discovery, item)
			host, _ := address(u)
			if ip := net.ParseIP(host); ip != nil && ip.IsPrivate() && !item.Ignored {
				s.LANDiscoveryAddresses = append(s.LANDiscoveryAddresses, u)
			}
		}
		var osInfo map[string]string
		if json.Unmarshal([]byte(s.OSInfo), &osInfo) == nil && s.OperatingSystem == "" {
			s.OperatingSystem = strings.TrimSpace(osInfo["variant"] + " " + osInfo["variantVersion"])
		}
		var zone map[string]any
		if json.Unmarshal([]byte(p["timeZoneInformation"]), &zone) == nil {
			s.TimeZone = storedString(zone["timeZoneId"])
			s.TimeZoneOffsetMS = storedInt(zone["timeZoneOffsetMs"])
		}
		for _, k := range []string{"system_info", "flags"} {
			if row[k] != nil {
				p[k] = storedString(row[k])
			}
		}
		for _, k := range []string{"backup_bitrate_bytes_per_second", "location_id"} {
			if a[k] != nil {
				p[k] = storedString(a[k])
			}
		}
		serverNames[id] = s.Name
		report.Servers = append(report.Servers, s)
	}
	sort.Slice(report.Servers, func(i, j int) bool { return report.Servers[i].ID < report.Servers[j].ID })
	storage, err := read("vms_storage", "resource_ptr_id", "space_limit", "used_for_writing", "storage_type", "backup")
	if err != nil {
		return err
	}
	for _, row := range storage {
		r := byID[storedString(row["resource_ptr_id"])]
		id := storedUUID(r["guid"])
		p := props(id)
		serverID := storedUUID(r["parent_guid"])
		path := storedString(r["url"])
		if strings.Contains(path, "://") {
			path = redactURL(path)
		}
		report.Storage = append(report.Storage, StorageInfo{ID: id, Name: storedString(r["name"]), Path: path, ServerID: serverID, ServerName: serverNames[serverID], Type: storedString(row["storage_type"]), RecordingEnabled: storedBool(row["used_for_writing"]), Backup: storedBool(row["backup"]), StoredSpaceBytes: storedInt(p["space"]), SpaceLimitBytes: storedInt(row["space_limit"]), ArchiveMode: p["storageArchiveMode"], StatusFlags: p["persistentStorageStatusFlags"], Properties: p})
	}
	sort.Slice(report.Storage, func(i, j int) bool { return report.Storage[i].ID < report.Storage[j].ID })
	licenses, err := read("vms_license", "id", "license_key", "license_block")
	if err != nil {
		return err
	}
	for _, r := range licenses {
		block := storedString(r["license_block"])
		fields := map[string]string{}
		for _, line := range strings.Split(block, "\n") {
			k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
			if ok {
				fields[strings.TrimSpace(k)] = v
			}
		}
		key := storedString(r["license_key"])
		if key == "" {
			key = fields["SERIAL"]
		}
		license := LicenseInfo{ID: storedString(r["id"]), Key: key, Serial: fields["SERIAL"], Name: fields["NAME"], Channels: storedInt(fields["COUNT"]), Class: fields["CLASS"], Version: fields["VERSION"], Brand: fields["BRAND"], HardwareID: fields["HWID"], Expiration: fields["EXPIRATION"], Company: fields["COMPANY"], Support: fields["SUPPORT"], Fields: fields, LicenseBlock: block, ActivationInformation: activationAvailability}
		license.calculateDeactivations()
		report.Licenses = append(report.Licenses, license)
	}
	sort.Slice(report.Licenses, func(i, j int) bool { return report.Licenses[i].Key < report.Licenses[j].Key })
	report.Summary.Users = len(report.Users)
	report.Summary.Servers = len(report.Servers)
	report.Summary.StorageLocations = len(report.Storage)
	report.Summary.Licenses = len(report.Licenses)
	return nil
}

func (l *LicenseInfo) calculateDeactivations() {
	used := int64(0)
	raw, present := l.Fields["DEACTIVATIONS"]
	if present {
		var err error
		used, err = strconv.ParseInt(strings.TrimSpace(raw), 10, 32)
		if err != nil || used < 0 {
			l.DeactivationInformation = "Unknown: invalid DEACTIVATIONS value in the database snapshot; not verified online"
			return
		}
	}
	l.DeactivationsUsed = &used
	expiration, hasExpiration := l.Fields["EXPIRATION"]
	if l.Class == "" || !hasExpiration {
		l.DeactivationInformation = "Unknown: licence class or expiry field is missing from the database snapshot; not verified online"
		return
	}
	eligible := l.Class != "nvr" && strings.TrimSpace(expiration) == ""
	l.DeactivationEligible = &eligible
	if !eligible {
		l.DeactivationInformation = "Not applicable: Nx does not allow deactivation of NVR or time-limited licences; licence validity has not been verified online"
		return
	}
	remaining := max(int64(0), maximumLicenseDeactivations-used)
	l.DeactivationsRemaining = &remaining
	l.DeactivationInformation = "Calculated from the database snapshot as max(0, 3 - deactivations used); not verified online"
	if !present {
		l.DeactivationInformation += ". DEACTIVATIONS is absent; Nx defaults to zero used"
	}
}

func readInventoryProperties(ctx context.Context, tx *sql.Tx) (map[string]map[string]string, error) {
	args := make([]any, len(inventoryPropertyNames))
	placeholders := make([]string, len(args))
	for i, name := range inventoryPropertyNames {
		args[i] = name
		placeholders[i] = "?"
	}
	rows, err := tx.QueryContext(ctx, "SELECT resource_guid,name,value FROM vms_kvpair WHERE name IN ("+strings.Join(placeholders, ",")+") ORDER BY id", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	properties := map[string]map[string]string{}
	for rows.Next() {
		var rawID any
		var name, value string
		if err := rows.Scan(&rawID, &name, &value); err != nil {
			return nil, err
		}
		id := storedUUID(rawID)
		if properties[id] == nil {
			properties[id] = map[string]string{}
		}
		if old, ok := properties[id][name]; ok && old != value {
			return nil, fmt.Errorf("conflicting duplicate inventory property %q for %s", name, id)
		}
		properties[id][name] = value
	}
	return properties, rows.Err()
}
