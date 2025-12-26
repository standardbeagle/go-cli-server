package client

import "testing"

func TestParseVersion(t *testing.T) {
	tests := []struct {
		input         string
		major         int
		minor         int
		patch         int
		expectError   bool
	}{
		{"1.2.3", 1, 2, 3, false},
		{"v1.2.3", 1, 2, 3, false},
		{"V1.2.3", 1, 2, 3, false},
		{"0.6.5", 0, 6, 5, false},
		{"v0.0.1", 0, 0, 1, false},
		{"1.2.3-beta", 1, 2, 3, false},
		{"1.2.3+build", 1, 2, 3, false},
		{"1.2.3-beta+build", 1, 2, 3, false},
		{"1.2", 0, 0, 0, true},
		{"1", 0, 0, 0, true},
		{"", 0, 0, 0, true},
		{"a.b.c", 0, 0, 0, true},
		{"1.b.3", 0, 0, 0, true},
		{"1.2.c", 0, 0, 0, true},
	}

	for _, tt := range tests {
		major, minor, patch, err := ParseVersion(tt.input)
		if tt.expectError {
			if err == nil {
				t.Errorf("ParseVersion(%q): expected error, got nil", tt.input)
			}
		} else {
			if err != nil {
				t.Errorf("ParseVersion(%q): unexpected error: %v", tt.input, err)
				continue
			}
			if major != tt.major || minor != tt.minor || patch != tt.patch {
				t.Errorf("ParseVersion(%q) = (%d, %d, %d), want (%d, %d, %d)",
					tt.input, major, minor, patch, tt.major, tt.minor, tt.patch)
			}
		}
	}
}

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		a      string
		b      string
		expect int
	}{
		{"1.0.0", "1.0.0", 0},
		{"v1.0.0", "1.0.0", 0},
		{"1.0.0", "v1.0.0", 0},
		{"1.0.0", "2.0.0", -1},
		{"2.0.0", "1.0.0", 1},
		{"1.1.0", "1.0.0", 1},
		{"1.0.0", "1.1.0", -1},
		{"1.0.1", "1.0.0", 1},
		{"1.0.0", "1.0.1", -1},
		{"0.6.5", "0.6.5", 0},
		{"0.6.5", "0.6.6", -1},
		{"0.7.0", "0.6.5", 1},
	}

	for _, tt := range tests {
		result, err := CompareVersions(tt.a, tt.b)
		if err != nil {
			t.Errorf("CompareVersions(%q, %q): unexpected error: %v", tt.a, tt.b, err)
			continue
		}
		if result != tt.expect {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", tt.a, tt.b, result, tt.expect)
		}
	}
}

func TestVersionsMatch(t *testing.T) {
	tests := []struct {
		a      string
		b      string
		expect bool
	}{
		{"1.0.0", "1.0.0", true},
		{"v1.0.0", "1.0.0", true},
		{"1.0.0", "v1.0.0", true},
		{"v1.0.0", "v1.0.0", true},
		{"1.0.0", "1.0.1", false},
		{"1.0.0", "2.0.0", false},
		{"invalid", "1.0.0", false},
		{"1.0.0", "invalid", false},
	}

	for _, tt := range tests {
		result := VersionsMatch(tt.a, tt.b)
		if result != tt.expect {
			t.Errorf("VersionsMatch(%q, %q) = %v, want %v", tt.a, tt.b, result, tt.expect)
		}
	}
}

func TestValidateVersion(t *testing.T) {
	tests := []struct {
		input       string
		expectValid bool
	}{
		{"1.0.0", true},
		{"v1.0.0", true},
		{"0.6.5", true},
		{"1.2.3-beta", true},
		{"1.2", false},
		{"invalid", false},
		{"", false},
	}

	for _, tt := range tests {
		err := ValidateVersion(tt.input)
		if tt.expectValid && err != nil {
			t.Errorf("ValidateVersion(%q): expected valid, got error: %v", tt.input, err)
		}
		if !tt.expectValid && err == nil {
			t.Errorf("ValidateVersion(%q): expected invalid, got nil error", tt.input)
		}
	}
}

func TestFormatVersion(t *testing.T) {
	tests := []struct {
		major  int
		minor  int
		patch  int
		expect string
	}{
		{1, 0, 0, "v1.0.0"},
		{0, 6, 5, "v0.6.5"},
		{10, 20, 30, "v10.20.30"},
	}

	for _, tt := range tests {
		result := FormatVersion(tt.major, tt.minor, tt.patch)
		if result != tt.expect {
			t.Errorf("FormatVersion(%d, %d, %d) = %q, want %q",
				tt.major, tt.minor, tt.patch, result, tt.expect)
		}
	}
}
