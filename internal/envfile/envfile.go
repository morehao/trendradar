// Package envfile loads a local .env file so secrets (API keys) stay out of
// committed config. Real environment variables always win over file values.
package envfile

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// DefaultNames are tried in order; the first file found wins.
var DefaultNames = []string{".env.local", ".env"}

// LoadFile parses a KEY=VALUE file. Blank lines and lines starting with # are
// ignored; "export " prefixes, single quotes and double quotes are supported.
func LoadFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	out := make(map[string]string)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(strings.TrimPrefix(sc.Text(), "\ufeff"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		val = strings.TrimSpace(val)
		if len(val) >= 2 {
			if val[0] == '"' && val[len(val)-1] == '"' {
				if unq, err := strconv.Unquote(val); err == nil {
					val = unq
				}
			} else if val[0] == '\'' && val[len(val)-1] == '\'' {
				val = val[1 : len(val)-1]
			}
		}
		out[key] = val
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// ApplyFile sets values from path into the process environment, but never
// overwrites a variable that is already set (the shell wins).
func ApplyFile(path string) error {
	vals, err := LoadFile(path)
	if err != nil {
		return err
	}
	for k, v := range vals {
		if os.Getenv(k) == "" {
			_ = os.Setenv(k, v)
		}
	}
	return nil
}

// FindNearest returns the first DefaultNames file found by walking up from
// start. It returns "" when none exists.
func FindNearest(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		for _, name := range DefaultNames {
			p := filepath.Join(dir, name)
			if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
				return p, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", nil
		}
		dir = parent
	}
}

// ApplyDefault honors TRENDRADAR_ENV_FILE when set, otherwise searches upward
// from start for .env.local / .env. A missing file is not an error; it returns
// the empty path. It returns the loaded path (if any).
func ApplyDefault(start string) (string, error) {
	if p := os.Getenv("TRENDRADAR_ENV_FILE"); p != "" {
		if err := ApplyFile(p); err != nil {
			return p, err
		}
		return p, nil
	}
	p, err := FindNearest(start)
	if err != nil {
		return "", err
	}
	if p == "" {
		return "", nil
	}
	if err := ApplyFile(p); err != nil {
		return p, err
	}
	return p, nil
}

// ErrNotFound is returned by callers that require an explicit file.
var ErrNotFound = errors.New("env file not found")
