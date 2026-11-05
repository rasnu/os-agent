// Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
// Licensed under the Functional Source License, Version 1.1, ALv2 Future License (FSL-1.1-ALv2).

package buildinfo

import (
	"testing"
)

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		v1       string
		v2       string
		expected int
	}{
		{"1.0.0", "1.0.0", 0},
		{"1.2.0", "1.0.0", 1},
		{"1.0.0", "1.2.0", -1},
		{"1.2.1", "1.2.0", 1},
		{"1.2.0", "1.2.1", -1},
		{"2.0.0", "1.99.99", 1},
		{"v1.2.0", "1.2.0", 0},
		{"rasnuos-agent 1.2.0", "1.0.0", 1},
		{"rasnuos-agent 1.0.0", "1.2.0", -1},
		{"1.2.0-rc1", "1.2.0", 0},
		{"dev", "1.0.0", 0},
		{"1.0.0", "dev", 0},
	}

	for _, tt := range tests {
		res := CompareVersions(tt.v1, tt.v2)
		if res != tt.expected {
			t.Errorf("CompareVersions(%q, %q) = %d; expected %d", tt.v1, tt.v2, res, tt.expected)
		}
	}
}

func TestValidateUpdateVersion(t *testing.T) {
	// Upgrades should succeed
	if err := ValidateUpdateVersion("1.0.0", "1.2.0"); err != nil {
		t.Errorf("Expected upgrade from 1.0.0 to 1.2.0 to succeed, got: %v", err)
	}

	// Reinstalling exact same version should succeed
	if err := ValidateUpdateVersion("1.2.0", "1.2.0"); err != nil {
		t.Errorf("Expected same version 1.2.0 to succeed, got: %v", err)
	}

	// Downgrade must fail
	if err := ValidateUpdateVersion("1.2.0", "1.0.0"); err == nil {
		t.Errorf("Expected downgrade from 1.2.0 to 1.0.0 to fail, but it succeeded")
	}

	// Downgrade with binary output string format must fail
	if err := ValidateUpdateVersion("1.2.0", "rasnuos-agent 1.0.1\n"); err == nil {
		t.Errorf("Expected downgrade from 1.2.0 to 'rasnuos-agent 1.0.1' to fail, but it succeeded")
	}

	// Dev versions should bypass restriction
	if err := ValidateUpdateVersion("dev", "1.0.0"); err != nil {
		t.Errorf("Expected dev version to bypass check, got: %v", err)
	}
}
