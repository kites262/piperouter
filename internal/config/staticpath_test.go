package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigBaseDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "piperouter.yaml")
	got := ConfigBaseDir(path)
	// Compare cleaned absolute forms.
	want, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("ConfigBaseDir = %q, want %q", got, want)
	}
	if ConfigBaseDir("") != "" {
		t.Fatal(`ConfigBaseDir("") should be empty`)
	}
}

func TestResolveStaticFilePathAbsolute(t *testing.T) {
	abs := filepath.Join(t.TempDir(), "index.html")
	got, err := ResolveStaticFilePath(abs, "")
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Clean(abs) {
		t.Fatalf("got %q, want %q", got, filepath.Clean(abs))
	}
}

func TestResolveStaticFilePathRelative(t *testing.T) {
	base := t.TempDir()
	// Create nested file for later validate tests; resolve itself does not Stat.
	got, err := ResolveStaticFilePath("www/index.html", base)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Clean(filepath.Join(base, "www/index.html"))
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	// Dot-relative form.
	got2, err := ResolveStaticFilePath("./www/index.html", base)
	if err != nil {
		t.Fatal(err)
	}
	if got2 != want {
		t.Fatalf("./ form: got %q, want %q", got2, want)
	}
}

func TestResolveStaticFilePathRelativeNeedsBase(t *testing.T) {
	_, err := ResolveStaticFilePath("www/index.html", "")
	if err == nil || !strings.Contains(err.Error(), "relative") {
		t.Fatalf("err = %v, want relative error", err)
	}
}

func TestResolveStaticFilePathAllowsDotDotOutsideBase(t *testing.T) {
	// base = <tmp>/cfg ; sibling file = <tmp>/files/index.html
	root := t.TempDir()
	base := filepath.Join(root, "cfg")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	sibling := filepath.Join(root, "files", "index.html")
	if err := os.MkdirAll(filepath.Dir(sibling), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sibling, []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := ResolveStaticFilePath("../files/index.html", base)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Clean(sibling)
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestResolveStaticFilePathRejectsURLAndDir(t *testing.T) {
	base := t.TempDir()
	if _, err := ResolveStaticFilePath("file:///etc/passwd", base); err == nil {
		t.Fatal("expected URL rejection")
	}
	if _, err := ResolveStaticFilePath(base+"/", base); err == nil {
		t.Fatal("expected trailing-separator rejection")
	}
}

func TestValidateAndBuildRelativeStatic(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "piperouter.yaml")
	fileRel := "landing.html"
	fileAbs := filepath.Join(dir, fileRel)
	if err := os.WriteFile(fileAbs, []byte("<html>ok</html>"), 0o644); err != nil {
		t.Fatal(err)
	}

	c := &Config{
		Version: SupportedVersion,
		Routes: []RouteConfig{
			{
				Name:   "home",
				Type:   RouteTypeStatic,
				Prefix: "/",
				// Relative — must resolve against the config dir.
				Static: &StaticOptions{File: fileRel},
			},
		},
	}
	c.Normalize()
	base := ConfigBaseDir(cfgPath)
	if err := Validate(c, base); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	// Config must keep the relative form (no rewrite into absolute).
	if c.Routes[0].Static.File != fileRel {
		t.Fatalf("config file rewritten to %q, want relative %q", c.Routes[0].Static.File, fileRel)
	}
}

func TestValidateRelativeStaticRejectsWithoutBase(t *testing.T) {
	c := &Config{
		Version: SupportedVersion,
		Routes: []RouteConfig{
			{Name: "home", Type: RouteTypeStatic, Prefix: "/", Static: &StaticOptions{File: "x.html"}},
		},
	}
	c.Normalize()
	err := Validate(c, "")
	if err == nil {
		t.Fatal("expected error without baseDir")
	}
}
