package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Marshal serializes c as canonical YAML with 2-space indentation (PRD §6.7).
// The output is deterministic for a given config, which makes it suitable as
// the input of Revision.
func Marshal(c *Config) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(c); err != nil {
		_ = enc.Close()
		return nil, fmt.Errorf("marshal configuration: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("marshal configuration: %w", err)
	}
	return buf.Bytes(), nil
}

// Revision returns the content-addressed identity of c:
// "sha256:" + hex(sha256(Marshal(c))).
func Revision(c *Config) (string, error) {
	data, err := Marshal(c)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// WriteAtomic persists c to path following the PRD §6.5 flow:
// write to a same-directory temp file, fsync, close, back up any existing
// destination to <path>.bak, atomically rename the temp file over the
// destination, then fsync the directory (best effort).
//
// On any error the original file at path remains intact and the temp file is
// removed.
func WriteAtomic(path string, c *Config) error {
	data, err := Marshal(c)
	if err != nil {
		return err
	}

	dir := filepath.Dir(path)
	base := filepath.Base(path)

	// Preserve the existing file mode when replacing; default otherwise.
	mode := os.FileMode(0o644)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}

	tmp, err := os.CreateTemp(dir, base+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp config file: %w", err)
	}
	tmpName := tmp.Name()
	fail := func(step string, err error) error {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("%s: %w", step, err)
	}

	if _, err := tmp.Write(data); err != nil {
		return fail("write temp config file", err)
	}
	if err := tmp.Sync(); err != nil {
		return fail("sync temp config file", err)
	}
	if err := tmp.Chmod(mode); err != nil {
		return fail("chmod temp config file", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("close temp config file: %w", err)
	}

	// Back up the previous content before it is replaced (PRD §6.5).
	prev, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := os.WriteFile(path+".bak", prev, mode); err != nil {
			_ = os.Remove(tmpName)
			return fmt.Errorf("write config backup: %w", err)
		}
	case errors.Is(err, os.ErrNotExist):
		// First write: nothing to back up.
	default:
		_ = os.Remove(tmpName)
		return fmt.Errorf("read existing config for backup: %w", err)
	}

	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("replace config file: %w", err)
	}

	// Best effort: make the rename itself durable.
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}
