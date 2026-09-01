package modkit

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// VendorArtifact records the provenance of one third-party file committed into
// the repository.
//
// A vendored file is code someone else wrote, living in this tree, shipped to
// every user. Without provenance nobody can answer where it came from, which
// version it is, or whether the bytes on disk are the bytes that were reviewed;
// and "re-download it and compare" stops being an answer the moment the CDN
// moves on. Every field here exists to make that question answerable offline.
type VendorArtifact struct {
	// Path is the repository-relative file.
	Path string `json:"path"`
	// Source is the exact https URL the bytes came from.
	Source string `json:"source"`
	// Version is the upstream release, so the pin can be compared with a
	// changelog rather than a digest.
	Version string `json:"version"`
	// Bytes and SHA256 are the reviewed contents. Both are checked: the digest
	// catches any change, and the byte count catches the mistake that is easiest
	// to make when updating a pin by hand.
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
	// License is the SPDX identifier. LicenseAt names the retained notice file
	// when the license requires one to travel with the code.
	License   string `json:"license"`
	LicenseAt string `json:"license_at,omitempty"`
	// Origins are the non-same-origin hosts this file is permitted to contact.
	// Empty means none, which is the case for every asset here: a vendored file
	// that phones home defeats the point of vendoring it.
	Origins []string `json:"origins,omitempty"`
}

var (
	// sha256Pattern is a full digest. A truncated one is worse than none,
	// because it looks like a check that happened.
	sha256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

	// dynamicCodePatterns turn a data path into a code path. A file containing
	// one cannot be reasoned about from its inputs, and the browser CSP that
	// forbids inline script says nothing about what a loaded file does with the
	// strings it is handed.
	dynamicCodePatterns = []struct {
		name string
		re   *regexp.Regexp
	}{
		// Word-boundary anchored so `evaluate`, `.evaluation` and
		// `FunctionalThing` are not swept up - a scan that cries wolf is a scan
		// somebody switches off.
		{"eval(", regexp.MustCompile(`(^|[^.\w$])eval\s*\(`)},
		{"new Function(", regexp.MustCompile(`\bnew\s+Function\s*\(`)},
		{"setTimeout with a string", regexp.MustCompile(`\bsetTimeout\s*\(\s*["'` + "`" + `]`)},
		{"setInterval with a string", regexp.MustCompile(`\bsetInterval\s*\(\s*["'` + "`" + `]`)},
	}

	// absoluteURLPattern finds absolute references. Same-origin paths start with
	// a slash and are how every adapter loads its own assets, so only absolute
	// URLs are candidates.
	absoluteURLPattern = regexp.MustCompile(`\bhttps?://([a-zA-Z0-9.-]+)`)
)

// hostsAlwaysAllowed are references that cannot cause a request. A namespace URI
// is an identifier, not an endpoint: every SVG in the tree carries the w3.org
// namespace and none of them fetch it.
var hostsAlwaysAllowed = map[string]struct{}{
	"www.w3.org": {},
	"localhost":  {},
	"127.0.0.1":  {},
}

func validateVendors(items []VendorArtifact, canonical bool) error {
	seen := make(map[string]struct{}, len(items))
	last := ""
	for i, item := range items {
		if err := validateSafePath(item.Path); err != nil {
			return fmt.Errorf("manifest vendors[%d] path: %w", i, err)
		}
		if strings.TrimSpace(item.Version) == "" {
			return fmt.Errorf("manifest vendors[%d] version is required", i)
		}
		if item.Bytes <= 0 {
			return fmt.Errorf("manifest vendors[%d] bytes is required", i)
		}
		if !sha256Pattern.MatchString(item.SHA256) {
			return fmt.Errorf("manifest vendors[%d] sha256 must be a full lowercase digest", i)
		}
		if strings.TrimSpace(item.License) == "" {
			return fmt.Errorf("manifest vendors[%d] license is required", i)
		}
		parsed, err := url.Parse(item.Source)
		if err != nil || parsed.Host == "" {
			return fmt.Errorf("manifest vendors[%d] source must be an absolute url", i)
		}
		// https only, in every environment: a pin fetched over http cannot be
		// trusted to be the file whose digest was recorded.
		if parsed.Scheme != "https" {
			return fmt.Errorf("manifest vendors[%d] source must use https", i)
		}
		if item.LicenseAt != "" {
			if err := validateSafePath(item.LicenseAt); err != nil {
				return fmt.Errorf("manifest vendors[%d] license_at: %w", i, err)
			}
		}
		for j, origin := range item.Origins {
			if strings.TrimSpace(origin) == "" {
				return fmt.Errorf("manifest vendors[%d] origins[%d] is empty", i, j)
			}
		}
		if _, ok := seen[item.Path]; ok {
			return fmt.Errorf("manifest vendors contain duplicate path %q", item.Path)
		}
		seen[item.Path] = struct{}{}
		if canonical && i > 0 && last > item.Path {
			return fmt.Errorf("manifest vendors must be sorted by path")
		}
		last = item.Path
	}
	return nil
}

