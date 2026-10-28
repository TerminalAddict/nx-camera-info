package updates

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Install accepts no frontend-supplied path or command. Copy the selected
// download into a private directory, hashing the actual bytes to be launched.
// The independent installer must outlive the app; retain its files on success.
func (m *Manager) Install(launch func(string) error) State {
	m.mu.Lock()
	if m.state.Status != "ready" || m.ready.Name == "" {
		s := m.state
		s.Error = "Download and verify an update before installing."
		m.mu.Unlock()
		return s
	}
	asset, path := m.ready, m.state.SavedPath
	m.state.Status, m.state.Error = "installing", ""
	m.state.Message = "Verifying the installer and preparing to close…"
	m.mu.Unlock()

	staged, err := m.stageInstaller(path, asset)
	if err == nil {
		err = launch(staged)
		if err != nil {
			os.RemoveAll(filepath.Dir(staged))
		}
	}
	m.mu.Lock()
	if err != nil {
		m.state.Status = "ready"
		m.state.Message = "Installation did not start. NX Camera Info is still open."
		m.state.Error = err.Error()
	} else {
		m.state.Message = "Installer opened. Closing NX Camera Info…"
	}
	m.mu.Unlock()
	return m.State()
}

func (m *Manager) stageInstaller(path string, asset Download) (result string, err error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() != asset.Size {
		return "", fmt.Errorf("the downloaded installer is missing or has changed; download it again")
	}
	in, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer in.Close()
	opened, err := in.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return "", fmt.Errorf("the downloaded installer changed while opening it; download it again")
	}
	root := m.cacheDir
	if root == "" {
		cache, err := os.UserCacheDir()
		if err != nil {
			return "", err
		}
		root = filepath.Join(cache, "nx-camera-info", "updates")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", err
	}
	// Only prune our own old staging directories, never user-selected downloads.
	entries, _ := os.ReadDir(root)
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "install-") {
			if info, err := entry.Info(); err == nil && time.Since(info.ModTime()) > 7*24*time.Hour {
				os.RemoveAll(filepath.Join(root, entry.Name()))
			}
		}
	}
	dir, err := os.MkdirTemp(root, "install-")
	if err != nil {
		return "", err
	}
	defer func() {
		if err != nil {
			os.RemoveAll(dir)
		}
	}()
	result = filepath.Join(dir, asset.Name)
	out, err := os.OpenFile(result, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0700)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(out, hash), io.LimitReader(in, asset.Size+1))
	closeErr := out.Close()
	if copyErr != nil {
		return "", copyErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	if n != asset.Size || hex.EncodeToString(hash.Sum(nil)) != asset.digest {
		return "", fmt.Errorf("the installer failed its size or SHA-256 recheck; download it again")
	}
	return result, nil
}
