// Package updates checks public GitHub releases and downloads verified installers.
// It has no access to camera databases or credentials.
package updates

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

const ProjectURL = "https://github.com/TerminalAddict/nx-camera-info"
const latestURL = "https://api.github.com/repos/TerminalAddict/nx-camera-info/releases/latest"
const maxInstallerSize = 2 << 30

type Download struct {
	Name        string `json:"name"`
	Label       string `json:"label"`
	Size        int64  `json:"size"`
	url, digest string
}

type State struct {
	Status         string     `json:"status"`
	CurrentVersion string     `json:"currentVersion"`
	LatestVersion  string     `json:"latestVersion"`
	Message        string     `json:"message"`
	Error          string     `json:"error"`
	ReleaseURL     string     `json:"releaseURL"`
	Downloads      []Download `json:"downloads"`
	Received       int64      `json:"received"`
	Total          int64      `json:"total"`
	SavedPath      string     `json:"savedPath"`
}

type Manager struct {
	mu                        sync.Mutex
	state                     State
	client                    *http.Client
	platform, arch, preferred string
	cancel                    context.CancelFunc
}

func New(current, platform, arch, preferred string, transport http.RoundTripper) *Manager {
	return &Manager{
		state:    State{Status: "idle", CurrentVersion: current, Message: "Check GitHub for a new version."},
		platform: platform, arch: arch, preferred: preferred,
		client: &http.Client{Timeout: 30 * time.Minute, CheckRedirect: trustedRedirect, Transport: transport},
	}
}

func (m *Manager) State() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.state
	s.Downloads = slices.Clone(s.Downloads)
	return s
}

func busy(status string) bool {
	return status == "checking" || status == "choosing" || status == "downloading"
}

func (m *Manager) Check() State {
	m.mu.Lock()
	if busy(m.state.Status) {
		m.mu.Unlock()
		return m.State()
	}
	current := m.state.CurrentVersion
	m.state = State{Status: "checking", CurrentVersion: current, Message: "Checking GitHub…"}
	m.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	next, err := m.check(ctx, current)
	if err != nil {
		next = State{Status: "error", CurrentVersion: current, Message: "Could not check for updates.", Error: err.Error()}
	}
	m.mu.Lock()
	m.state = next
	m.mu.Unlock()
	return m.State()
}

type release struct {
	Tag        string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	Assets     []struct {
		Name   string `json:"name"`
		URL    string `json:"browser_download_url"`
		Digest string `json:"digest"`
		Size   int64  `json:"size"`
	} `json:"assets"`
}

