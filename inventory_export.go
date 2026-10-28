package main

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
	"text/tabwriter"
)

var reportSections = []string{"system", "servers", "users", "storage", "licenses", "cameras"}

func validSection(s string) bool {
	if s == "" || s == "all" {
		return true
	}
	for _, section := range reportSections {
		if s == section {
			return true
		}
	}
	return false
}

func sectionJSON(r Report, section string) any {
	if section == "" || section == "all" {
		return r
	}
	var data any
	switch section {
	case "system":
		data = r.System
	case "servers":
		data = r.Servers
	case "users":
		data = r.Users
	case "storage":
		data = r.Storage
	case "licenses":
		data = r.Licenses
	case "cameras":
		data = r.Cameras
	}
	return map[string]any{"summary": r.Summary, section: data, "warnings": r.Warnings}
}

type exportRecord struct {
	kind, id string
	fields   []string
	values   []string
}

// Preserve struct field order, numeric precision and null/unknown values in
// spreadsheet exports. Nested metadata is JSON inside a properly quoted cell.
func record(kind, id string, value any) (exportRecord, error) {
	r := exportRecord{kind: kind, id: id}
	t := reflect.TypeOf(value)
	data, err := json.Marshal(value)
	if err != nil {
		return r, err
	}
	var values map[string]any
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if err = d.Decode(&values); err != nil {
		return r, err
	}
	for i := 0; i < t.NumField(); i++ {
		name := strings.Split(t.Field(i).Tag.Get("json"), ",")[0]
		if name == "" || name == "-" {
			continue
		}
		v := values[name]
		text := ""
		if v != nil {
			if s, ok := v.(string); ok {
				text = s
			} else {
				b, err := json.Marshal(v)
				if err != nil {
					return r, err
				}
				text = string(b)
			}
		}
		r.fields = append(r.fields, name)
		r.values = append(r.values, text)
	}
	return r, nil
}

func inventoryRecords(r Report, section string) ([]exportRecord, error) {
	result := []exportRecord{}
	var failure error
	add := func(kind, id string, v any) {
		if section != "" && section != "all" && section != kind {
			return
		}
		item, err := record(kind, id, v)
		if err != nil {
			failure = err
			return
		}
		result = append(result, item)
	}
	add("system", r.System.LocalID, r.System)
	for _, s := range r.Servers {
		add("servers", s.ID, s)
	}
	for _, u := range r.Users {
		add("users", u.ID, u)
	}
	for _, s := range r.Storage {
		add("storage", s.ID, s)
	}
	for _, l := range r.Licenses {
		add("licenses", l.ID, l)
	}
	for _, c := range r.Cameras {
		add("cameras", c.ID, c)
	}
	return result, failure
}

func renderInventoryCSV(w io.Writer, r Report, section string) error {
	if section == "cameras" {
		return renderCSV(w, r.Cameras)
	}
	records, err := inventoryRecords(r, section)
	if err != nil {
		return err
	}
	out := csv.NewWriter(w)
	if section == "" || section == "all" {
		if err := out.Write([]string{"record_type", "record_id", "field", "value"}); err != nil {
			return err
		}
		for _, r := range records {
			for i, field := range r.fields {
				if err := out.Write([]string{r.kind, r.id, field, r.values[i]}); err != nil {
					return err
				}
			}
		}
		for _, warning := range r.Warnings {
			if err := out.Write([]string{"warnings", "", "message", warning}); err != nil {
				return err
			}
		}
	} else {
		var empty any
		switch section {
		case "system":
			empty = SystemInfo{}
		case "servers":
			empty = ServerInfo{}
		case "users":
			empty = UserInfo{}
		case "storage":
			empty = StorageInfo{}
		case "licenses":
			empty = LicenseInfo{}
		}
		header, err := record(section, "", empty)
		if err != nil {
			return err
		}
		if err := out.Write(header.fields); err != nil {
			return err
		}
		for _, r := range records {
			if err := out.Write(r.values); err != nil {
				return err
			}
		}
	}
	out.Flush()
	return out.Error()
}

func displayStored(s string) string {
	if s == "" {
		return "Not set"
	}
	return terminalText(s)
}

func renderInventoryTable(w io.Writer, r Report, section string) error {
	records, err := inventoryRecords(r, section)
	if err != nil {
		return err
	}
	for _, warning := range r.Warnings {
		if _, err := fmt.Fprintln(w, "Note:", terminalText(warning)); err != nil {
			return err
		}
	}
	last := ""
	for _, item := range records {
		if item.kind == "cameras" {
			continue
		}
		if last != item.kind {
			if _, err := fmt.Fprintln(w, "\n"+strings.ToUpper(item.kind)); err != nil {
				return err
			}
			last = item.kind
			if item.kind == "users" {
				if err := renderUsersTable(w, r.Users); err != nil {
					return err
				}
			}
		}
		if item.kind == "users" {
			continue
		}
		t := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
		deactivationEligible := ""
		for i, field := range item.fields {
			if field == "deactivation_eligible" {
				deactivationEligible = item.values[i]
			}
		}
		for i, field := range item.fields {
			value := item.values[i]
			if field == "activations_remaining" && value == "" {
				value = "Unavailable from database"
			}
			if strings.HasPrefix(field, "deactivation") && value == "" {
				value = "Unknown"
				if field == "deactivations_remaining" && deactivationEligible == "false" {
					value = "Not applicable"
				}
			}
			if strings.HasSuffix(field, "_bytes") && value != "" {
				if n := storedInt(value); n != nil && *n >= 0 {
					value = fmt.Sprintf("%.2f GiB (%d bytes)", float64(*n)/(1<<30), *n)
				}
			}
			if _, err := fmt.Fprintf(t, "%s\t%s\n", strings.ReplaceAll(field, "_", " "), displayStored(value)); err != nil {
				return err
			}
		}
		if err := t.Flush(); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
	}
	if len(records) == 0 {
		_, err = fmt.Fprintln(w, "No records available.")
	}
	return err
}

func renderUsersTable(w io.Writer, users []UserInfo) error {
	t := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(t, "ACCOUNT\tTYPE\tDISPLAY NAME\tROLE\tEMAIL\tENABLED"); err != nil {
		return err
	}
	for _, u := range users {
		values := []string{u.Account, u.Type, u.DisplayName, u.Role, u.Email, nullableBool(u.Enabled)}
		for i := range values {
			values[i] = displayStored(values[i])
		}
		if _, err := fmt.Fprintln(t, strings.Join(values, "\t")); err != nil {
			return err
		}
	}
	return t.Flush()
}
