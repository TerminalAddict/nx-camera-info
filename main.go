package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
	"unicode"
)

const (
	version             = "0.1.1"
	defaultDatabasePath = "/opt/networkoptix/mediaserver/var/ecs.sqlite"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	var opts Options
	flags := flag.NewFlagSet("nx-camera-info", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&opts.DB, "db", defaultDatabasePath, "path to an Nx Witness ecs.sqlite database (read only)")
	flags.StringVar(&opts.Format, "format", "table", "output format: table, json, csv; JSON and CSV include additional camera details")
	flags.StringVar(&opts.Output, "output", "", "write to a new file with permissions 0600; never overwrite (default: stdout)")
	flags.StringVar(&opts.Filter, "filter", "", "case-insensitive match on name, host/IP, UUID, vendor, model, MAC, group, logical ID or server")
	flags.BoolVar(&opts.ShowPasswords, "show-passwords", false, "reveal saved passwords and complete URLs")
	flags.BoolVar(&opts.IncludeVirtual, "include-virtual", false, "include virtual desktop camera records")
	flags.BoolVar(&opts.SummaryOnly, "summary", false, "show counts and server versions only; supports table or json, not csv")
	showVersion := flags.Bool("version", false, "print program version")
	showHelp := flags.Bool("help", false, "show all options and usage examples; no database required")
	flags.BoolVar(showHelp, "h", false, "alias for --help")
	flags.Usage = func() {
		out := flags.Output()
		fmt.Fprintln(out, "nx-camera-info: offline camera inventory and credential recovery for Nx Witness")
		fmt.Fprintln(out, "Usage: nx-camera-info [options]")
		fmt.Fprintln(out, "\nWith no options, reads the default database and displays a table with passwords masked.")
		fmt.Fprintln(out, "Virtual desktop cameras are omitted unless --include-virtual is supplied.")
		fmt.Fprintln(out, "\nOptions (both --option and -option are accepted):")
		flags.PrintDefaults()
		fmt.Fprint(out, `
Examples:
  nx-camera-info
  nx-camera-info --show-passwords
  nx-camera-info --show-passwords --format csv --output camera-export.csv
  nx-camera-info --show-passwords --format json --output camera-export.json
  nx-camera-info --filter Hikvision --show-passwords
  nx-camera-info --include-virtual --summary --format json
  nx-camera-info --db ./ecs.sqlite --show-passwords
  nx-camera-info --help

The table shows camera names, hosts, usernames, passwords, vendors, models,
assigned servers and credential recovery status. JSON and CSV also include
UUIDs, ports, MAC addresses, serial numbers, firmware, device/stream URLs,
server details, stored status codes, schedule/audio flags and credential sources.
Without --show-passwords, passwords and URL userinfo/query/fragment are masked.

Reads the database without changing its contents. Does not contact cameras or
validate logins. Use --db PATH to read a backup or a different installation.
Errors and recovery counts go to stderr; results go to stdout or --output.
Exit codes: 0 = success; 1 = input/output error; 2 = partial credential recovery.
`)
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}
	if *showHelp {
		flags.SetOutput(stdout)
		flags.Usage()
		return 0
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "error: unexpected positional arguments; use --db PATH")
		return 1
	}
	if *showVersion {
		fmt.Fprintln(stdout, "nx-camera-info "+version)
		return 0
	}
	if opts.Format != "table" && opts.Format != "json" && opts.Format != "csv" {
		fmt.Fprintln(stderr, "error: --format must be table, json or csv")
		return 1
	}
	if opts.SummaryOnly && opts.Format == "csv" {
		fmt.Fprintln(stderr, "error: --summary supports table or json output")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	report, err := readReport(ctx, opts)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(stderr, "error: database not found at %q.\n", opts.DB)
			fmt.Fprintln(stderr, "Specify its location with --db PATH, for example: nx-camera-info --db ./ecs.sqlite")
			fmt.Fprintln(stderr)
			flags.Usage()
			return 1
		}
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	var output bytes.Buffer
	if err := render(&output, report, opts); err != nil {
		fmt.Fprintln(stderr, "error formatting output:", err)
		return 1
	}
	if opts.Output != "" {
		err = writeNewFile(opts.Output, output.Bytes())
	} else {
		_, err = stdout.Write(output.Bytes())
	}
	if err != nil {
		fmt.Fprintln(stderr, "error writing output:", err)
		return 1
	}
	if !opts.SummaryOnly {
		fmt.Fprintf(stderr, "%d cameras; %d recovered, %d empty, %d without stored credentials, %d decode errors; %d virtual records omitted.\n",
			report.Summary.Matched, report.Summary.Recovered, report.Summary.Empty,
			report.Summary.Missing, report.Summary.DecodeErrors, report.Summary.VirtualOmitted)
	}
	if report.Summary.DecodeErrors != 0 {
		return 2
	}
	return 0
}

