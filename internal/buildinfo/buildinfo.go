// Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
// Licensed under the Functional Source License, Version 1.1, ALv2 Future License (FSL-1.1-ALv2).

// Package buildinfo holds values stamped into the binary at link time via -ldflags.
package buildinfo

import (
	"fmt"
	"strconv"
	"strings"
)

// Version is the agent release version, overridden at link time via -ldflags.
var Version = "1.0.0"

// CompareVersions compares two version strings (e.g. "1.2.0" vs "1.0.1", "v1.2.0" vs "1.2.0").
// Returns:
//
//	-1 if v1 < v2
//	 0 if v1 == v2
//	 1 if v1 > v2
//
// Non-numeric or dev versions ("dev", "local", "") are treated as neutral (returns 0).
func CompareVersions(v1, v2 string) int {
	v1Clean := CleanVersion(v1)
	v2Clean := CleanVersion(v2)

	if v1Clean == "" || v2Clean == "" || v1Clean == "dev" || v2Clean == "dev" {
		return 0
	}

	parts1 := parseVersionParts(v1Clean)
	parts2 := parseVersionParts(v2Clean)

	maxLen := len(parts1)
	if len(parts2) > maxLen {
		maxLen = len(parts2)
	}

	for i := 0; i < maxLen; i++ {
		p1 := 0
		if i < len(parts1) {
			p1 = parts1[i]
		}
		p2 := 0
		if i < len(parts2) {
			p2 = parts2[i]
		}
		if p1 < p2 {
			return -1
		}
		if p1 > p2 {
			return 1
		}
	}
	return 0
}

// CleanVersion extracts a semantic version like "1.2.3" from strings like
// "rasnuos-agent 1.2.3", "v1.2.3", "1.2.3-rc1".
func CleanVersion(v string) string {
	v = strings.TrimSpace(v)
	for _, word := range strings.Fields(v) {
		word = strings.TrimPrefix(word, "rasnuos-agent-")
		word = strings.TrimPrefix(word, "rasnuos-agent")
		word = strings.TrimPrefix(word, "v")
		word = strings.TrimPrefix(word, "V")
		if idx := strings.IndexAny(word, "-+"); idx != -1 {
			word = word[:idx]
		}
		if isNumericSemver(word) {
			return word
		}
	}
	v = strings.TrimPrefix(v, "v")
	v = strings.TrimPrefix(v, "V")
	if idx := strings.IndexAny(v, "-+"); idx != -1 {
		v = v[:idx]
	}
	return v
}

func isNumericSemver(s string) bool {
	parts := strings.Split(s, ".")
	if len(parts) < 2 {
		return false
	}
	for _, p := range parts {
		if _, err := strconv.Atoi(p); err != nil {
			return false
		}
	}
	return true
}

func parseVersionParts(v string) []int {
	parts := strings.Split(v, ".")
	res := make([]int, 0, len(parts))
	for _, p := range parts {
		if n, err := strconv.Atoi(p); err == nil {
			res = append(res, n)
		} else {
			res = append(res, 0)
		}
	}
	return res
}

// ValidateUpdateVersion ensures that newVersion is not a downgrade from currentVersion.
// It allows equal versions (reinstall/repair) and upgrades.
func ValidateUpdateVersion(currentVersion, newVersion string) error {
	cur := CleanVersion(currentVersion)
	newV := CleanVersion(newVersion)

	if cur == "" || cur == "dev" || newV == "" || newV == "dev" {
		return nil
	}

	if CompareVersions(newV, cur) < 0 {
		return fmt.Errorf("security violation: target update version %s is older than running version %s (downgrade attack blocked)", newVersion, currentVersion)
	}
	return nil
}
