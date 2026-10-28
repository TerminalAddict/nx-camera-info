"""Offline tests: synthetic databases and fake HTTP responses; never contact Nx."""

import base64
from contextlib import closing, redirect_stdout, redirect_stderr
import errno
import io
import json
import os
from pathlib import Path
import sqlite3
import tempfile
import unittest
from unittest.mock import patch
from urllib import error, parse

import backup_cloud_sites as backup


OWNED = "11111111-1111-4111-8111-111111111111"
SHARED = "22222222-2222-4222-8222-222222222222"
ORG = "33333333-3333-4333-8333-333333333333"
PASSWORD = "synthetic-test-password"
TOKEN = "nxcdb-synthetic-test-token"


class Response(io.BytesIO):
    def __init__(self, body=b"", status=200, headers=None):
        if isinstance(body, (dict, list)):
            body = json.dumps(body).encode()
            default_type = "application/json"
        else:
            default_type = "application/octet-stream"
        super().__init__(body)
        self.status = status
        self.headers = {"Content-Type": default_type, "Content-Length": str(len(body))}
        self.headers.update(headers or {})


class Opener:
    def __init__(self, handler):
        self.handler = handler
        self.calls = []

    def open(self, req, timeout):
        self.calls.append(req)
        return self.handler(req)


class ScriptTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.directory = Path(self.temp.name)
        self.log = io.StringIO()
        self.stdout = redirect_stdout(self.log)
        self.stdout.__enter__()
        self.addCleanup(self.stdout.__exit__, None, None, None)
        source = self.directory / "fixture.sqlite"
        with closing(sqlite3.connect(source)) as db:
            db.execute("CREATE TABLE synthetic (name TEXT)")
            db.execute("INSERT INTO synthetic VALUES ('Synthetic camera')")
            db.commit()
        self.database = source.read_bytes()
        source.unlink()

    def make_cloud(self, handler, prompt=lambda _: "123456"):
        opener = Opener(handler)
        cloud = backup.Cloud(backup.Http(opener=opener), "test@example.invalid", PASSWORD, prompt)
        return cloud, opener

    def tokens(self, req):
        if req.method == "POST":
            self.assertEqual(req.full_url, backup.CLOUD + "/cdb/oauth2/token")
            body = json.loads(req.data)
            self.assertEqual(body["grant_type"], "password")
            self.assertEqual(body["password"], PASSWORD)
            self.assertEqual(body["client_id"], "3rdParty")
            return Response({"access_token": TOKEN, "refresh_token": "synthetic-refresh"})
        if req.method == "DELETE":
            self.assertTrue(req.full_url.startswith(backup.CLOUD + "/cdb/oauth2/token/"))
            return Response(status=204)
        return None

    def assert_no_secrets_logged(self):
        for value in (TOKEN, PASSWORD, "synthetic-refresh", "123456"):
            self.assertNotIn(value, self.log.getvalue())

    def test_owned_shared_and_organisation_are_combined_once(self):
        def handler(req):
            query = parse.urlsplit(req.full_url).query
            rows = {
                "": [{"id": OWNED, "name": "Owned"}],
                "directlyShared=true": [{"id": OWNED, "name": "Owned"},
                                        {"id": SHARED, "name": "Shared"}],
                "directlyShared=false": [{"id": ORG, "name": "Organisation"}],
            }
            self.assertEqual(req.get_header("Authorization"), "Bearer " + TOKEN)
            return Response(rows[query])
        cloud, opener = self.make_cloud(handler)
        sites = cloud.discover(backup.Session(TOKEN))
        self.assertEqual({site.id for site in sites}, {OWNED, SHARED, ORG})
        self.assertEqual(len(opener.calls), 3)

    def test_failed_shared_discovery_does_not_claim_complete_inventory(self):
        def handler(req):
            if "directlyShared=true" in req.full_url:
                return Response({"error": "forbidden"}, 403)
            return Response([{"id": OWNED, "name": "Owned"}])
        cloud, _ = self.make_cloud(handler)
        with self.assertRaises(backup.ApiError):
            cloud.discover(backup.Session(TOKEN))

    def test_malformed_inventory_is_not_reported_as_no_sites(self):
        for response in ({"unexpected": []}, {"systems": [None]}, [{"id": "../bad"}]):
            with self.subTest(response=response):
                cloud, _ = self.make_cloud(lambda req: Response(response))
                with self.assertRaises(backup.BackupError):
                    cloud.discover(backup.Session(TOKEN))

    def test_empty_lists_and_known_wrappers(self):
        cloud, opener = self.make_cloud(lambda req: Response({"reply": {"systems": []}}))
        self.assertEqual(cloud.discover(backup.Session(TOKEN)), [])
        self.assertEqual(len(opener.calls), 3)

    def test_fresh_site_scope_and_revocation(self):
        cloud, opener = self.make_cloud(self.tokens)
        session = cloud.login(OWNED)
        self.assertEqual(json.loads(opener.calls[0].data)["scope"],
                         backup.CLOUD + " cloudSystemId=" + OWNED)
        cloud.logout(session)
        self.assertEqual([req.method for req in opener.calls], ["POST", "DELETE", "DELETE"])
        self.assertTrue(opener.calls[1].full_url.endswith("synthetic-refresh"))
        self.assertTrue(opener.calls[2].full_url.endswith(TOKEN))
        self.assertEqual(cloud.sessions, [])
        self.assert_no_secrets_logged()

    def test_mfa_confirmation_on_login_and_retry_invalid_code(self):
        confirmations = []
        def handler(req):
            if req.method == "POST":
                return Response({"access_token": TOKEN, "error": "secondFactorRequired"}, 403)
            confirmations.append(req.full_url)
            self.assertEqual(req.get_header("Authorization"), "Bearer " + TOKEN)
            if len(confirmations) == 1:
                return Response({"error": "invalidTotp"}, 403)
            return Response(b"")
        codes = iter(["111111", "123456"])
        cloud, _ = self.make_cloud(handler, prompt=lambda _: next(codes))
        self.assertEqual(cloud.login().access, TOKEN)
        self.assertEqual(len(confirmations), 2)
        self.assertIn("/totp/key/123456?token=", confirmations[1])
        self.assert_no_secrets_logged()

    def test_mfa_requested_during_site_discovery(self):
        attempts = 0
        def handler(req):
            nonlocal attempts
            if "/totp/key/" in req.full_url:
                return Response(status=204)
            attempts += 1
            if attempts == 1:
                return Response({"error": "secondFactorRequired"}, 403)
            return Response([])
        cloud, _ = self.make_cloud(handler)
        self.assertEqual(cloud.discover(backup.Session(TOKEN)), [])
        self.assertEqual(attempts, 4)

    def test_mfa_cancelled_session_is_cleaned_up(self):
        def handler(req):
            if req.method == "POST":
                return Response({"access_token": TOKEN, "error": "secondFactorRequired"})
            return self.tokens(req)
        cloud, opener = self.make_cloud(handler, prompt=lambda _: "")
        with self.assertRaisesRegex(backup.BackupError, "cancelled"):
            cloud.login()
        cloud.close()
        self.assertEqual(opener.calls[-1].method, "DELETE")
        self.assertEqual(cloud.password, "")

    def test_mfa_logical_error_in_http_200_is_not_success(self):
        cloud, _ = self.make_cloud(lambda req: Response({"error": "invalidTotp"}))
        with self.assertRaisesRegex(backup.BackupError, "three attempts"):
            cloud.confirm_mfa(backup.Session(TOKEN))

    def test_bad_password_is_not_retried_or_logged(self):
        cloud, opener = self.make_cloud(lambda req: Response(
            {"error": "invalid_grant", "description": PASSWORD + TOKEN}, 401))
        with self.assertRaisesRegex(backup.ApiError, "Authentication rejected") as raised:
            cloud.login()
        self.assertNotIn(PASSWORD, str(raised.exception))
        self.assertEqual(len(opener.calls), 1)

    def test_oauth_http_400_credentials_and_account_status_errors_are_clear(self):
        for code, message in (("invalid_grant", "email and password"),
                              ("accountBlocked", "blocked"),
                              ("accountNotActivated", "not been activated")):
            with self.subTest(code=code):
                cloud, _ = self.make_cloud(lambda req: Response(
                    {"error": code, "description": PASSWORD}, 400))
                with self.assertRaisesRegex(backup.ApiError, message) as raised:
                    cloud.login()
                self.assertNotIn(PASSWORD, str(raised.exception))

    def test_malformed_tokens_cannot_leak_via_http_header_errors(self):
        for token in (TOKEN + "\n", TOKEN + "\x00", "", TOKEN + "é"):
            with self.subTest(token=repr(token)):
                cloud, opener = self.make_cloud(lambda req: Response({"access_token": token}))
                with self.assertRaises(backup.BackupError) as raised:
                    cloud.login()
                self.assertNotIn(TOKEN, str(raised.exception))
                self.assertEqual(len(opener.calls), 1)

    def test_failed_revocation_warns_without_exposing_tokens(self):
        def handler(req):
            if req.method == "DELETE":
                raise error.URLError("failed URL contains " + TOKEN)
            return self.tokens(req)
        cloud, opener = self.make_cloud(handler)
        session = cloud.login()
        cloud.logout(session)
        cloud.logout(session)  # Already attempted: no duplicate requests.
        self.assertEqual(len(opener.calls), 3)
        self.assertEqual(cloud.sessions, [])
        self.assertIn("revocation could not be confirmed", self.log.getvalue())
        self.assert_no_secrets_logged()

    def test_redirect_preserves_bearer_only_to_same_site_relay(self):
        relay = f"https://{OWNED}.relay-eu-test-1.vmsproxy.com/rest/v4/site/database"
        def handler(req):
            self.assertEqual(req.get_header("Authorization"), "Bearer " + TOKEN)
            if req.full_url != relay:
                # Exercise urllib's real HTTPError branch, not only fake statuses.
                raise error.HTTPError(req.full_url, 307, "Redirect", {"Location": relay}, io.BytesIO())
            return Response(self.database)
        cloud, opener = self.make_cloud(handler)
        with cloud.http.open(f"https://{OWNED}.relay.vmsproxy.com/rest/v4/site/database",
                             token=TOKEN, site_id=OWNED) as response:
            self.assertEqual(response.read(), self.database)
        self.assertEqual(len(opener.calls), 2)

    def test_untrusted_redirects_never_receive_token(self):
        urls = ["http://nxvms.com/", "https://attacker.invalid/", "https://nxvms.com.attacker.invalid/",
                f"https://{SHARED}.relay.vmsproxy.com/", f"https://{OWNED}.relay.vmsproxy.com:444/",
                f"https://{OWNED}.relay.vmsproxy.com.attacker.invalid/",
                f"https://user@{OWNED}.relay.vmsproxy.com/"]
        for url in urls:
            with self.subTest(url=url):
                cloud, opener = self.make_cloud(lambda req: Response(status=307, headers={"Location": url}))
                with self.assertRaisesRegex(backup.BackupError, "untrusted"):
                    with cloud.http.open(f"https://{OWNED}.relay.vmsproxy.com/", token=TOKEN, site_id=OWNED):
                        self.fail("untrusted redirect was followed")
                self.assertEqual(len(opener.calls), 1)

    def test_credentials_cannot_follow_cloud_post_to_other_origin(self):
        cloud, opener = self.make_cloud(lambda req: Response(
            status=307, headers={"Location": "https://attacker.invalid/"}))
        with self.assertRaisesRegex(backup.BackupError, "untrusted"):
            cloud.login()
        self.assertEqual(len(opener.calls), 1)

    def test_network_exceptions_hide_urls_and_tokens(self):
        def handler(req):
            raise error.URLError("secret URL " + TOKEN + PASSWORD)
        cloud, _ = self.make_cloud(handler)
        with self.assertRaises(backup.BackupError) as raised:
            cloud.login()
        self.assertNotIn(TOKEN, str(raised.exception))
        self.assertNotIn(PASSWORD, str(raised.exception))

    def test_raw_sqlite_and_json_envelopes(self):
        encoded = base64.b64encode(self.database).decode()
        responses = [self.database, {"data": encoded}, {"error": "0", "reply": {"data": encoded}}]
        for index, body in enumerate(responses):
            with self.subTest(index=index):
                destination = self.directory / f"Site {index}.db"
                size, is_sqlite = backup.save_backup(Response(body), destination)
                self.assertEqual(destination.read_bytes(), self.database)
                self.assertEqual(size, len(self.database))
                self.assertTrue(is_sqlite)
                if os.name != "nt":
                    self.assertEqual(destination.stat().st_mode & 0o777, 0o600)
        self.assertEqual(list(self.directory.glob("*.partial")), [])

    def test_proprietary_binary_is_saved_but_not_claimed_sqlite(self):
        destination = self.directory / "Site.db"
        size, is_sqlite = backup.save_backup(Response(b"\x80NX synthetic opaque dump"), destination)
        self.assertGreater(size, 0)
        self.assertFalse(is_sqlite)

    def test_html_errors_invalid_base64_and_corrupt_sqlite_are_not_backups(self):
        responses = [Response(b"<html>Sign in</html>"),
                     Response({"error": "forbidden", "description": TOKEN}),
                     Response({"data": "not base64!"}), Response({"data": ""}),
                     Response({"data": base64.b64encode(b"<html>Sign in</html>").decode()}),
                     Response(b"SQLite format 3\x00corrupt"), Response(b""),
                     Response(b"text login error", headers={"Content-Type": "text/plain"})]
        for response in responses:
            with self.subTest(body=response.getvalue()[:50]):
                with self.assertRaises(backup.BackupError):
                    backup.save_backup(response, self.directory / "Site.db")
                self.assertEqual(list(self.directory.iterdir()), [])

    def test_truncation_and_limits_preserve_existing_backup(self):
        destination = self.directory / "Site.db"
        destination.write_bytes(b"previous backup")
        responses = [Response(self.database, headers={"Content-Length": str(len(self.database) + 1)}),
                     Response(self.database, headers={"Content-Length": str(backup.BACKUP_LIMIT + 1)}),
                     Response(self.database, headers={"Content-Encoding": "gzip"})]
        for response in responses:
            with self.subTest(headers=response.headers):
                with self.assertRaises(backup.BackupError):
                    backup.save_backup(response, destination)
                self.assertEqual(destination.read_bytes(), b"previous backup")
                self.assertEqual(list(self.directory.glob("*.partial")), [])

    def test_stream_without_content_length_enforces_limit(self):
        response = Response(self.database)
        del response.headers["Content-Length"]
        with patch.object(backup, "BACKUP_LIMIT", 100):
            with self.assertRaisesRegex(backup.BackupError, "limit"):
                backup.save_backup(response, self.directory / "Site.db")
        self.assertEqual(list(self.directory.iterdir()), [])

    def test_existing_file_or_symlink_never_overwritten(self):
        destination = self.directory / "Site.db"
        destination.write_bytes(b"existing")
        with self.assertRaises(FileExistsError):
            backup.save_backup(Response(self.database), destination)
        self.assertEqual(destination.read_bytes(), b"existing")
        destination.unlink()
        try:
            destination.symlink_to(self.directory / "missing-target")
        except OSError:
            return  # Windows may require extra rights to create a symlink.
        with self.assertRaises(FileExistsError):
            backup.save_backup(Response(self.database), destination)
        self.assertTrue(destination.is_symlink())
        self.assertFalse((self.directory / "missing-target").exists())

    def test_removable_filesystem_without_hard_links(self):
        destination = self.directory / "Site.db"
        with patch.object(backup.os, "link", side_effect=OSError(errno.EOPNOTSUPP, "no links")):
            backup.save_backup(Response(self.database), destination)
            with self.assertRaises(FileExistsError):
                backup.save_backup(Response(self.database), destination)
        self.assertEqual(destination.read_bytes(), self.database)

    def test_interrupted_download_removes_partial(self):
        class Interrupted(Response):
            def read(self, *args):
                if self.tell():
                    raise KeyboardInterrupt()
                return super().read(10)
        with self.assertRaises(KeyboardInterrupt):
            backup.save_backup(Interrupted(self.database), self.directory / "Site.db")
        self.assertEqual(list(self.directory.iterdir()), [])

    def test_windows_safe_filenames_and_duplicates(self):
        names = ["../a:b\\c?*\x1b", "CON", "nul.txt", "LPT1", "AUX", "...", "a/b", "a:b", "A:B", "é" * 300]
        sites = [backup.Site(str(index), name) for index, name in enumerate(names)]
        filenames = backup.site_filenames(sites)
        self.assertEqual(len({name.casefold() for name in filenames.values()}), len(names))
        for name in filenames.values():
            self.assertEqual(Path(name).name, name)
            self.assertFalse(any(c in name for c in '<>:"/\\|?*\x1b'))
            self.assertLess(len(name.encode()), 255)
        self.assertEqual(filenames["1"], "_CON.db")
        self.assertEqual(filenames["2"], "_nul.txt.db")
        self.assertEqual(backup.display("Site\n\x1b[31m"), "Site__[31m")

    def test_download_falls_back_for_older_server_but_not_for_denied_access(self):
        for status in (404, 403):
            with self.subTest(status=status):
                def handler(req):
                    auth = self.tokens(req)
                    if auth is not None:
                        return auth
                    if "/rest/v4/" in req.full_url:
                        return Response({"error": "forbidden" if status == 403 else "notFound"}, status)
                    return Response(self.database)
                cloud, opener = self.make_cloud(handler)
                destination = self.directory / f"Site{status}.db"
                if status == 404:
                    backup.download_site(cloud, backup.Site(OWNED, "Site"), destination)
                    self.assertEqual(destination.read_bytes(), self.database)
                else:
                    with self.assertRaisesRegex(backup.ApiError, "Permission denied"):
                        backup.download_site(cloud, backup.Site(OWNED, "Site"), destination)
                    self.assertFalse(destination.exists())
                    self.assertFalse(any("/rest/v3/" in req.full_url for req in opener.calls))
                self.assertEqual(cloud.sessions, [])

    def test_site_mfa_and_fresh_auth_retry(self):
        for code in ("secondFactorRequired", "userPasswordRequired"):
            with self.subTest(code=code):
                attempts = 0
                def handler(req):
                    nonlocal attempts
                    auth = self.tokens(req)
                    if auth is not None:
                        return auth
                    if "/totp/key/" in req.full_url:
                        return Response(status=204)
                    attempts += 1
                    if attempts == 1:
                        return Response({"errorId": code}, 403)
                    return Response(self.database)
                cloud, opener = self.make_cloud(handler)
                destination = self.directory / (code + ".db")
                backup.download_site(cloud, backup.Site(OWNED, "Site"), destination)
                self.assertEqual(destination.read_bytes(), self.database)
                self.assertEqual(attempts, 2)
                self.assertEqual(sum(req.method == "POST" for req in opener.calls),
                                 2 if code == "userPasswordRequired" else 1)

    def test_denied_shared_and_offline_sites_do_not_stop_owned_backup(self):
        def handler(req):
            auth = self.tokens(req)
            if auth is not None:
                return auth
            host = parse.urlsplit(req.full_url).hostname
            if host.startswith(SHARED):
                return Response({"errorId": "forbidden"}, 403)
            if host.startswith(ORG):
                return Response({"errorId": "serviceUnavailable"}, 503)
            return Response(self.database)
        cloud, opener = self.make_cloud(handler)
        sites = [backup.Site(SHARED, "Shared"), backup.Site(ORG, "Offline"), backup.Site(OWNED, "Owned")]
        self.assertEqual(backup.run_backups(cloud, sites, self.directory), 2)
        self.assertEqual([path.name for path in self.directory.iterdir()], ["Owned.db"])
        self.assertIn("1 saved, 0 existing files skipped, 2 failed", self.log.getvalue())
        self.assertIn("Permission denied", self.log.getvalue())
        self.assertIn("offline", self.log.getvalue())
        self.assertEqual(sum(req.method == "POST" for req in opener.calls), 3)
        self.assert_no_secrets_logged()

    def test_existing_backup_is_skipped_without_network_access(self):
        (self.directory / "Site.db").write_bytes(b"existing")
        cloud, opener = self.make_cloud(lambda req: self.fail("network must not be called"))
        self.assertEqual(backup.run_backups(cloud, [backup.Site(OWNED, "Site")], self.directory), 2)
        self.assertEqual(opener.calls, [])

    def test_full_interactive_workflow_with_owned_and_shared_sites(self):
        def handler(req):
            auth = self.tokens(req)
            if auth is not None:
                return auth
            parts = parse.urlsplit(req.full_url)
            if parts.path == "/cdb/systems":
                if not parts.query:
                    return Response([{"id": OWNED, "name": "Owned"}])
                if parts.query == "directlyShared=true":
                    return Response([{"id": SHARED, "name": "Shared"}])
                return Response([])
            if parts.hostname.startswith(SHARED):
                return Response({"errorId": "forbidden"}, 403)
            return Response(self.database)
        opener = Opener(handler)
        http = backup.Http(opener=opener)
        with patch.object(backup, "Http", return_value=http), patch("builtins.input", return_value="test@example.invalid"), \
                patch.object(backup, "secret", return_value=PASSWORD):
            result = backup.main(["--output-dir", str(self.directory)])
        self.assertEqual(result, 2)
        self.assertEqual((self.directory / "Owned.db").read_bytes(), self.database)
        self.assertFalse((self.directory / "Shared.db").exists())
        self.assertEqual(sum(req.method == "DELETE" for req in opener.calls), 6)
        self.assert_no_secrets_logged()

    def test_cli_eof_is_clean_cancellation_and_help_needs_no_credentials(self):
        with patch("builtins.input", side_effect=EOFError), patch.object(backup, "Http") as http:
            self.assertEqual(backup.main(["-o", str(self.directory)]), 130)
            http.assert_not_called()
        with self.assertRaises(SystemExit) as raised:
            backup.main(["--help"])
        self.assertEqual(raised.exception.code, 0)

    def test_invalid_timeout_rejected_before_credentials(self):
        for value in ("0", "-1", "nan", "inf", "garbage"):
            with self.subTest(value=value), redirect_stderr(io.StringIO()):
                with self.assertRaises(SystemExit) as raised:
                    backup.main(["--timeout", value])
                self.assertEqual(raised.exception.code, 2)

    def test_csv_bom_header_spaces_and_password_punctuation_are_preserved(self):
        source = self.directory / "accounts.csv"
        source.write_text('\ufeff Name , USER , Password \r\n"Store, North", test@example.invalid ," p,a""ss "\r\n',
                          encoding="utf-8", newline="")
        accounts, invalid = backup.read_accounts(source)
        self.assertEqual(invalid, 0)
        self.assertEqual(len(accounts), 1)
        self.assertEqual(accounts[0].name, "Store, North")
        self.assertEqual(accounts[0].user, "test@example.invalid")
        self.assertEqual(accounts[0].password, ' p,a"ss ')
        self.assertNotIn(' p,a"ss ', repr(accounts))
        self.assertNotIn(' p,a"ss ', self.log.getvalue())

    def test_csv_multiline_password_and_reordered_headers(self):
        source = self.directory / "accounts.csv"
        for newline in ("\n", "\r\n"):
            with self.subTest(newline=repr(newline)):
                content = 'password,name,user\n"line one\nline two",Example,test@example.invalid\n'
                source.write_bytes(content.replace("\n", newline).encode("utf-8"))
                accounts, invalid = backup.read_accounts(source)
                self.assertEqual(invalid, 0)
                self.assertEqual(accounts[0].password, "line one" + newline + "line two")

    def test_invalid_csv_rows_are_skipped_without_printing_contents(self):
        source = self.directory / "accounts.csv"
        source.write_text("name,user,password\n"
                          "Good,test@example.invalid,secret-good\n"
                          "Bad email,not-an-email,secret-bad-email\n"
                          ",test@example.invalid,secret-missing-name\n"
                          "Extra,test@example.invalid,secret-extra,column\n\n", encoding="utf-8")
        accounts, invalid = backup.read_accounts(source)
        self.assertEqual([account.name for account in accounts], ["Good"])
        self.assertEqual(invalid, 3)
        self.assertIn("CSV line 3", self.log.getvalue())
        self.assertNotIn("secret-", self.log.getvalue())

    def test_bad_csv_structure_aborts_before_any_login(self):
        cases = [b"name,user,user\n", b"test@example.invalid,secret-password\n",
                 b"name,user,password\n", b"name,user,password\nName,bad-email,secret-password\n",
                 b'name,user,password\nValid,test@example.invalid,secret-password\nBad,test@example.invalid,"unterminated\n',
                 b"name,user,password\nName,test@example.invalid,\xff\n"]
        for content in cases:
            with self.subTest(content=content[:25]), redirect_stderr(self.log):
                source = self.directory / "accounts.csv"
                source.write_bytes(content)
                with patch.object(backup, "Http") as http, patch("builtins.input") as prompt:
                    self.assertEqual(backup.main(["--accounts-csv", str(source)]), 1)
                    http.assert_not_called()
                    prompt.assert_not_called()
        self.assertNotIn("secret-password", self.log.getvalue())

    def test_csv_missing_file_is_reported_without_network(self):
        with patch.object(backup, "Http") as http, redirect_stderr(self.log):
            self.assertEqual(backup.main(["--accounts-csv", str(self.directory / "missing.csv")]), 1)
        http.assert_not_called()
        self.assertIn("Cannot read the accounts CSV", self.log.getvalue())

    def test_csv_accounts_continue_after_auth_failure_and_retry_then_deduplicate_sites(self):
        source = self.directory / "accounts.csv"
        source.write_text("name,user,password\n"
                          "Wrong password,bad@example.invalid,bad-secret\n"
                          "Power User,reader@example.invalid,reader-secret\n"
                          "First Owner,owner@example.invalid,owner-secret\n"
                          "Second Owner,other@example.invalid,other-secret\n", encoding="utf-8")
        owners = {}
        attempted_downloads = []
        revocations = []
        password_by_user = {"bad": "bad-secret", "reader": "reader-secret",
                            "owner": "owner-secret", "other": "other-secret"}

        def handler(req):
            parts = parse.urlsplit(req.full_url)
            if req.method == "POST":
                body = json.loads(req.data)
                user = body["username"].split("@")[0]
                self.assertEqual(body["password"], password_by_user[user])
                if user == "bad":
                    return Response({"error": "invalid_grant", "description": "bad-secret"}, 401)
                token = "nxcdb-csv-" + str(len(owners))
                owners[token] = user
                return Response({"access_token": token, "refresh_token": "refresh-" + token})
            if req.method == "DELETE":
                revocations.append(req.full_url)
                return Response(status=204)
            user = owners[req.get_header("Authorization").removeprefix("Bearer ")]
            if parts.path == "/cdb/systems":
                if parts.query == "directlyShared=false":
                    return Response([])
                # All accounts see the same first site. The last account also
                # owns a different site with the same name, requiring a suffix.
                sites = [{"id": OWNED, "name": "Warehouse"}]
                if user == "other":
                    sites.append({"id": SHARED, "name": "Warehouse"})
                return Response(sites)
            attempted_downloads.append((user, parts.hostname.split(".")[0]))
            if user == "reader":
                return Response({"errorId": "forbidden"}, 403)
            return Response(self.database)

        output = self.directory / "backups"
        opener = Opener(handler)
        http = backup.Http(opener=opener)
        with patch.object(backup, "Http", return_value=http), \
                patch("builtins.input", side_effect=AssertionError("CSV must not prompt")), \
                patch.object(backup, "secret", side_effect=AssertionError("CSV must not prompt")):
            result = backup.main(["--accounts-csv", str(source), "-o", str(output)])
        self.assertEqual(result, 2)  # One account could not be checked.
        self.assertEqual(attempted_downloads, [("reader", OWNED), ("owner", OWNED), ("other", SHARED)])
        self.assertEqual((output / "Warehouse.db").read_bytes(), self.database)
        self.assertEqual((output / f"Warehouse -- {SHARED}.db").read_bytes(), self.database)
        self.assertEqual(len(list(output.iterdir())), 2)
        self.assertEqual(len(revocations), len(owners) * 2)
        self.assertIn("2 saved, 0 existing files skipped, 0 failed", self.log.getvalue())
        self.assertIn("1 login/discovery failures", self.log.getvalue())
        self.assertIn("ALREADY SAVED", self.log.getvalue())
        for password in password_by_user.values():
            self.assertNotIn(password, self.log.getvalue())

    def test_csv_account_discovery_failure_does_not_stop_next_account(self):
        accounts = [backup.Account("Broken discovery", "first@example.invalid", PASSWORD, 2),
                    backup.Account("Good discovery", "second@example.invalid", PASSWORD, 3)]
        current_user = ""
        deleted = []
        def handler(req):
            nonlocal current_user
            if req.method == "POST":
                current_user = json.loads(req.data)["username"]
            if req.method == "DELETE":
                deleted.append(req.full_url)
            auth = self.tokens(req)
            if auth is not None:
                return auth
            if "/cdb/systems" in req.full_url:
                if current_user.startswith("first"):
                    return Response({"error": "serviceUnavailable"}, 503)
                return Response([{"id": OWNED, "name": "Owned"}])
            return Response(self.database)
        cloud, _ = self.make_cloud(handler)
        self.assertEqual(backup.run_accounts(accounts, 0, self.directory, cloud.http), 2)
        self.assertEqual((self.directory / "Owned.db").read_bytes(), self.database)
        self.assertEqual(len(deleted), 6)
        self.assertTrue(all(account.password == "" for account in accounts))

    def test_csv_unexpected_mfa_is_reported_without_prompt_and_next_account_runs(self):
        accounts = [backup.Account("MFA account", "mfa@example.invalid", PASSWORD, 2),
                    backup.Account("Next account", "next@example.invalid", PASSWORD, 3)]
        def handler(req):
            if req.method == "POST" and json.loads(req.data)["username"].startswith("mfa"):
                return Response({"access_token": TOKEN, "error": "secondFactorRequired"}, 403)
            auth = self.tokens(req)
            return auth if auth is not None else Response([])
        cloud, opener = self.make_cloud(handler)
        with patch.object(backup.getpass, "getpass", side_effect=AssertionError("must not prompt")):
            self.assertEqual(backup.run_accounts(accounts, 0, self.directory, cloud.http), 2)
        self.assertEqual(sum(req.method == "POST" for req in opener.calls), 2)
        self.assertIn("CSV mode does not prompt", self.log.getvalue())
        self.assertIn("No sites were returned", self.log.getvalue())

    def test_csv_default_destination_and_invalid_row_exit_status(self):
        source = self.directory / "accounts.csv"
        source.write_text("name,user,password\nGood,test@example.invalid," + PASSWORD +
                          "\nInvalid,not-email,not-logged\n", encoding="utf-8")
        def handler(req):
            auth = self.tokens(req)
            if auth is not None:
                return auth
            if "/cdb/systems" in req.full_url:
                return Response([{"id": OWNED, "name": "Owned"}])
            return Response(self.database)
        cloud, _ = self.make_cloud(handler)
        with patch.object(backup, "Http", return_value=cloud.http), \
                patch.object(backup.Path, "home", return_value=self.directory), \
                patch("builtins.input", side_effect=AssertionError("must not prompt")):
            self.assertEqual(backup.main(["--accounts-csv", str(source)]), 2)
        files = list((self.directory / "NX Camera Backups").glob("*/Owned.db"))
        self.assertEqual(len(files), 1)
        self.assertEqual(files[0].read_bytes(), self.database)
        self.assertIn("1 invalid CSV rows skipped", self.log.getvalue())
        self.assertNotIn("not-logged", self.log.getvalue())

    def test_backup_retry_succeeds_with_another_account_without_failed_final_status(self):
        attempts = 0
        def handler(req):
            nonlocal attempts
            auth = self.tokens(req)
            if auth is not None:
                return auth
            if "/cdb/systems" in req.full_url:
                return Response([{"id": OWNED, "name": "Site"}])
            attempts += 1
            return Response({"errorId": "forbidden"}, 403) if attempts == 1 else Response(self.database)
        cloud, _ = self.make_cloud(handler)
        accounts = [backup.Account("Reader", "reader@example.invalid", PASSWORD, 2),
                    backup.Account("Owner", "owner@example.invalid", PASSWORD, 3)]
        self.assertEqual(backup.run_accounts(accounts, 0, self.directory, cloud.http), 0)
        self.assertEqual(attempts, 2)
        self.assertIn("1 saved, 0 existing files skipped, 0 failed", self.log.getvalue())

    def test_csv_interrupt_cleans_up_current_session_and_remaining_passwords(self):
        accounts = [backup.Account("First", "first@example.invalid", PASSWORD, 2),
                    backup.Account("Second", "second@example.invalid", PASSWORD, 3)]
        def handler(req):
            auth = self.tokens(req)
            if auth is not None:
                return auth
            raise KeyboardInterrupt()
        cloud, opener = self.make_cloud(handler)
        with self.assertRaises(KeyboardInterrupt):
            backup.run_accounts(accounts, 0, self.directory, cloud.http)
        self.assertEqual(sum(req.method == "POST" for req in opener.calls), 1)
        self.assertEqual(sum(req.method == "DELETE" for req in opener.calls), 2)
        self.assertTrue(all(account.password == "" for account in accounts))


if __name__ == "__main__":
    unittest.main()