// scanVendorSource reports what makes a vendored file unreviewable: dynamic code
// execution, and references to origins the manifest did not declare.
//
// It reads the shipped bytes rather than trusting a package's own claims,
// because the bytes are what runs.
func scanVendorSource(path string, body []byte, allowed ...string) []string {
	source := string(body)
	var findings []string

	for _, pattern := range dynamicCodePatterns {
		if pattern.re.MatchString(source) {
			findings = append(findings, fmt.Sprintf("%s uses %s", path, pattern.name))
		}
	}

	permitted := map[string]struct{}{}
	for _, host := range allowed {
		permitted[strings.ToLower(strings.TrimSpace(host))] = struct{}{}
	}
	reported := map[string]struct{}{}
	for _, match := range absoluteURLPattern.FindAllStringSubmatch(source, -1) {
		host := strings.ToLower(match[1])
		if _, ok := hostsAlwaysAllowed[host]; ok {
			continue
		}
		if _, ok := permitted[host]; ok {
			continue
		}
		if _, ok := reported[host]; ok {
			continue
		}
		reported[host] = struct{}{}
		findings = append(findings, fmt.Sprintf("%s references undeclared origin %s", path, host))
	}
	return findings
}

// VerifyVendorArtifacts checks every declared artifact against the bytes on
// disk and scans them.
//
// This runs during registry build rather than in a separate audit, so a swapped
// vendor file fails the build instead of shipping. A check that has to be
// remembered is a check that eventually is not run.
func VerifyVendorArtifacts(root string, items []VendorArtifact) error {
	for _, item := range items {
		full := filepath.Join(root, filepath.FromSlash(item.Path))
		body, err := os.ReadFile(full)
		if err != nil {
			return fmt.Errorf("vendor %s: %w", item.Path, err)
		}
		if int64(len(body)) != item.Bytes {
			return fmt.Errorf("vendor %s: bytes %d on disk, manifest declares %d",
				item.Path, len(body), item.Bytes)
		}
		if digest := sha256Hex(body); digest != item.SHA256 {
			return fmt.Errorf("vendor %s: sha256 %s on disk, manifest declares %s",
				item.Path, digest, item.SHA256)
		}
		if item.LicenseAt != "" {
			notice := filepath.Join(root, filepath.FromSlash(item.LicenseAt))
			if _, err := os.Stat(notice); err != nil {
				return fmt.Errorf("vendor %s: license notice %s: %w", item.Path, item.LicenseAt, err)
			}
		}
		if findings := scanVendorSource(item.Path, body, item.Origins...); len(findings) > 0 {
			return fmt.Errorf("vendor %s rejected: %s", item.Path, strings.Join(findings, "; "))
		}
	}
	return nil
}

// VerifyCatalogVendors checks every artifact declared anywhere in the catalog.
//
// It walks the catalog rather than the directory so an undeclared file is not
// silently skipped: TestEveryVendoredFileIsDeclared covers the other direction,
// because a vendored file with no owner is also a file no removal cleans up.
func VerifyCatalogVendors(root string) error {
	catalog, err := LoadCatalog(os.DirFS(root))
	if err != nil {
		return err
	}
	for _, module := range catalog.Modules {
		if err := VerifyVendorArtifacts(root, module.Vendors); err != nil {
			return fmt.Errorf("module %s: %w", module.ID, err)
		}
	}
	return nil
}

func sha256Hex(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
