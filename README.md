# nx-camera-info

A standalone command-line tool for listing camera information and recovering saved camera credentials from an Nx Witness `ecs.sqlite` database. It runs locally in the Linux server's shell. It does not require Nx Powertoys, Python, an installed SQLite command, or a running Nx server.

## Run

From this directory:

```sh
# Camera inventory, with passwords masked.
./dist/nx-camera-info --db ecs.sqlite

# Show saved camera usernames and passwords.
./dist/nx-camera-info --db ecs.sqlite --show-passwords

# Export all available camera details, including passwords.
./dist/nx-camera-info --db ecs.sqlite --show-passwords --format csv --output camera-export.csv
./dist/nx-camera-info --db ecs.sqlite --show-passwords --format json --output camera-export.json

# Match a name, IP/hostname, UUID, vendor, model, MAC, group, logical ID or server.
./dist/nx-camera-info --db ecs.sqlite --filter Hikvision --show-passwords

# Counts only; include desktop camera records when needed.
./dist/nx-camera-info --db ecs.sqlite --summary
./dist/nx-camera-info --db ecs.sqlite --include-virtual --summary --format json
```

The default database path is `/opt/networkoptix/mediaserver/var/ecs.sqlite`. Use `--db ecs.sqlite` to read the copy in the current directory, as in the examples above. There is no automatic fallback to a local file.

`--help` (or `-h`) explains every option and includes usage examples without requiring a database. If the selected database does not exist, the tool shows the attempted path, guidance for `--db`, and the full help text, then exits with code `1`. All options also accept a single leading dash.

Copy `dist/nx-camera-info` to an x86-64 Linux Nx server and use the default database path:

```sh
chmod +x ./nx-camera-info
./nx-camera-info --help
sudo ./nx-camera-info --show-passwords
```

That path is the standard Network Optix Linux location; OEM installations may differ. `sudo` is only needed if your account cannot read the database. This is a separate shell executable, not a command registered inside Nx Witness.

## Output

The table shows name, host, username, password, vendor, model, assigned server, and credential recovery status. JSON and CSV also contain:

- Camera UUID, parsed IP address, URL port, original device URL, MAC, serial number, firmware, physical ID, logical ID and group.
- Server UUID, display name, URL and Nx version.
- Stored HTTP/media ports, ONVIF device URL, stream URLs and driver class.
- Stored status code, recording schedule flag and audio flag, where present.
- Credential source, password-presence flag, decode errors and metadata warnings.

`host` may be an IP address or hostname. `ip` is populated only when the saved host is a literal IP; the program does not perform DNS lookups. `port` comes from the URL or its scheme's default. `http_port` and `media_port` are the stored override properties, which can be empty.

Passwords are masked by default. The masked view also removes URL userinfo, query strings and fragments; unusual unparseable URLs are suppressed. URL paths remain visible. `--show-passwords` reveals passwords and complete URLs. A blank password with `has_password: false` is distinct from a missing credential record or a decode error. CSV preserves exact field values; use a text-aware import if opening it in spreadsheet software.

`--output` creates a new file with Unix permissions `0600` and refuses to overwrite an existing file or symlink. Shell redirection uses your shell's permissions instead. JSON has `summary` and `cameras` members; summary-only JSON contains just the summary object. Command diagnostics go to stderr, leaving stdout suitable for piping.

Exit codes: `0` successful read (including no matches or missing credentials); `1` argument, database or output error; `2` results emitted but at least one selected camera's credentials could not be decoded.

## Findings from the supplied database

- SQLite integrity check passed.
- 36 camera records: 31 network camera/RTSP records and 5 virtual desktop records.
- Two server records, both reporting Nx Witness `6.1.2.42921`.
- Effective stored credentials recovered for all 31 network camera records: 20 use `credentials`, and 11 fall back to `defaultCredentials`.
- All 38 stored credential fields decoded structurally: 20 explicit and 18 default entries. Seven cameras have both fields; the explicit field takes precedence.