func writeNewFile(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(data)
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(path)
		return errors.Join(writeErr, closeErr)
	}
	return nil
}

func render(w io.Writer, report Report, opts Options) error {
	if opts.Format == "json" {
		encoder := json.NewEncoder(w)
		encoder.SetIndent("", "  ")
		if opts.SummaryOnly {
			return encoder.Encode(report.Summary)
		}
		return encoder.Encode(report)
	}
	if opts.SummaryOnly {
		s := report.Summary
		_, err := fmt.Fprintf(w, "Camera records: %d\nVirtual records omitted: %d\nMatched cameras: %d\nCredentials recovered: %d\nEmpty credentials: %d\nCredentials not stored: %d\nDecode errors: %d\nNx server versions: %s\n",
			s.TotalRecords, s.VirtualOmitted, s.Matched, s.Recovered, s.Empty, s.Missing, s.DecodeErrors, strings.Join(s.ServerVersions, ", "))
		return err
	}
	if opts.Format == "csv" {
		return renderCSV(w, report.Cameras)
	}
	table := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(table, "NAME\tHOST\tUSERNAME\tPASSWORD\tVENDOR\tMODEL\tSERVER\tCREDENTIALS"); err != nil {
		return err
	}
	for _, cam := range report.Cameras {
		values := []string{cam.Name, cam.Host, cam.Username, cam.Password, cam.Vendor, cam.Model, cam.ServerName, cam.CredentialStatus}
		for i, value := range values {
			values[i] = terminalText(value)
		}
		if _, err := fmt.Fprintln(table, strings.Join(values, "\t")); err != nil {
			return err
		}
	}
	return table.Flush()
}

func terminalText(value string) string {
	if strings.ContainsFunc(value, func(r rune) bool { return !unicode.IsPrint(r) }) {
		return strconv.QuoteToASCII(value)
	}
	return value
}

func renderCSV(w io.Writer, cameras []Camera) error {
	out := csv.NewWriter(w)
	if err := out.Write([]string{"id", "name", "host", "ip", "port", "url", "username", "password", "has_password",
		"credential_source", "credential_status", "credential_error", "vendor", "model", "mac", "serial",
		"firmware", "physical_id", "logical_id", "group", "server_id", "server_name", "server_url",
		"server_version", "http_port", "media_port", "onvif_url", "stream_urls", "driver", "virtual",
		"stored_status_code", "schedule_enabled", "audio_enabled", "warnings"}); err != nil {
		return err
	}
	for _, c := range cameras {
		streams, err := json.Marshal(c.StreamURLs)
		if err != nil {
			return err
		}
		if err := out.Write([]string{c.ID, c.Name, c.Host, c.IP, c.Port, c.URL, c.Username, c.Password, strconv.FormatBool(c.HasPassword),
			c.CredentialSource, c.CredentialStatus, c.CredentialError, c.Vendor, c.Model, c.MAC, c.Serial,
			c.Firmware, c.PhysicalID, c.LogicalID, c.Group, c.ServerID, c.ServerName, c.ServerURL,
			c.ServerVersion, c.HTTPPort, c.MediaPort, c.ONVIFURL, string(streams), c.Driver, strconv.FormatBool(c.Virtual),
			nullableInt(c.StatusCode), nullableBool(c.ScheduleEnabled), nullableBool(c.AudioEnabled), strings.Join(c.Warnings, "; ")}); err != nil {
			return err
		}
	}
	out.Flush()
	return out.Error()
}
