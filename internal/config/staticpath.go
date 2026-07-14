package config

import (
	"fmt"
	"path/filepath"
	"strings"
)

// ConfigBaseDir returns the absolute directory containing the configuration
// file at configPath. Relative static targets are resolved against this
// directory at validate/build time only — never on the request hot path.
//
// An empty configPath yields "". On resolution failure the non-absolute
// parent of configPath is returned as a best-effort fallback.
func ConfigBaseDir(configPath string) string {
	if configPath == "" {
		return ""
	}
	abs, err := filepath.Abs(configPath)
	if err != nil {
		return filepath.Dir(configPath)
	}
	return filepath.Dir(abs)
}

// ResolveStaticFilePath turns a static route's options.file into a cleaned
// absolute filesystem path. Absolute paths are cleaned as-is. Relative paths
// are joined with baseDir (the config file's directory). ".." segments are
// allowed and may resolve outside baseDir — same privilege as writing an
// absolute path (whoever edits the config already controls the process).
//
// This function is intended for configuration load / hot-reload / BuildTable
// only. The data plane must serve router.Route.File and must not call it per
// request.
//
// Rules:
//   - no URL schemes (file://, http://, …)
//   - no trailing separator (directories are not supported)
//   - relative paths require a non-empty baseDir
//   - result is always filepath.Clean'd and absolute
func ResolveStaticFilePath(file, baseDir string) (string, error) {
	if file == "" {
		return "", fmt.Errorf("file is required (path to a regular file)")
	}
	if strings.Contains(file, "://") {
		return "", fmt.Errorf("static file must be a filesystem path, not a URL")
	}
	if strings.HasSuffix(file, string(filepath.Separator)) || strings.HasSuffix(file, "/") {
		return "", fmt.Errorf("static file must be a file path, not a directory (trailing separator)")
	}

	var abs string
	if filepath.IsAbs(file) {
		abs = filepath.Clean(file)
	} else {
		if baseDir == "" {
			return "", fmt.Errorf("static file %q is relative; set an absolute path or load config from a file path", file)
		}
		base := baseDir
		if !filepath.IsAbs(base) {
			var err error
			base, err = filepath.Abs(base)
			if err != nil {
				return "", fmt.Errorf("resolve config base directory: %w", err)
			}
		}
		// Join + Clean collapses ".." (and may leave baseDir), matching
		// operator intent for paths like ../files/index.html.
		abs = filepath.Clean(filepath.Join(base, file))
	}
	if !filepath.IsAbs(abs) {
		return "", fmt.Errorf("static file must resolve to an absolute path")
	}
	return abs, nil
}