func (m *Manager) check(ctx context.Context, current string) (State, error) {
	s := State{CurrentVersion: current}
	resp, err := m.get(ctx, latestURL, "application/vnd.github+json")
	if err != nil {
		return s, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
	if err != nil || len(data) > 2<<20 {
		return s, fmt.Errorf("GitHub returned an incomplete or oversized release response; try again later")
	}
	var r release
	if err := json.Unmarshal(data, &r); err != nil {
		return s, fmt.Errorf("GitHub returned an invalid release response")
	}
	latest, err := parseVersion(r.Tag)
	if err != nil || r.Draft || r.Prerelease {
		return s, fmt.Errorf("GitHub did not return a published stable release")
	}
	installed, err := parseVersion(current)
	if err != nil {
		return s, fmt.Errorf("this development version cannot be compared with stable releases")
	}
	s.LatestVersion = strings.TrimPrefix(r.Tag, "v")
	s.ReleaseURL = ProjectURL + "/releases/tag/" + r.Tag
	if slices.Compare(latest[:], installed[:]) <= 0 {
		s.Status = "current"
		s.Message = "You’re up to date. No newer stable release is available."
		return s, nil
	}
	s.Status = "available"
	s.Message = "Version " + s.LatestVersion + " is available. Choose an installer to download."
	platform := m.platform
	if platform == "darwin" {
		platform = "macos"
	}
	prefix := "nx-camera-info-gui-" + s.LatestVersion + "-" + platform + "-" + m.arch
	suffixes := map[string][]string{"linux": {".deb", ".rpm", ".tar.gz"}, "windows": {"-setup.exe"}, "darwin": {".pkg", ".dmg"}}[m.platform]
	if m.arch != "amd64" && m.arch != "arm64" {
		suffixes = nil
	}
	labels := map[string]string{".deb": "Debian / Ubuntu (.deb)", ".rpm": "Fedora / RPM (.rpm)", ".tar.gz": "Linux archive (.tar.gz)", "-setup.exe": "Windows installer (.exe)", ".pkg": "macOS installer (.pkg)", ".dmg": "macOS disk image (.dmg)"}
	for _, suffix := range suffixes {
		name := prefix + suffix
		for _, a := range r.Assets {
			if a.Name != name {
				continue
			}
			expected := ProjectURL + "/releases/download/" + r.Tag + "/" + name
			digest, err := hex.DecodeString(strings.TrimPrefix(a.Digest, "sha256:"))
			if a.URL != expected || !strings.HasPrefix(a.Digest, "sha256:") || err != nil || len(digest) != sha256.Size || a.Size <= 0 || a.Size > maxInstallerSize {
				continue
			}
			d := Download{Name: name, Label: labels[suffix], Size: a.Size, url: expected, digest: strings.ToLower(a.Digest[7:])}
			if suffix == m.preferred {
				s.Downloads = append([]Download{d}, s.Downloads...)
			} else {
				s.Downloads = append(s.Downloads, d)
			}
			break
		}
	}
	if len(s.Downloads) == 0 {
		s.Message = "Version " + s.LatestVersion + " is available, but no verified installer matches this platform. See the release page for downloads."
	}
	return s, nil
}

// Only numeric stable tags are offered. Draft and prerelease responses are rejected.
func parseVersion(value string) ([3]uint64, error) {
	var result [3]uint64
	parts := strings.Split(strings.TrimPrefix(value, "v"), ".")
	if len(parts) != 3 {
		return result, fmt.Errorf("invalid stable version")
	}
	for i, part := range parts {
		if part == "" || (len(part) > 1 && part[0] == '0') {
			return result, fmt.Errorf("invalid stable version")
		}
		for _, c := range part {
			if c < '0' || c > '9' {
				return result, fmt.Errorf("invalid stable version")
			}
		}
		n, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return result, err
		}
		result[i] = n
	}
	return result, nil
}

func trustedRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 5 {
		return fmt.Errorf("too many GitHub download redirects")
	}
	u := req.URL
	if u.Scheme != "https" || u.User != nil || (u.Port() != "" && u.Port() != "443") {
		return fmt.Errorf("untrusted download redirect")
	}
	switch u.Hostname() {
	case "github.com", "api.github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com":
		return nil
	}
	return fmt.Errorf("untrusted download redirect")
}

func (m *Manager) get(ctx context.Context, address, accept string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("User-Agent", "NX-Camera-Info/"+m.State().CurrentVersion)
	if req.URL.Hostname() == "api.github.com" {
		req.Header.Set("X-GitHub-Api-Version", "2026-03-10")
	}
	resp, err := m.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("cannot reach GitHub; check your connection and try again")
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		switch resp.StatusCode {
		case 403, 429:
			return nil, fmt.Errorf("GitHub has limited requests; try again later or visit the project page")
		case 404:
			return nil, fmt.Errorf("the release or installer is not available on GitHub")
		}
		return nil, fmt.Errorf("GitHub returned HTTP %d; try again later", resp.StatusCode)
	}
	return resp, nil
}

