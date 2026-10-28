#!/usr/bin/env python3
"""Download Nx Cloud site configuration backups. Python 3.10+, standard library only.

API references and usage are in README.md under "Back up Nx Cloud sites".
Credentials are entered interactively or read from a CSV, and never logged.
"""

from __future__ import annotations

import argparse
import base64
import binascii
from collections import Counter
from contextlib import closing, contextmanager
import csv
from dataclasses import dataclass
from datetime import datetime
import errno
import getpass
import http.client
import json
import math
import os
from pathlib import Path
import re
import shutil
import socket
import sqlite3
import ssl
import sys
import tempfile
import unicodedata
from urllib import error, parse, request
import uuid
import warnings


CLOUD = "https://nxvms.com"
JSON_LIMIT = 16 * 1024 * 1024
BACKUP_LIMIT = 512 * 1024 * 1024
BACKUP_JSON_LIMIT = 128 * 1024 * 1024
CHUNK_SIZE = 256 * 1024
BACKUP_PATHS = (
    "/rest/v4/site/database",
    "/rest/v3/system/database",
    "/rest/v2/system/database",
    "/rest/v1/system/database",
    "/ec2/dumpDatabase",
)
MFA_CODES = {"secondfactorrequired", "second_factor_required", "mfarequired", "mfa_required"}
FRESH_CODES = {"userpasswordrequired", "freshsessionrequired", "auth_sessionisnotfresh"}


class BackupError(Exception):
    """An error whose message is safe to display (no server bodies or tokens)."""


def error_code(data):
    if not isinstance(data, dict):
        return ""
    value = data.get("errorId") or data.get("error") or data.get("code", "")
    if isinstance(value, dict):
        value = value.get("code", "")
    return str(value).lower()


class ApiError(BackupError):
    def __init__(self, status, data=None):
        self.status = status
        self.code = error_code(data)
        if self.code in MFA_CODES:
            message = "Two-factor authentication is required."
        elif self.code in FRESH_CODES:
            message = "The server requires fresh password authentication."
        elif self.code in ("invalid_grant", "notauthorized", "badusername"):
            message = f"Authentication rejected (HTTP {status}); check the account email and password."
        elif self.code in ("accountblocked", "account_blocked"):
            message = "Nx has blocked this account; check its status in the cloud portal."
        elif self.code in ("accountnotactivated", "account_not_activated"):
            message = "This Nx Cloud account has not been activated."
        elif status == 401:
            message = "Authentication rejected (HTTP 401); check login and site access."
        elif status == 403:
            message = ("Permission denied (HTTP 403); backup administrator rights or "
                       "fresh authentication are required.")
        elif status == 429:
            message = "Nx rate limit reached (HTTP 429); try again later."
        elif status in (502, 503, 504):
            message = f"Site/relay unavailable or offline (HTTP {status})."
        else:
            message = f"Nx request failed (HTTP {status})."
        super().__init__(message)


def display(value):
    """Do not let site names print terminal escape/control sequences."""
    return "".join(c if not unicodedata.category(c).startswith("C") else "_"
                   for c in str(value))


def secret(prompt):
    # getpass normally falls back to visible stdin if no terminal is available.
    with warnings.catch_warnings():
        warnings.simplefilter("error", getpass.GetPassWarning)
        try:
            return getpass.getpass(prompt)
        except getpass.GetPassWarning:
            raise BackupError("Run this script in a terminal that supports hidden input.") from None


