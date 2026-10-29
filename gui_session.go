package main

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"sync"
	"time"
)

// The unredacted snapshot stays in Go memory. Only explicitly revealed views
// cross the desktop bridge. Nothing is saved between application sessions.
type cameraSession struct {
	mu       sync.Mutex
	path     string
	snapshot Report
	loadedAt string
	backup   []byte
	source   DatabaseSource
}

type DatabaseSource struct {
	Kind    string `json:"kind"`
	Address string `json:"address,omitempty"`
	SiteID  string `json:"siteId,omitempty"`
	Auth    string `json:"auth,omitempty"`
	Route   string `json:"route,omitempty"`
}

type ViewOptions struct {
	Filter         string `json:"filter"`
	Reveal         bool   `json:"reveal"`
	IncludeVirtual bool   `json:"includeVirtual"`
	Section        string `json:"section"`
}

type CameraView struct {
	Filename string         `json:"filename"`
	LoadedAt string         `json:"loadedAt"`
	Report   Report         `json:"report"`
	Options  ViewOptions    `json:"options"`
	Source   DatabaseSource `json:"source"`
}

func (s *cameraSession) open(path string) (CameraView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load(path, ViewOptions{})
}

func (s *cameraSession) load(path string, options ViewOptions) (CameraView, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	report, err := readReport(ctx, Options{DB: path, ShowPasswords: true, IncludeVirtual: true})
	if err != nil {
		return CameraView{}, err
	}
	clear(s.backup)
	s.backup, s.source = nil, DatabaseSource{Kind: "file"}
	s.path, s.snapshot, s.loadedAt = path, report, time.Now().Format(time.RFC3339)
	return s.filtered(options), nil
}

func (s *cameraSession) refresh(options ViewOptions) (CameraView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.path == "" {
		return CameraView{}, errors.New("open a database first")
	}
	if s.source.Kind == "server" {
		return CameraView{}, errors.New("sign in again to refresh from the server")
	}
	return s.load(s.path, options)
}

// Takes ownership of data only after successful validation. Cancellation leaves
// the previous report intact. CloseDatabase cancels downloads before clearing.
func (s *cameraSession) openBackup(ctx context.Context, data []byte, name string, source DatabaseSource) error {
	report, err := readMemoryReport(ctx, data)
	if err != nil {
		return err
	}
	return s.installBackup(ctx, data, name, source, report)
}

func (s *cameraSession) installBackup(ctx context.Context, data []byte, name string, source DatabaseSource, report Report) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	clear(s.backup)
	s.path, s.snapshot, s.loadedAt = name, report, time.Now().Format(time.RFC3339)
	s.backup, s.source = data, source
	return nil
}

func (s *cameraSession) saveBackup(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.backup) == 0 {
		return errors.New("open a server backup first")
	}
	return writeNewFile(path, s.backup)
}

func (s *cameraSession) view(options ViewOptions) (CameraView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.path == "" {
		return CameraView{}, errors.New("open a database first")
	}
	return s.filtered(options), nil
}

func (s *cameraSession) filtered(options ViewOptions) CameraView {
	r := s.snapshot
	r.Cameras = []Camera{}
	r.Summary = Summary{
		TotalRecords:      s.snapshot.Summary.TotalRecords,
		ServerVersions:    s.snapshot.Summary.ServerVersions,
		PasswordsRevealed: options.Reveal,
		Users:             s.snapshot.Summary.Users, Servers: s.snapshot.Summary.Servers,
		StorageLocations: s.snapshot.Summary.StorageLocations, Licenses: s.snapshot.Summary.Licenses,
	}
	for _, original := range s.snapshot.Cameras {
		if original.Virtual && !options.IncludeVirtual {
			r.Summary.VirtualOmitted++
			continue
		}
		if !matches(original, options.Filter) {
			continue
		}
		cam := original
		if !options.Reveal {
			if cam.HasPassword {
				cam.Password = "[redacted]"
			}
			cam.URL, cam.ServerURL, cam.ONVIFURL = redactURL(cam.URL), redactURL(cam.ServerURL), redactURL(cam.ONVIFURL)
			cam.StreamURLs = make(map[string]string, len(original.StreamURLs))
			for k, v := range original.StreamURLs {
				cam.StreamURLs[k] = redactURL(v)
			}
		}
		r.Cameras = append(r.Cameras, cam)
		switch cam.CredentialStatus {
		case "recovered":
			r.Summary.Recovered++
		case "empty":
			r.Summary.Empty++
		case "not_stored":
			r.Summary.Missing++
		case "decode_error":
			r.Summary.DecodeErrors++
		}
	}
	r.Summary.Matched = len(r.Cameras)
	return CameraView{Filename: filepath.Base(s.path), LoadedAt: s.loadedAt, Report: r, Options: options, Source: s.source}
}

func (s *cameraSession) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.path, s.snapshot, s.loadedAt = "", Report{}, ""
	clear(s.backup)
	s.backup, s.source = nil, DatabaseSource{}
}

func (s *cameraSession) export(path, format string, options ViewOptions) (int, error) {
	if !validSection(options.Section) {
		return 0, errors.New("unknown report section")
	}
	if format != "csv" && format != "json" {
		return 0, errors.New("choose CSV or JSON")
	}
	view, err := s.view(options)
	if err != nil {
		return 0, err
	}
	var data bytes.Buffer
	if err = render(&data, view.Report, Options{Format: format, Section: options.Section}); err != nil {
		return 0, err
	}
	if err = writeNewFile(path, data.Bytes()); err != nil {
		return 0, err
	}
	return len(view.Report.Cameras), nil
}
