package generator

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// VendorDep describes a vendored JavaScript dependency.
type VendorDep struct {
	Version string `json:"version"`
	URL     string `json:"url"`
	Out     string `json:"out"`
	SHA256  string `json:"sha256,omitempty"`
}

// VendorLock is the structure of the hamr.vendor.json lock file.
type VendorLock struct {
	Deps map[string]VendorDep `json:"deps"`
}

// defaultRegistry contains built-in JS dependency definitions.
var defaultRegistry = map[string]VendorDep{
	"htmx": {
		Version: "2.0.4",
		URL:     "https://unpkg.com/htmx.org@{{.Version}}/dist/htmx.min.js",
		Out:     "static/js/htmx.min.js",
	},
	"alpine": {
		Version: "3.14.9",
		URL:     "https://unpkg.com/alpinejs@{{.Version}}/dist/cdn.min.js",
		Out:     "static/js/alpine.min.js",
	},
	"idiomorph": {
		Version: "0.3.0",
		URL:     "https://unpkg.com/idiomorph@{{.Version}}/dist/idiomorph.min.js",
		Out:     "static/js/idiomorph.min.js",
	},
}

const lockFileName = "hamr.vendor.json"

// VendorAll downloads all registry dependencies into dir.
// If update is true, it re-downloads even if the lock file already has the dep.
func VendorAll(dir string, update bool) error {
	lock, err := readLockFile(dir)
	if err != nil {
		return err
	}

	for name, reg := range defaultRegistry {
		if err := vendorDep(dir, name, reg.Version, reg, lock, update); err != nil {
			return fmt.Errorf("vendor %s: %w", name, err)
		}
	}

	return writeLockFile(dir, lock)
}

// VendorOne downloads a single dependency by name. The name may include a
// version suffix like "alpine@3.14.9".
func VendorOne(dir, nameArg string, update bool) error {
	name, version := parseNameVersion(nameArg)

	reg, ok := defaultRegistry[name]
	if !ok {
		return fmt.Errorf("unknown dependency %q (known: htmx, alpine, idiomorph)", name)
	}

	if version != "" {
		reg.Version = version
	}

	lock, err := readLockFile(dir)
	if err != nil {
		return err
	}

	if err := vendorDep(dir, name, reg.Version, reg, lock, update); err != nil {
		return fmt.Errorf("vendor %s: %w", name, err)
	}

	return writeLockFile(dir, lock)
}

// VendorVerify checks that all locked dependencies exist on disk and match
// their recorded SHA256 checksums.
func VendorVerify(dir string) error {
	lock, err := readLockFile(dir)
	if err != nil {
		return err
	}

	if len(lock.Deps) == 0 {
		return fmt.Errorf("no dependencies in %s", lockFileName)
	}

	var mismatches []string
	for name, dep := range lock.Deps {
		path := filepath.Join(dir, dep.Out)
		actual, err := checksumFile(path)
		if err != nil {
			mismatches = append(mismatches, fmt.Sprintf("%s: %v", name, err))
			continue
		}
		if actual != dep.SHA256 {
			mismatches = append(mismatches, fmt.Sprintf("%s: checksum mismatch (expected %s, got %s)", name, dep.SHA256, actual))
		}
	}

	if len(mismatches) > 0 {
		return fmt.Errorf("verification failed:\n  %s", strings.Join(mismatches, "\n  "))
	}

	return nil
}

// VendorCustom downloads a file from an arbitrary URL and saves it at the
// given output path relative to dir.
func VendorCustom(dir, url, out string) error {
	destPath := filepath.Join(dir, out)
	hash, err := downloadAndChecksum(url, destPath)
	if err != nil {
		return err
	}

	lock, err := readLockFile(dir)
	if err != nil {
		return err
	}

	// Use the output filename (without extension) as the dep name.
	name := strings.TrimSuffix(filepath.Base(out), filepath.Ext(out))
	lock.Deps[name] = VendorDep{
		URL:    url,
		Out:    out,
		SHA256: hash,
	}

	return writeLockFile(dir, lock)
}

// vendorDep handles the logic for a single dependency: skip if already valid,
// otherwise download and record in the lock file.
func vendorDep(dir, name, version string, reg VendorDep, lock *VendorLock, update bool) error {
	existing, locked := lock.Deps[name]

	// If not updating and the lock file already has this dep at the same
	// version, verify the file on disk instead of re-downloading.
	if !update && locked && existing.Version == version {
		path := filepath.Join(dir, existing.Out)
		actual, err := checksumFile(path)
		if err == nil && actual == existing.SHA256 {
			return nil // already valid
		}
		// Fall through to re-download if file is missing or checksum differs.
	}

	url := resolveURL(reg.URL, version)
	destPath := filepath.Join(dir, reg.Out)

	hash, err := downloadAndChecksum(url, destPath)
	if err != nil {
		return err
	}

	lock.Deps[name] = VendorDep{
		Version: version,
		URL:     url,
		Out:     reg.Out,
		SHA256:  hash,
	}

	return nil
}

// readLockFile reads the lock file from dir. Returns an empty VendorLock if
// the file doesn't exist.
func readLockFile(dir string) (*VendorLock, error) {
	path := filepath.Join(dir, lockFileName)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &VendorLock{Deps: make(map[string]VendorDep)}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read lock file: %w", err)
	}

	var lock VendorLock
	if err := json.Unmarshal(data, &lock); err != nil {
		return nil, fmt.Errorf("parse lock file: %w", err)
	}
	if lock.Deps == nil {
		lock.Deps = make(map[string]VendorDep)
	}
	return &lock, nil
}

// writeLockFile writes the lock file to dir as indented JSON using an atomic
// temp-file + rename pattern.
func writeLockFile(dir string, lock *VendorLock) error {
	data, err := json.MarshalIndent(lock, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal lock file: %w", err)
	}
	data = append(data, '\n')

	path := filepath.Join(dir, lockFileName)
	tmp := path + ".tmp"

	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("create temp lock file: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("write temp lock file: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("sync temp lock file: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("close temp lock file: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename temp lock file: %w", err)
	}
	return nil
}

// downloadAndChecksum fetches url via HTTP GET, writes the body to destPath,
// and returns the SHA256 hex digest of the downloaded content.
func downloadAndChecksum(url, destPath string) (string, error) {
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}

	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return "", fmt.Errorf("create directory: %w", err)
	}

	f, err := os.Create(destPath)
	if err != nil {
		return "", fmt.Errorf("create file %s: %w", destPath, err)
	}
	defer func() { _ = f.Close() }()

	h := sha256.New()
	if _, err := io.Copy(f, io.TeeReader(resp.Body, h)); err != nil {
		return "", fmt.Errorf("write %s: %w", destPath, err)
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

// checksumFile computes the SHA256 hex digest of a file.
func checksumFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// resolveURL replaces the {{.Version}} placeholder in a URL template.
func resolveURL(urlTemplate, version string) string {
	return strings.ReplaceAll(urlTemplate, "{{.Version}}", version)
}

// parseNameVersion splits "name@version" into its parts.
// If there is no "@", version is empty.
func parseNameVersion(s string) (name, version string) {
	if i := strings.LastIndex(s, "@"); i >= 0 {
		return s[:i], s[i+1:]
	}
	return s, ""
}