class NoRedirect(request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def trusted_url(url, site_id=None):
    """A bearer must never follow an arbitrary cross-host relay redirect."""
    try:
        parts = parse.urlsplit(url)
        if (parts.scheme != "https" or parts.username or parts.password
                or parts.port not in (None, 443) or parts.fragment):
            return False
        if site_id is None:
            return parts.hostname == "nxvms.com"
        return bool(re.fullmatch(
            re.escape(site_id) + r"\.relay(?:-[a-z0-9-]+)?\.vmsproxy\.com",
            parts.hostname or ""))
    except ValueError:
        return False


class Http:
    def __init__(self, timeout=120, opener=None):
        self.timeout = timeout
        self.opener = opener or request.build_opener(
            NoRedirect(), request.HTTPSHandler(context=ssl.create_default_context()))

    @contextmanager
    def open(self, url, *, method="GET", token=None, body=None, site_id=None,
             accept="application/json", timeout=None):
        payload = None if body is None else json.dumps(body).encode("utf-8")
        headers = {"Accept": accept, "Accept-Encoding": "identity",
                   "User-Agent": "NX-Camera-Info-Cloud-Backup/1"}
        if token:
            headers["Authorization"] = "Bearer " + token
        if payload is not None:
            headers["Content-Type"] = "application/json"
        response = None
        try:
            for _ in range(6):
                if not trusted_url(url, site_id):
                    raise BackupError("Refused an untrusted Nx URL or relay redirect.")
                req = request.Request(url, data=payload, headers=headers, method=method)
                try:
                    response = self.opener.open(req, timeout=self.timeout if timeout is None else timeout)
                except error.HTTPError as exc:
                    response = exc
                if response.status not in (301, 302, 303, 307, 308):
                    break
                location = response.headers.get("Location")
                status = response.status
                response.close()
                response = None
                if not location or (method != "GET" and status not in (307, 308)):
                    raise BackupError("Unexpected redirect from Nx.")
                url = parse.urljoin(url, location)
            else:
                raise BackupError("Too many Nx relay redirects.")
            yield response
        except ssl.SSLError:
            raise BackupError("TLS verification/connection failed; check the system clock and certificates.") from None
        except (TimeoutError, socket.timeout):
            raise BackupError("Connection timed out; the site may be offline. Try again or increase --timeout.") from None
        except (error.URLError, http.client.HTTPException, ConnectionError):
            # Exceptions can include the entire URL, including an OAuth token.
            raise BackupError("Network/TLS connection failed or the download was interrupted.") from None
        finally:
            if response is not None:
                response.close()

    def json(self, url, **kwargs):
        with self.open(url, **kwargs) as response:
            data = read_json(response, JSON_LIMIT)
            if not 200 <= response.status < 300:
                raise ApiError(response.status, data)
            if error_code(data) not in ("", "0", "ok"):
                raise ApiError(403, data)
            return data


def read_json(response, limit):
    raw = response.read(limit + 1)
    if len(raw) > limit:
        raise BackupError("Nx JSON response exceeded the size limit.")
    if not raw and response.status in (204, 205):
        return {}
    try:
        return json.loads(raw)
    except (ValueError, UnicodeError):
        if not 200 <= response.status < 300:
            raise ApiError(response.status) from None
        raise BackupError("Nx returned an unexpected response instead of JSON.") from None


@dataclass(repr=False)
class Session:
    access: str
    refresh: str = ""


class Cloud:
    def __init__(self, http, email, password, prompt_secret=secret):
        self.http = http
        self.email = email
        self.password = password
        self.prompt_secret = prompt_secret
        self.sessions = []

    def login(self, site_id=None):
        # Include the cloud scope for MFA confirmation and revoking this session.
        # Each site's token is freshly password-authenticated, not just refreshed.
        body = {"grant_type": "password", "response_type": "token",
                "client_id": "3rdParty", "username": self.email,
                "password": self.password,
                "scope": CLOUD + " cloudSystemId=" + (site_id or "*")}
        with self.http.open(CLOUD + "/cdb/oauth2/token", method="POST", body=body) as response:
            data = read_json(response, JSON_LIMIT)
            code = error_code(data)
            if not isinstance(data, dict) or not isinstance(data.get("access_token"), str):
                raise ApiError(response.status if response.status >= 400 else 401, data)
            access = data["access_token"]
            if not access or any(not 33 <= ord(c) <= 126 for c in access):
                raise BackupError("Nx did not issue a valid access token.")
            refresh = data.get("refresh_token", "")
            if not isinstance(refresh, str) or any(not 33 <= ord(c) <= 126 for c in refresh):
                raise BackupError("Nx returned an invalid refresh token.")
            session = Session(access, refresh)
            self.sessions.append(session)
            if code in MFA_CODES:
                self.confirm_mfa(session)
            elif response.status >= 400 or code not in ("", "0", "ok"):
                raise ApiError(response.status if response.status >= 400 else 401, data)
            return session

    def confirm_mfa(self, session):
        for attempt in range(3):
            code = self.prompt_secret("Nx authenticator code (blank to cancel): ").strip()
            if not code:
                raise BackupError("Two-factor authentication cancelled.")
            if not re.fullmatch(r"[0-9]{6}", code):
                print("Enter the six-digit code from your authenticator.")
                continue
            url = CLOUD + "/cdb/account/self/2fa/totp/key/" + code
            url += "?" + parse.urlencode({"token": session.access})
            try:
                with self.http.open(url, token=session.access) as response:
                    if not 200 <= response.status < 300:
                        raise ApiError(response.status, read_json(response, JSON_LIMIT))
                    # This endpoint may return 200 with an empty body.
                    raw = response.read(JSON_LIMIT + 1)
                    if len(raw) > JSON_LIMIT:
                        raise BackupError("Unexpected response to two-factor authentication.")
                    if raw:
                        try:
                            result = json.loads(raw)
                        except (ValueError, UnicodeError):
                            raise BackupError("Unexpected response to two-factor authentication.") from None
                        if error_code(result) not in ("", "0", "ok"):
                            raise ApiError(403, result)
                return
            except ApiError as exc:
                if exc.code not in ("invalidtotp", "invalid_totp") and exc.status not in (400, 401, 403):
                    raise
                print("Nx did not accept that code. Try a current authenticator code.")
        raise BackupError("Two-factor authentication failed after three attempts.")

    def get_json(self, path, session):
        for attempt in range(2):
            try:
                return self.http.json(CLOUD + path, token=session.access)
            except ApiError as exc:
                if exc.code not in MFA_CODES or attempt:
                    raise
                self.confirm_mfa(session)

    def discover(self, session):
        sites = {}
        # The default list is documented as owned sites. Explicitly query shared
        # and organization-provided sites too; never silently omit a failed list.
        for query, label in (("", "default/owned"), ("?directlyShared=true", "directly shared"),
                             ("?directlyShared=false", "organisation-provided")):
            rows = site_rows(self.get_json("/cdb/systems" + query, session))
            print(f"Discovered {len(rows)} sites from the {label} list.")
            for row in rows:
                try:
                    site_id = str(uuid.UUID(row["id"]))
                except (KeyError, ValueError, TypeError, AttributeError):
                    raise BackupError("Nx returned a site without a valid UUID; discovery is incomplete.") from None
                sites[site_id] = Site(site_id, str(row.get("name") or site_id))
        return sorted(sites.values(), key=lambda site: (site.name.casefold(), site.id))

    def logout(self, session):
        if session not in self.sessions:
            return
        failed = False
        for token in (session.refresh, session.access):
            if not token:
                continue
            try:
                url = CLOUD + "/cdb/oauth2/token/" + parse.quote(token, safe="")
                with self.http.open(url, method="DELETE", token=session.access,
                                    timeout=min(self.http.timeout, 10)) as response:
                    if response.status not in (200, 204, 404):
                        failed = True
            except BackupError:
                failed = True
        if session in self.sessions:
            self.sessions.remove(session)
        if failed:
            print("Warning: Nx session revocation could not be confirmed; no tokens were saved.")

    def close(self):
        for session in list(self.sessions):
            self.logout(session)
        self.password = ""


@dataclass(frozen=True)
class Site:
    id: str
    name: str


def site_rows(data):
    if isinstance(data, list) and all(isinstance(item, dict) for item in data):
        return data
    if isinstance(data, dict):
        for key in ("systems", "sites", "reply"):
            if key in data:
                return site_rows(data[key])
    raise BackupError("Unrecognised site-list response; cannot confirm that all sites were discovered.")


def filename_stem(name):
    name = unicodedata.normalize("NFC", name)
    name = "".join("_" if c in '<>:"/\\|?*' or unicodedata.category(c).startswith("C")
                   else c for c in name)
    name = name.strip(" .") or "Unnamed site"
    # Leave space for a UUID suffix on filesystems with a 255-byte filename limit.
    name = name.encode("utf-8")[:120].decode("utf-8", errors="ignore").rstrip(" .")
    if re.fullmatch(r"(?i)(CON|PRN|AUX|NUL|COM[1-9¹²³]|LPT[1-9¹²³])(?:\..*)?", name):
        name = "_" + name
    return name


def site_filenames(sites):
    stems = {site.id: filename_stem(site.name) for site in sites}
    counts = Counter(stem.casefold() for stem in stems.values())
    names = {}
    used = set()
    for site in sites:
        stem = stems[site.id]
        if counts[stem.casefold()] > 1:
            stem += " -- " + site.id
        candidate = stem + ".db"
        while candidate.casefold() in used:
            stem += "_"
            candidate = stem + ".db"
        names[site.id] = candidate
        used.add(candidate.casefold())
    return names


def publish_backup(temp, destination):
    """Publish without replacing an existing file, including a dangling symlink."""
    try:
        os.link(temp, destination)
        return
    except OSError as exc:
        if exc.errno not in (errno.EPERM, errno.EOPNOTSUPP, errno.ENOSYS, errno.EXDEV):
            raise
    # Some removable/network filesystems do not support hard links. Exclusive
    # creation still prevents overwrites; remove an interrupted copy on failure.
    fd = os.open(destination, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    try:
        with os.fdopen(fd, "wb") as output, open(temp, "rb") as source:
            shutil.copyfileobj(source, output, CHUNK_SIZE)
            output.flush()
            os.fsync(output.fileno())
    except BaseException:
        destination.unlink(missing_ok=True)
        raise


def save_backup(response, destination):
    """Accept a raw dump or Nx's JSON/base64 envelope, never an error page."""
    content_type = response.headers.get("Content-Type", "").split(";")[0].lower().strip()
    if response.headers.get("Content-Encoding", "identity").lower() != "identity":
        raise BackupError("Unexpected compressed HTTP response; no backup saved.")
    length = response.headers.get("Content-Length")
    if length is not None:
        try:
            length = int(length)
        except ValueError:
            raise BackupError("Invalid download size from Nx.") from None
        if length < 0 or length > BACKUP_LIMIT:
            raise BackupError("Backup exceeds the 512 MiB download limit.")
    fd, temp_name = tempfile.mkstemp(prefix=".nx-backup-", suffix=".partial", dir=destination.parent)
    temp = Path(temp_name)
    try:
        with os.fdopen(fd, "wb") as output:
            count = 0
            while chunk := response.read(CHUNK_SIZE):
                count += len(chunk)
                if count > BACKUP_LIMIT:
                    raise BackupError("Backup exceeds the 512 MiB download limit.")
                output.write(chunk)
            output.flush()
            os.fsync(output.fileno())
        if not count or (length is not None and count != length):
            raise BackupError("Empty or incomplete database download; no backup saved.")
        with temp.open("rb") as source:
            prefix = source.read(512)
        json_payload = "json" in content_type or prefix.lstrip().startswith((b"{", b"["))
        if json_payload:
            if count > BACKUP_JSON_LIMIT:
                raise BackupError("JSON backup exceeds the 128 MiB decoding limit.")
            try:
                data = json.loads(temp.read_bytes())
                if error_code(data) not in ("", "0", "ok"):
                    raise BackupError("Nx returned an API error instead of a database.")
                payload = data.get("reply", data)
                encoded = payload["data"]
                decoded = base64.b64decode(encoded, validate=True)
                if not decoded:
                    raise ValueError()
            except (ValueError, KeyError, TypeError, AttributeError, binascii.Error):
                raise BackupError("Nx did not return a valid database payload.") from None
            with temp.open("wb") as output:
                output.write(decoded)
                output.flush()
                os.fsync(output.fileno())
            prefix = decoded[:512]
        if ((not json_payload and (content_type.startswith("text/") or "html" in content_type))
                or prefix.lstrip().startswith((b"<", b"{", b"["))):
            raise BackupError("Nx returned a web page instead of a database.")
        is_sqlite = prefix.startswith(b"SQLite format 3\x00")
        if is_sqlite:
            try:
                with closing(sqlite3.connect(
                        temp.resolve().as_uri() + "?mode=ro&immutable=1", uri=True)) as db:
                    result = db.execute("PRAGMA quick_check").fetchall()
                    if result != [("ok",)]:
                        raise BackupError("Downloaded SQLite database failed its integrity check.")
            except sqlite3.Error:
                raise BackupError("Downloaded SQLite database is invalid; no backup saved.") from None
        elif not json_payload and content_type not in (
                "application/octet-stream", "application/x-sqlite3", "application/vnd.sqlite3"):
            raise BackupError("Unrecognised backup format; no backup saved.")
        size = temp.stat().st_size
        publish_backup(temp, destination)
        return size, is_sqlite
    finally:
        temp.unlink(missing_ok=True)


def download_site(cloud, site, destination):
    session = cloud.login(site.id)
    try:
        for path in BACKUP_PATHS:
            for attempt in range(2):
                url = f"https://{site.id}.relay.vmsproxy.com" + path
                with cloud.http.open(url, token=session.access, site_id=site.id,
                                     accept="application/octet-stream") as response:
                    if response.status in (404, 405, 501):
                        break  # Only an unavailable API triggers version fallback.
                    if response.status != 200:
                        data = read_json(response, JSON_LIMIT)
                        exc = ApiError(response.status, data)
                        if exc.code in MFA_CODES and attempt == 0:
                            cloud.confirm_mfa(session)
                            continue
                        if exc.code in FRESH_CODES and attempt == 0:
                            cloud.logout(session)
                            session = cloud.login(site.id)
                            continue
                        raise exc
                    print("  Backup request authorised; downloading...", flush=True)
                    return save_backup(response, destination)
        raise BackupError("No supported database backup endpoint was found on this site.")
    finally:
        cloud.logout(session)


class BackupRun:
    """Share successful site IDs and filename reservations across cloud accounts."""

    def __init__(self, directory):
        self.directory = directory
        self.names = {}
        self.saved = set()
        self.existing = set()
        self.failed = set()
        self.failed_attempts = 0
        self.duplicates = 0

    def process(self, cloud, sites):
        proposed = site_filenames(sites)
        used_names = {name.casefold() for name in self.names.values()}
        for site in sites:
            if site.id in self.names:
                continue
            candidate = proposed[site.id]
            if candidate.casefold() in used_names:
                stem = filename_stem(site.name) + " -- " + site.id
                candidate = stem + ".db"
                while candidate.casefold() in used_names:
                    stem += "_"
                    candidate = stem + ".db"
            self.names[site.id] = candidate
            used_names.add(candidate.casefold())

        for index, site in enumerate(sites, 1):
            print(f"[{index}/{len(sites)}] {display(site.name)} ({site.id})", flush=True)
            if site.id in self.saved:
                print("  ALREADY SAVED: this site was backed up earlier in this run.")
                self.duplicates += 1
                continue
            destination = self.directory / self.names[site.id]
            if os.path.lexists(destination):
                print(f"  SKIPPED: {display(destination.name)} already exists; it was preserved.")
                self.existing.add(site.id)
                self.failed.discard(site.id)
                continue
            self.existing.discard(site.id)
            try:
                size, is_sqlite = download_site(cloud, site, destination)
                detail = "SQLite integrity checked" if is_sqlite else "Nx binary backup; not verified as SQLite"
                print(f"  SAVED: {display(destination.name)} ({size:,} bytes; {detail}).")
                self.saved.add(site.id)
                self.failed.discard(site.id)
                self.existing.discard(site.id)
            except BackupError as exc:
                print(f"  FAILED: {exc}")
                self.failed.add(site.id)
                self.failed_attempts += 1
            except OSError:
                print("  FAILED: could not write backup; check disk space, permissions and existing files.")
                self.failed.add(site.id)
                self.failed_attempts += 1

    def report(self):
        print(f"\nFinished: {len(self.saved)} saved, {len(self.existing)} existing files skipped, "
              f"{len(self.failed)} failed.")
        if self.duplicates:
            print(f"Repeated sites already saved in this run: {self.duplicates}.")
        if self.failed_attempts:
            print(f"Failed download attempts: {self.failed_attempts}; "
                  f"sites still without a backup after retries: {len(self.failed)}.")
        print(f"Backup directory: {display(self.directory)}")
        return 2 if self.existing or self.failed else 0


def run_backups(cloud, sites, directory):
    run = BackupRun(directory)
    run.process(cloud, sites)
    return run.report()


@dataclass(repr=False)
class Account:
    name: str
    user: str
    password: str
    line: int


def read_accounts(path):
    """Validate structure before login; invalid individual records can be skipped.

    Preserve passwords exactly, including spaces, commas and quoted newlines.
    Never include CSV contents in an exception or diagnostic.
    """
    accounts, invalid = [], []
    try:
        with path.open(encoding="utf-8-sig", newline="") as source:
            reader = csv.reader(source, strict=True)
            header = next(reader, None)
            fields = [field.strip().casefold() for field in header] if header else []
            if len(fields) != 3 or set(fields) != {"name", "user", "password"}:
                raise BackupError("Accounts CSV must have the header: name,user,password.")
            for row in reader:
                if not row or all(not field.strip() for field in row):
                    continue
                line = reader.line_num
                if len(row) != 3:
                    invalid.append(f"CSV line {line}: expected three columns; row skipped.")
                    continue
                record = dict(zip(fields, row))
                name, user, password = record["name"].strip(), record["user"].strip(), record["password"]
                if not name or not user or not password:
                    invalid.append(f"CSV line {line}: name, user and password must be filled in; row skipped.")
                elif not re.fullmatch(r"[^@\s]+@[^@\s]+", user):
                    invalid.append(f"CSV line {line}: user must be an email address; row skipped.")
                else:
                    accounts.append(Account(name, user, password, line))
    except (csv.Error, UnicodeError):
        raise BackupError("Accounts CSV has invalid UTF-8 or malformed CSV quoting; no accounts were processed.") from None
    except OSError:
        raise BackupError("Cannot read the accounts CSV; check its path and permissions.") from None
    for message in invalid:
        print(message)
    if not accounts:
        raise BackupError("Accounts CSV contains no valid account rows.")
    return accounts, len(invalid)


def csv_mfa_unavailable(_prompt):
    raise BackupError("Two-factor authentication is required; CSV mode does not prompt. "
                      "Use interactive mode for this account.")


def run_accounts(accounts, invalid_rows, directory, http):
    run = BackupRun(directory)
    account_failures = 0
    try:
        for index, account in enumerate(accounts, 1):
            print(f"\nAccount [{index}/{len(accounts)}]: {display(account.name)} "
                  f"(CSV line {account.line})", flush=True)
            cloud = Cloud(http, account.user, account.password, prompt_secret=csv_mfa_unavailable)
            try:
                session = cloud.login()
                sites = cloud.discover(session)
                cloud.logout(session)
                if not sites:
                    print("No sites were returned for this account.")
                else:
                    run.process(cloud, sites)
            except BackupError as exc:
                print(f"  ACCOUNT FAILED: {exc}")
                account_failures += 1
            except OSError:
                print("  ACCOUNT FAILED: network or filesystem error; continuing with the next account.")
                account_failures += 1
            finally:
                account.password = ""
                cloud.close()
    finally:
        for account in accounts:
            account.password = ""
    result = run.report()
    print(f"Accounts: {len(accounts)} processed, {account_failures} login/discovery failures; "
          f"{invalid_rows} invalid CSV rows skipped.")
    return 2 if account_failures or invalid_rows else result


def positive_timeout(value):
    number = float(value)
    if not math.isfinite(number) or number <= 0:
        raise argparse.ArgumentTypeError("timeout must be a finite positive number")
    return number


def main(argv=None):
    parser = argparse.ArgumentParser(
        description="Download Nx Cloud configuration backups for owned, shared and "
                    "organisation-provided sites. Use a name,user,password CSV for multiple "
                    "cloud accounts, or omit --accounts-csv for interactive login. No third-party packages.",
        epilog="Credentials are never logged. CSV mode runs without prompts, including for 2FA. "
               "Existing backups are never overwritten. Exit codes: 0 all saved/no sites; "
               "1 setup or interactive login/discovery error; 2 some accounts/rows/backups "
               "failed or files existed; 130 interrupted.")
    parser.add_argument("--accounts-csv", type=Path,
                        help="UTF-8 CSV with name,user,password headers (user is an Nx Cloud email)")
    parser.add_argument("-o", "--output-dir", type=Path,
                        help="destination directory (default: new dated folder under your home; "
                             "interactive mode prompts)")
    parser.add_argument("--timeout", type=positive_timeout, default=120,
                        help="network inactivity timeout in seconds (default: 120)")
    args = parser.parse_args(argv)
    cloud = None
    try:
        accounts, invalid_rows = read_accounts(args.accounts_csv.expanduser()) if args.accounts_csv else (None, 0)
        directory = args.output_dir
        if directory is None:
            default = Path.home() / "NX Camera Backups" / datetime.now().strftime("%Y-%m-%d_%H-%M-%S-%f")
            if accounts is not None:
                directory = default
            else:
                chosen = input(f"Backup directory [{default}]: ").strip()
                directory = Path(chosen) if chosen else default
        directory = directory.expanduser().resolve()
        directory.mkdir(mode=0o700, parents=True, exist_ok=True)
        # Check write access before asking for credentials or issuing API calls.
        with tempfile.TemporaryFile(dir=directory):
            pass
        if accounts is not None:
            print(f"Loaded {len(accounts)} accounts. Backup directory: {display(directory)}", flush=True)
            return run_accounts(accounts, invalid_rows, directory, Http(args.timeout))
        email = input("Nx Cloud email: ").strip()
        password = secret("Nx Cloud password: ")
        if not email or not password:
            raise BackupError("Email and password are required.")
        cloud = Cloud(Http(args.timeout), email, password)
        password = ""
        print("Signing in to nxvms.com...", flush=True)
        session = cloud.login()
        sites = cloud.discover(session)
        cloud.logout(session)
        if not sites:
            print("No sites were returned by the owned, shared or organisation lists.")
            return 0
        print(f"\nFound {len(sites)} unique sites. Checking backup access on each site.")
        return run_backups(cloud, sites, directory)
    except (KeyboardInterrupt, EOFError):
        print("\nCancelled. Completed backups have been kept.")
        return 130
    except BackupError as exc:
        print(f"Error: {exc}", file=sys.stderr)
        return 1
    except OSError:
        print("Error: cannot access the destination; check the path, permissions and disk space.", file=sys.stderr)
        return 1
    finally:
        if cloud is not None:
            cloud.close()


if __name__ == "__main__":
    sys.exit(main())