// Download accepts only an asset from the last check. The destination is chosen
// by the native dialog, never by frontend-provided paths or URLs.
func (m *Manager) Download(name string, choose func(string) (string, error)) State {
	m.mu.Lock()
	if busy(m.state.Status) {
		m.mu.Unlock()
		return m.State()
	}
	var asset Download
	for _, d := range m.state.Downloads {
		if d.Name == name {
			asset = d
			break
		}
	}
	if asset.Name == "" {
		m.state.Error = "Check for updates and choose an available installer first."
		m.mu.Unlock()
		return m.State()
	}
	m.state.Status = "choosing"
	m.state.Error = ""
	m.state.SavedPath = ""
	m.state.Message = "Choose where to save the installer."
	m.state.Received = 0
	m.state.Total = asset.Size
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	m.cancel = cancel
	m.mu.Unlock()
	defer cancel()
	path, err := choose(asset.Name)
	if err == nil && path != "" {
		path, err = filepath.Abs(path)
		if err == nil {
			m.mu.Lock()
			m.state.Status = "downloading"
			m.state.Message = "Downloading " + asset.Name + "…"
			m.mu.Unlock()
			err = m.download(ctx, asset, path)
		}
	}
	m.mu.Lock()
	m.cancel = nil
	m.state.Status = "available"
	switch {
	case errors.Is(err, context.Canceled), path == "" && err == nil:
		m.state.Message = "Download cancelled. You can try again whenever you’re ready."
	case err != nil:
		m.state.Message = "The download did not complete. You can try again."
		m.state.Error = err.Error()
	default:
		m.state.Status = "ready"
		m.state.SavedPath = path
		m.state.Message = "Download verified. Quit NX Camera Info, then run the installer to finish updating."
		if strings.HasSuffix(asset.Name, ".tar.gz") {
			m.state.Message = "Download verified. Quit NX Camera Info, extract the archive, then run install.sh to finish updating."
		}
	}
	m.mu.Unlock()
	return m.State()
}

func (m *Manager) Cancel() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancel != nil {
		m.cancel()
	}
}

func (m *Manager) download(ctx context.Context, asset Download, destination string) error {
	if _, err := os.Lstat(destination); err == nil {
		return fmt.Errorf("that file already exists; choose a new filename")
	} else if !os.IsNotExist(err) {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(destination), ".nx-camera-info-update-*.part")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	resp, err := m.get(ctx, asset.url, "application/octet-stream")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	hash := sha256.New()
	writer := &progressWriter{manager: m, writer: io.MultiWriter(f, hash)}
	n, err := io.Copy(writer, io.LimitReader(resp.Body, asset.Size+1))
	if err != nil {
		return fmt.Errorf("download interrupted: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if n != asset.Size || hex.EncodeToString(hash.Sum(nil)) != asset.digest {
		return fmt.Errorf("the download failed its size or SHA-256 check; no installer was saved")
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	// Exclusive creation preserves files (including symlinks) created while the
	// download was running. Only verified bytes are copied to the final filename.
	out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if os.IsExist(err) {
		return fmt.Errorf("that file already exists; choose a new filename")
	}
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		out.Close()
		if !ok {
			os.Remove(destination)
		}
	}()
	if _, err = io.Copy(out, f); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = out.Sync(); err != nil {
		return err
	}
	if err = out.Close(); err != nil {
		return err
	}
	ok = true
	return nil
}

type progressWriter struct {
	manager *Manager
	writer  io.Writer
}

func (p *progressWriter) Write(b []byte) (int, error) {
	n, err := p.writer.Write(b)
	p.manager.mu.Lock()
	p.manager.state.Received += int64(n)
	p.manager.mu.Unlock()
	return n, err
}

// ReleaseLink is constructed from a validated stable tag, never remote HTML.
func (m *Manager) ReleaseLink() string {
	link := m.State().ReleaseURL
	if u, err := url.Parse(link); err != nil || u.Scheme != "https" || !strings.HasPrefix(link, ProjectURL+"/releases/tag/") {
		return ProjectURL + "/releases/latest"
	}
	return link
}