These are saved credentials from this database snapshot. They have not been tested against the cameras, and the saved device addresses or passwords may have changed. The program makes no network connections and does not alter camera or Nx configuration. Virtual desktop records are omitted unless `--include-virtual` is supplied.

## Database and format support

The tool joins `vms_camera` to `vms_resource`, user attributes, server records and status records, and reads a selected set of camera properties from `vms_kvpair`. It opens SQLite with `mode=ro` and `query_only`, and reads within one transaction. It does not use `immutable=1`, so committed changes in an existing WAL are respected. SQLite may create or use shared-memory sidecars for a WAL database even when the database connection is read-only.

For an offline copy, use an Nx database backup or a consistent SQLite backup. Copying only `ecs.sqlite` while the server is writing can omit committed data in `ecs.sqlite-wal`. Do not remove active WAL/SHM files. Opening the live database requires appropriate access to any sidecars as well as the database file.

Credential fields in the tested database are hex-encoded AES-128-CBC with the fixed key and IV in Network Optix's published source and zero-byte padding. The decoded data is `username:password`, split at the first colon. The implementation rejects invalid lengths, padding and UTF-8 rather than presenting undecodable bytes as a password. This format has no cryptographic authentication; structural validation does not prove a password is current or that the database has not been altered.

An explicit `credentials` property takes precedence even if empty or undecodable. Only an absent property falls back to `defaultCredentials`, matching Nx's published camera logic. Conflicting duplicate properties fail with an error. These are camera credentials; this program does not recover Nx user account passwords from account hashes or dump server authentication keys.

Tested against the supplied Nx 6.1.2 schema. Other versions may use compatible tables, but are not claimed as tested. Unsupported schema or credential formats produce explicit errors. Stored status codes and schedule flags describe the snapshot, not live reachability or proof of current recording.

Format and schema references:

- [Network Optix's AES encoding implementation](https://github.com/networkoptix/nx_open/blob/96b23e2661065723711bc00bb7e7cceade3acb0d/libs/nx_utils/src/nx/utils/crypt/symmetrical.cpp).
- [Network Optix's camera credential selection and parsing](https://github.com/networkoptix/nx_open/blob/96b23e2661065723711bc00bb7e7cceade3acb0d/vms/libs/nx_vms_common/src/core/resource/camera_resource.cpp).
- [Network Optix's database documentation](https://support.networkoptix.com/hc/en-us/articles/115013837268-Media-Server-Database-Files).
- [Nx Powertoys camera password feature](https://nxpt.io/).

## Build and test

Requires Go 1.26 or later to build. The SQLite dependency is pinned in `go.mod` and `go.sum`; the first build may download dependencies. The compiled program uses Go's AES implementation and a pure-Go SQLite driver, so it needs neither OpenSSL nor libc at runtime when built with these targets.

```sh
make build          # dist/nx-camera-info for the build machine
make check          # go vet and race-enabled tests
make linux-amd64    # dist/nx-camera-info-linux-amd64
make linux-arm64    # dist/nx-camera-info-linux-arm64
```

Tests use synthetic data and independently generated OpenSSL test vectors. They cover credential precedence, Unicode and colon-containing passwords, corrupt formats, masked URLs, read-only enforcement, committed WAL reads, filename URI escaping, metadata joins, filters, CSV/JSON, output permissions and overwrite refusal. User databases and exported credentials are excluded from Git.

Validation completed: `go vet` and race-enabled tests passed. The Linux x86-64 binary ran against the supplied database; its 31 effective credential pairs matched an independent OpenSSL decoding check, CSV and JSON exports agreed, and the original database's SHA-256 remained unchanged. The Linux ARM64 binary was cross-compiled and identified as a static AArch64 ELF executable, but has not been run on ARM hardware. Both binaries are statically linked. No actual camera login or remote Nx server execution was attempted.

Third-party notices are included in `THIRD_PARTY_NOTICES.txt` and copied into `dist/` by the build targets. Keep them with redistributed binaries.
