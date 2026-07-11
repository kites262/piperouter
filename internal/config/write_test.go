package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMarshalParseRoundTripStability(t *testing.T) {
	c := baseConfig()
	c.Server.Proxy.TLS = TLSConfig{Enabled: true, CertFile: "/tls/cert.pem", KeyFile: "/tls/key.pem"}

	b1, err := Marshal(c)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	c2, err := Parse(b1)
	if err != nil {
		t.Fatalf("Parse(Marshal(c)): %v", err)
	}
	b2, err := Marshal(c2)
	if err != nil {
		t.Fatalf("Marshal(round-trip): %v", err)
	}
	if !bytes.Equal(b1, b2) {
		t.Errorf("round-trip not stable:\nfirst:\n%s\nsecond:\n%s", b1, b2)
	}
}

func TestMarshalUsesTwoSpaceIndent(t *testing.T) {
	out, err := Marshal(baseConfig())
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "\n  proxy:\n") {
		t.Errorf("expected 2-space indented 'proxy:' block, got:\n%s", s)
	}
	if strings.Contains(s, "\n    proxy:\n") {
		t.Errorf("proxy block indented more than 2 spaces:\n%s", s)
	}
}

func TestRevision(t *testing.T) {
	c := baseConfig()

	r1, err := Revision(c)
	if err != nil {
		t.Fatalf("Revision: %v", err)
	}
	if !strings.HasPrefix(r1, "sha256:") {
		t.Errorf("Revision = %q, want sha256: prefix", r1)
	}
	if len(r1) != len("sha256:")+64 {
		t.Errorf("Revision length = %d, want %d", len(r1), len("sha256:")+64)
	}

	// Stable for the same content, including deep copies.
	r2, err := Revision(c)
	if err != nil {
		t.Fatalf("Revision: %v", err)
	}
	if r1 != r2 {
		t.Errorf("Revision not stable: %q vs %q", r1, r2)
	}
	rClone, err := Revision(c.Clone())
	if err != nil {
		t.Fatalf("Revision(clone): %v", err)
	}
	if rClone != r1 {
		t.Errorf("Revision(clone) = %q, want %q", rClone, r1)
	}

	// Changes when the config changes.
	mod := c.Clone()
	mod.Routes[0].Prefix = "/changed"
	rMod, err := Revision(mod)
	if err != nil {
		t.Fatalf("Revision(modified): %v", err)
	}
	if rMod == r1 {
		t.Error("Revision unchanged after config modification")
	}
}

func TestWriteAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "piperouter.yaml")

	c1 := baseConfig()
	if err := WriteAtomic(path, c1); err != nil {
		t.Fatalf("WriteAtomic (first): %v", err)
	}
	if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
		t.Errorf("first write must not create a backup, stat err = %v", err)
	}
	firstBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	wantFirst, err := Marshal(c1)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstBytes, wantFirst) {
		t.Error("written file does not match Marshal output")
	}
	if _, err := Load(path); err != nil {
		t.Fatalf("Load(written file): %v", err)
	}

	// Second write: previous content must survive in <path>.bak.
	c2 := c1.Clone()
	c2.Routes[0].Prefix = "/changed"
	if err := WriteAtomic(path, c2); err != nil {
		t.Fatalf("WriteAtomic (second): %v", err)
	}
	bakBytes, err := os.ReadFile(path + ".bak")
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if !bytes.Equal(bakBytes, firstBytes) {
		t.Error(".bak does not contain the previous file content")
	}
	newBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	wantSecond, err := Marshal(c2)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(newBytes, wantSecond) {
		t.Error("file content after second write does not match new config")
	}

	// No stray temp files may remain.
	assertNoTempFiles(t, dir)

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("directory should contain exactly config + backup, got %v", names)
	}
}

func TestWriteAtomicLeavesOriginalIntactOnError(t *testing.T) {
	dir := t.TempDir()
	// Destination is a directory: the rename/backup step must fail, and no
	// temp file may be left behind.
	dest := filepath.Join(dir, "piperouter.yaml")
	if err := os.Mkdir(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(dest, "keep.txt")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := WriteAtomic(dest, baseConfig()); err == nil {
		t.Fatal("WriteAtomic succeeded writing over a directory")
	}

	if got, err := os.ReadFile(sentinel); err != nil || string(got) != "keep" {
		t.Errorf("original data disturbed: %q, %v", got, err)
	}
	assertNoTempFiles(t, dir)
}

func assertNoTempFiles(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Errorf("stray temp file left behind: %s", e.Name())
		}
	}
}
