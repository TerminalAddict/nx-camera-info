package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestHelpAndVersionWithoutDatabase(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, option := range []string{"--help", "-h", "--version"} {
		var out, stderr bytes.Buffer
		if code := run([]string{option}, &out, &stderr); code != 0 || out.Len() == 0 || stderr.Len() != 0 {
			t.Fatalf("%s: exit %d, stdout %q, stderr %q", option, code, out.String(), stderr.String())
		}
	}
}

func TestLocalDefaultDatabase(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("Linux defaults to the Nx server database path")
	}
	path, db := fixture(t)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	t.Chdir(filepath.Dir(path))
	if err := os.Rename(path, "ecs.sqlite"); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	if code := run(nil, &out, &stderr); code != 0 || !strings.Contains(out.String(), "Front door") {
		t.Fatalf("local default: exit %d, stdout %q, stderr %q", code, out.String(), stderr.String())
	}
}
