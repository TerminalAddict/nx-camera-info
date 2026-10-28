<p align="center"><img src="branding/icon.svg" width="128" height="128" alt="NX Camera Info CCTV camera and information icon"></p>

# NX Camera Info

A desktop GUI and standalone command-line tool for listing camera information and recovering saved camera credentials from an Nx Witness `ecs.sqlite` database. Both run on Linux, Windows, and macOS, on Intel/AMD 64-bit and ARM64. Neither requires Nx Powertoys, Python, an installed SQLite command, or a running Nx server.

## Desktop GUI

The Wails v3 desktop application provides native Open/Save dialogs and menus, system light/dark appearance, and platform-adapted controls. Open a database and browse **Cameras**, **Users**, **System & servers**, **Storage**, and **Licences**. Filter cameras, reveal or hide camera passwords, inspect stored details, and export a full report or an individual section as CSV or JSON. The same reports are available on every supported GUI and CLI platform.

![NX Camera Info GUI on Linux, showing synthetic camera inventory](docs/screenshots/gui-linux-inventory.png)

Get the installers from [release v0.5.0](https://github.com/TerminalAddict/nx-camera-info/releases/tag/v0.5.0). See the [GUI installation and usage guide](docs/GUI.md) for dependencies, screenshots, keyboard shortcuts, and build instructions.

The [project branding](docs/BRANDING.md) uses the same CCTV/info icon throughout the app, desktop launchers, and installers.

**About & updates** shows the version, platform, and a short introduction to creator [Paul Willard](https://paulwillard.nz/). Check GitHub for a newer release and download the matching installer inside the app, with progress and SHA-256 verification. After downloading, quit the app and run the installer. See [About and updates](docs/GUI.md#about-and-updates) for details.

| System | GUI installer / package |
| --- | --- |
| Linux Intel/AMD 64-bit | `nx-camera-info-gui-0.5.0-linux-amd64.deb`, `.rpm`, or `.tar.gz` |
| Linux ARM64 | `nx-camera-info-gui-0.5.0-linux-arm64.deb`, `.rpm`, or `.tar.gz` |
| Windows Intel/AMD 64-bit | `nx-camera-info-gui-0.5.0-windows-amd64-setup.exe` |
| Windows ARM64 | `nx-camera-info-gui-0.5.0-windows-arm64-setup.exe` |
| macOS Intel | `nx-camera-info-gui-0.5.0-macos-amd64.pkg` or `.dmg` |
| macOS Apple Silicon | `nx-camera-info-gui-0.5.0-macos-arm64.pkg` or `.dmg` |

The **About & updates** page can download, verify and launch an update with **Install and close**, then close the app automatically. Windows offers to reopen the app after setup; macOS uses its PKG installer, and Linux shows installation progress in a terminal. See the [update guide](docs/GUI.md#about-and-updates) for each package type and the one-time upgrade from older releases.

Windows installers include Microsoft's full offline WebView2 installer. Linux DEB/RPM packages declare GTK4/WebKitGTK dependencies for the package manager to install; the archive includes a dependency-install script. macOS uses the built-in WebKit framework. These builds are not publisher-signed or Apple-notarized; see the guide for first-launch instructions.

## Command-line downloads

Get the executable for your computer from the [latest release](https://github.com/TerminalAddict/nx-camera-info/releases/latest):

| System | Executable |
| --- | --- |
| Linux Intel/AMD 64-bit | `nx-camera-info-linux-amd64` |
| Linux ARM64 | `nx-camera-info-linux-arm64` |
| Windows Intel/AMD 64-bit | `nx-camera-info-windows-amd64.exe` |
| Windows ARM64 | `nx-camera-info-windows-arm64.exe` |
| macOS Intel | `nx-camera-info-macos-amd64` |
| macOS Apple Silicon | `nx-camera-info-macos-arm64` |

Download `THIRD_PARTY_NOTICES.txt` alongside the executable. `SHA256SUMS` contains checksums for the release files. The macOS binaries are not Developer ID-signed or notarized.

On Windows, open PowerShell in the download directory:

```powershell
.\nx-camera-info-windows-amd64.exe --help
.\nx-camera-info-windows-amd64.exe --db "C:\Camera backups\ecs.sqlite"
.\nx-camera-info-windows-amd64.exe --db "C:\Camera backups\ecs.sqlite" --show-passwords --format csv --output camera-export.csv
```

Use the `windows-arm64.exe` executable instead on Windows ARM64. Exported files inherit Windows directory permissions; choose a private directory when exporting passwords.

On an Apple Silicon Mac, open Terminal in the download directory:

```sh
chmod +x ./nx-camera-info-macos-arm64
./nx-camera-info-macos-arm64 --help
./nx-camera-info-macos-arm64 --db "$HOME/Camera backups/ecs.sqlite"
```

Use `nx-camera-info-macos-amd64` instead on an Intel Mac. On Windows and macOS, the default database is `ecs.sqlite` in the current directory; use `--db` to select another file. A consistent database backup copied from your Nx server can be read on either system.

## Back up Nx Cloud sites

[scripts/backup_cloud_sites.py](scripts/backup_cloud_sites.py) is a standalone Python script for downloading site configuration backups through **nxvms.com**. Supply a CSV of separate cloud accounts, or log into one account interactively. Run it once or whenever you want new backups. It requires **Python 3.10 or newer**, with no third-party packages or `pip install` step. It works independently of the GUI and compiled CLI. Direct server URLs are not needed: each account's sites are discovered through Nx Cloud and reached through its HTTPS relay.

**Permission limit:** your cloud account needs backup administrator/owner rights on each site. **Power User access on a shared site does not grant database-backup permission.** The script checks shared sites too, reports refused requests, and continues with other sites. It cannot elevate your permissions and does not ask for local administrator credentials. Arrange backups for those sites separately with their owners or an authorised administrator.

For multiple cloud accounts, copy [scripts/cloud-accounts.example.csv](scripts/cloud-accounts.example.csv) to **`tmp/cloud-accounts.csv`**, then replace the dummy entries in that copy. Keep real credentials in the ignored `tmp/` directory, not in the example file. Use this header:

```csv
name,user,password
Example Warehouse,warehouse@example.invalid,DUMMY-password-change-me-1
Example Office,office@example.invalid,DUMMY-password-change-me-2
```

`name` labels the account in progress/error messages; `user` is its **Nx Cloud email**; `password` is that cloud account's password. The script discovers every accessible site for each account, including owned, shared and organisation-provided sites. Filenames use the **site name returned by Nx Cloud**, which can differ from the CSV label; an account may have several sites.

Save the CSV as UTF-8 (a UTF-8 BOM is also accepted). Header case and surrounding header spaces are ignored. Account names and emails are trimmed; **passwords are preserved exactly**, including leading/trailing spaces. Do not pad password values with spaces for alignment. Quote a field containing a comma, newline or double quote, doubling any embedded quotes; the example file includes a password containing a comma. Blank data rows are ignored.

From the repository directory on Windows, run this in PowerShell:

```powershell
py -3 scripts/backup_cloud_sites.py --accounts-csv tmp/cloud-accounts.csv
```

On Linux or macOS:

```sh
python3 scripts/backup_cloud_sites.py --accounts-csv tmp/cloud-accounts.csv
```

**CSV mode runs without prompts.** It creates a new dated output folder under `NX Camera Backups` in your home directory unless `--output-dir` is supplied. It signs into each cloud account, downloads permitted backups, revokes that account's sessions, then continues to the next row. Passwords and tokens are not printed or copied into reports. No two-factor code is needed for accounts without 2FA; if Nx unexpectedly requires one, that account/site is reported as failed rather than pausing the batch. Interactive mode remains available for such accounts.

Invalid individual rows are reported by line number and skipped. Bad passwords, cloud login failures, discovery errors, denied backup requests and unavailable sites do not stop later accounts. A malformed header, invalid UTF-8 or broken CSV quoting stops the run **before any account login**, because the file cannot be reliably parsed. Successfully downloaded sites are deduplicated by cloud site ID across all accounts. If one account cannot back up a site, another account that can access it gets its own attempt; a later success resolves that site's earlier failure in the final summary.

For a single interactive account, omit `--accounts-csv`:

```sh
python3 scripts/backup_cloud_sites.py
```

Use `py -3` instead of `python3` on Windows. Interactive mode prompts for:

1. **Backup directory.** Press Enter to create a new dated folder under `NX Camera Backups` in your home directory. This keeps successive runs separate and places backups outside the repository by default.
2. **Nx Cloud email and password.** Password input is hidden. Credentials and tokens stay in memory for this run; they are not saved in a configuration file or printed.
3. **Authenticator code**, when Nx requires two-factor authentication. The code is hidden too. Fresh authentication is obtained for each site, so Nx may require another code during a run.

For each account, it explicitly queries the default/owned list, directly shared sites, and organisation-provided sites, then deduplicates them by cloud site ID. If a discovery request fails, that account is marked failed rather than claiming an incomplete list contains all its sites; CSV mode continues with the next account. The server's response to the backup request is the authoritative permission check; a cloud role label alone is not used to exclude a site.

Successful downloads use the site name, for example `Warehouse.db`. Characters invalid in Windows filenames are replaced. Filename collisions receive a site-ID suffix, including when different accounts discover distinct sites with the same name. **Existing files are never overwritten**, including when you explicitly reuse an output directory; they are reported as skipped. Completed backups survive a later account/site failure or cancellation.

To choose a directory on the command line or allow more time for slow sites:

```powershell
py -3 scripts/backup_cloud_sites.py --accounts-csv tmp/cloud-accounts.csv --output-dir "C:\Camera backups\October" --timeout 300
```

```sh
python3 scripts/backup_cloud_sites.py --accounts-csv tmp/cloud-accounts.csv --output-dir "$HOME/Camera backups/October" --timeout 300
python3 scripts/backup_cloud_sites.py --help
```

`--timeout` is the network inactivity timeout in seconds (default `120`), not a limit on the whole run. The script tries the current `/rest/v4/site/database` endpoint, then earlier `/rest/v3`, `/rest/v2`, `/rest/v1` system/database endpoints and `/ec2/dumpDatabase` only when an endpoint is unavailable. Permission denials do not trigger attempts to bypass the restriction. No restore, site configuration change, or video download is performed. Login sessions are revoked on normal exit where possible; a failed revocation produces a warning.

Cloud and site authentication use separate tokens: a cloud token handles discovery, two-factor confirmation and token revocation, while each backup uses a fresh token scoped only to `cloudSystemId=<site ID>`. Nx rejects a specific site ID combined with cloud API access in the same token request. Failures identify whether the cloud login, site-token request or a particular backup endpoint failed, without printing credentials or raw server error bodies.

For a manual database backup, connect through the **Nx Witness desktop client**, then open **System/Site Administration → Advanced → Create Backup**. The nxvms.com site page does not necessarily expose that desktop control. Seeing a site in the portal confirms access to that site; its server still checks backup privileges separately. See [Nx's manual backup instructions](https://support.networkoptix.com/hc/en-us/articles/221631188-Back-up-and-Restore-VMS-Server-Data).

There is one configuration backup per **site**, covering its servers, cameras, users, rules and other site settings; recorded video requires a separate backup. Raw binary dumps and JSON/base64 database responses are accepted. SQLite downloads receive a read-only integrity check and can then be opened in NX Camera Info if their schema is supported. Other Nx binary formats are retained as `.db` backups and explicitly reported as unverified for SQLite compatibility. Empty/truncated transfers, HTML error pages and invalid database payloads are rejected. The download limit is 512 MiB; JSON/base64 responses have a 128 MiB decoding limit.

Backups can contain camera credentials, user password hashes and server authentication keys. Use a private destination. Files use mode `0600` on Linux/macOS; Windows files inherit the destination directory's access permissions. TLS verification stays enabled, and bearer tokens follow only recognised HTTPS relay redirects for the same site. Incomplete downloads are removed on handled failures/cancellation; a forced termination may leave a hidden `.nx-backup-*.partial` file. Both `.db` files and these partial downloads are ignored by Git.

Exit codes: `0` all discovered sites saved, or no sites found; `1` setup/CSV parsing failure or interactive login/discovery failure; `2` at least one CSV account/row failed, a site remained unbacked-up, or an existing file was skipped; `130` cancelled with Ctrl+C or end of input. A site discovered again after a successful backup is skipped without causing an error. The final summary reports unique saved/skipped/failed sites and, in CSV mode, account failures and invalid rows. Earlier failed download attempts remain visible even if another account successfully backs up the site later.

Offline tests use synthetic databases and simulated cloud/server responses, including CSV quoting/validation, account failures, retries and deduplication across accounts, owned/shared discovery, denied Power User access, authentication, redirects and download failures:

```sh
python3 -B -m unittest discover -s scripts -p test_backup_cloud_sites.py -v
```

These tests do not authenticate to real accounts. A separate live verification successfully downloaded and integrity-checked a database backup from Nx Witness **6.1.2.42921** through the cloud relay using a cloud administrator account. Other server versions and account configurations still need live testing. API references: [Nx Cloud API](https://nxvms.com/cdb/docs/api/v1/swagger/index.html), [cloud OAuth scopes](https://support.networkoptix.com/hc/en-us/articles/32896214893079-OAuth-API-specification), [database backup API](https://support.networkoptix.com/hc/en-us/articles/32895784410775-Back-up-and-restore-a-system-database-with-the-REST-API), and [Nx's confirmation of cloud-relay backup support](https://support.networkoptix.com/hc/en-us/community/posts/40819265345815-Cloud-Auth-Bearer-token-getting-Auth-MissingCredentials-on-database-backup-endpoint-even-for-Owner?sort_by=votes).

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

On Linux, the default database path is `/opt/networkoptix/mediaserver/var/ecs.sqlite`. Use `--db ecs.sqlite` to read the copy in the current directory, as in the examples above. There is no automatic fallback from the Linux server path to a local file.

`--help` (or `-h`) explains every option and includes usage examples without requiring a database. If the selected database does not exist, the tool shows the attempted path, guidance for `--db`, and the full help text, then exits with code `1`. All options also accept a single leading dash.

Copy `dist/nx-camera-info` (or the downloaded `nx-camera-info-linux-amd64`, renamed to `nx-camera-info`) to an x86-64 Linux Nx server and use the default database path:

```sh
chmod +x ./nx-camera-info
./nx-camera-info --help
sudo ./nx-camera-info --show-passwords
```

That path is the standard Network Optix Linux location; OEM installations may differ. `sudo` is only needed if your account cannot read the database. This is a separate shell executable, not a command registered inside Nx Witness.

## Output

The default report includes all sections. Use `--section cameras|users|system|servers|storage|licenses` to select one, or `--section all` for the full report. Camera filters affect only the camera section; users, servers, storage and licences remain complete.

```sh
nx-camera-info --db ecs.sqlite --section users
nx-camera-info --db ecs.sqlite --section servers
nx-camera-info --db ecs.sqlite --section licenses --format json
nx-camera-info --db ecs.sqlite --format json --output full-report.json
nx-camera-info --db ecs.sqlite --format csv --output full-report.csv
nx-camera-info --db ecs.sqlite --section cameras --format csv --output cameras.csv
nx-camera-info --db ecs.sqlite --section users --format csv --output users.csv
```

- **Users:** account, local/cloud/LDAP type, display name, role, email, enabled status, group IDs and inherited groups. Built-in Nx 6.1 roles are resolved even when they have no row in the custom-group table. Raw direct permissions are also available; a zero value does not mean no access because permissions can be inherited. User password hashes are not exported.
- **System and servers:** system name and IDs, cloud account, latest stored transaction, server names/IDs, main and discovered addresses (including ignored discovery entries), Nx version and full build, OS, architecture, CPU, RAM, time zone, disk/interface descriptions and selected settings. Discovery addresses can be historical. Servers without cameras are included.
- **Storage:** every storage row, its server, path, type, recording/backup flags, stored space and space-limit values in bytes, archive mode, status flags and selected storage properties. These are saved values, not live free space, recording health or a guarantee of archive retention.
- **Licences:** full keys, serials, channel counts, class, version, brand, hardware ID, expiry field, company/support fields, all other licence-block fields and the original block. Blank expiry is reported as not set, not a verified validity result. Keys are included without needing `--show-passwords`.

**Remaining licence deactivations follow Nx 6.1.2's calculation:** `max(0, 3 - DEACTIVATIONS)`. The stored `DEACTIVATIONS` value counts deactivations already used; when absent, Nx defaults to zero used and three remaining. The report includes `deactivations_used`, `deactivations_remaining`, `deactivation_eligible` (based on licence type/expiry), and `deactivation_information`. For example, `DEACTIVATIONS=1` produces two remaining, regardless of licensed channel count. These values reflect the database snapshot and are not verified online. This matches the published Nx [calculation and eligibility rules](https://github.com/networkoptix/nx_open/blob/vms_6.1.2/vms/libs/nx_vms_common/src/licensing/license.cpp) and [defaults](https://github.com/networkoptix/nx_open/blob/vms_6.1.2/vms/libs/nx_vms_common/src/licensing/license.h).

NVR and time-limited licences show **Not applicable** for remaining deactivations. Invalid counters or missing class/expiry fields show **Unknown**, with an explanation. These remaining values are JSON `null` and empty CSV cells; the information field explains why. A count does not establish licence validity or guarantee that the licensing service will permit a deactivation.

**Remaining activations are a separate, unavailable value:** `activations_remaining` remains JSON `null` or an empty CSV cell, with an explanation in `activation_information`. Licensed channels (`COUNT`) are capacity, not remaining activations. The app does not send licence keys to a remote service.

The camera table shows name, host, username, password, vendor, model, assigned server, and credential recovery status. JSON and CSV also contain:

- Camera UUID, parsed IP address, URL port, original device URL, MAC, serial number, firmware, physical ID, logical ID and group.
- Server UUID, display name, URL and Nx version.
- Stored HTTP/media ports, ONVIF device URL, stream URLs and driver class.
- Stored status code, recording schedule flag and audio flag, where present.
- Credential source, password-presence flag, decode errors and metadata warnings.

`host` may be an IP address or hostname. `ip` is populated only when the saved host is a literal IP; the program does not perform DNS lookups. `port` comes from the URL or its scheme's default. `http_port` and `media_port` are the stored override properties, which can be empty.

Passwords are masked by default. The masked view also removes URL userinfo, query strings and fragments; unusual unparseable URLs are suppressed. URL paths remain visible. `--show-passwords` reveals passwords and complete URLs. A blank password with `has_password: false` is distinct from a missing credential record or a decode error. CSV preserves exact field values; use a text-aware import if opening it in spreadsheet software.

`--output` creates a new file and refuses to overwrite an existing file or symlink. On Linux and macOS it uses Unix permissions `0600`; on Windows it inherits the destination directory's permissions. Shell redirection uses your shell's permissions instead. Full JSON contains `summary`, `system`, `servers`, `users`, `storage`, `licenses`, `cameras`, and optional `warnings`. Section JSON contains the chosen section, summary and warnings; summary-only JSON contains just inventory counts and server versions.

Full-report CSV uses four columns: `record_type,record_id,field,value`, allowing every section and nested metadata to be included in one valid CSV file. Single-section CSV has one row per record; nested objects/arrays are JSON in quoted cells. **Compatibility:** use `--section cameras --format csv` for the original 34-column camera CSV. This is required for scripts that relied on the pre-0.4 default camera-only CSV. Command diagnostics go to stderr, leaving stdout suitable for piping.

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

Tested against the supplied Nx 6.1.2 schema. Other versions may use compatible tables, but are not claimed as tested. Missing optional inventory tables generate compatibility notes while retaining camera access; absent optional fields remain unknown. Unsupported camera schema or credential formats produce explicit errors. Stored status codes and schedule flags describe the snapshot, not live reachability or proof of current recording.

Format and schema references:

- [Network Optix's AES encoding implementation](https://github.com/networkoptix/nx_open/blob/96b23e2661065723711bc00bb7e7cceade3acb0d/libs/nx_utils/src/nx/utils/crypt/symmetrical.cpp).
- [Network Optix's camera credential selection and parsing](https://github.com/networkoptix/nx_open/blob/96b23e2661065723711bc00bb7e7cceade3acb0d/vms/libs/nx_vms_common/src/core/resource/camera_resource.cpp).
- [Network Optix's database documentation](https://support.networkoptix.com/hc/en-us/articles/115013837268-Media-Server-Database-Files).
- [Nx Powertoys camera password feature](https://nxpt.io/).

## CLI build and test

Requires Go 1.26 or later to build. The SQLite dependency is pinned in `go.mod` and `go.sum`; the first build may download dependencies. The compiled CLI uses Go's AES implementation and a pure-Go SQLite driver, so no separate OpenSSL or SQLite installation is needed. All CLI targets disable CGO; the Linux CLI executables are statically linked, while Windows and macOS use standard system libraries. For the desktop application's native dependencies and build instructions, see the [GUI guide](docs/GUI.md#build-and-validate).

The Makefile targets run in a Unix-style shell with Make (the `release` target also needs `sha256sum`):

```sh
make build           # dist/nx-camera-info for this machine (.exe on Windows)
make check           # go vet and race-enabled tests
make linux-amd64     # dist/nx-camera-info-linux-amd64
make linux-arm64     # dist/nx-camera-info-linux-arm64
make windows-amd64   # dist/nx-camera-info-windows-amd64.exe
make windows-arm64   # dist/nx-camera-info-windows-arm64.exe
make macos-amd64     # dist/nx-camera-info-macos-amd64
make macos-arm64     # dist/nx-camera-info-macos-arm64
make release         # all six platforms, notices, and SHA256SUMS
```

To build natively in Windows PowerShell without Make:

```powershell
$env:CGO_ENABLED = "0"
go build -trimpath -ldflags="-s -w" -o dist/nx-camera-info.exe .
Copy-Item THIRD_PARTY_NOTICES.txt dist/
go test ./...
```

Tests use synthetic data and independently generated OpenSSL test vectors. They cover credential precedence, Unicode and colon-containing passwords, corrupt formats, masked URLs, read-only enforcement, committed WAL reads, filename URI escaping, metadata joins, filters, CSV/JSON, output permissions and overwrite refusal. User databases and exported credentials are excluded from Git.

GitHub Actions runs vet, tests, and executable help/version checks on Linux, Windows, and macOS for AMD64 and ARM64, plus race-enabled tests on Linux and macOS. Release binaries can be cross-compiled on Linux with `make release`.

Initial Linux validation against the supplied database recovered 31 effective credential pairs matching an independent OpenSSL decoding check; CSV and JSON exports agreed, and the original database's SHA-256 remained unchanged. No actual camera login or remote Nx server execution was attempted.

Third-party notices are included in `THIRD_PARTY_NOTICES.txt` and copied into `dist/` by the build targets. Keep them with redistributed binaries.
