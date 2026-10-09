package service

import (
	"testing"
)

func TestBuildOfficialUpdateManifestURL(t *testing.T) {
	tests := []struct {
		name           string
		serviceURL     string
		releaseVersion string
		want           string
		wantErr        bool
	}{
		{
			name:           "stable version",
			serviceURL:     "https://updates.example.com/",
			releaseVersion: "2.0.0",
			want:           "https://updates.example.com/v1/releases/2.0.0/manifest",
		},
		{
			name:           "version prefix and prerelease",
			serviceURL:     " https://updates.example.com/base/ ",
			releaseVersion: "v2.0.0-test.4",
			want:           "https://updates.example.com/base/v1/releases/2.0.0-test.4/manifest",
		},
		{
			name:           "empty service url",
			serviceURL:     "",
			releaseVersion: "2.0.0",
			want:           "",
		},
		{
			name:           "invalid version",
			serviceURL:     "https://updates.example.com",
			releaseVersion: "latest",
			wantErr:        true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := buildOfficialUpdateManifestURL(test.serviceURL, test.releaseVersion)
			if test.wantErr {
				if err == nil {
					t.Fatal("buildOfficialUpdateManifestURL() expected an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("buildOfficialUpdateManifestURL() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("buildOfficialUpdateManifestURL() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		name       string
		current    string
		latest     string
		wantUpdate bool
		wantErr    bool
	}{
		{
			name:       "dev build is newer than matching release",
			current:    "1.11.2",
			latest:     "1.11.2-dev.815+85961d8",
			wantUpdate: true,
		},
		{
			name:       "matching release is older than installed dev build",
			current:    "1.11.2-dev.815+85961d8",
			latest:     "1.11.2",
			wantUpdate: false,
		},
		{
			name:       "later dev build wins",
			current:    "1.11.2-dev.815+85961d8",
			latest:     "1.11.2-dev.816+1234567",
			wantUpdate: true,
		},
		{
			name:       "dev build remains older than next release",
			current:    "1.11.2-dev.815+85961d8",
			latest:     "1.11.3",
			wantUpdate: true,
		},
		{
			name:       "other prerelease follows semver",
			current:    "1.11.2",
			latest:     "1.11.2-test.1",
			wantUpdate: false,
		},
		{
			name:       "bare dev build remains exempt",
			current:    "dev",
			latest:     "2.0.0",
			wantUpdate: false,
		},
		{
			name:       "two-segment version padded with zero",
			current:    "0.1",
			latest:     "0.1.0",
			wantUpdate: false,
		},
		{
			name:       "two-segment version upgrade",
			current:    "0.1",
			latest:     "0.2",
			wantUpdate: true,
		},
		{
			name:       "two-segment version is newer than lower three-segment",
			current:    "0.1.5",
			latest:     "0.2",
			wantUpdate: true,
		},
		{
			name:       "two-segment latest is older than current",
			current:    "0.2.5",
			latest:     "0.2",
			wantUpdate: false,
		},
		{
			name:       "four-segment version upgrade",
			current:    "0.1.2",
			latest:     "0.1.2.5",
			wantUpdate: true,
		},
		{
			name:       "four-segment current is newer",
			current:    "0.2.6.1",
			latest:     "0.2.6",
			wantUpdate: false,
		},
		{
			name:       "v prefix with two segments",
			current:    "v0.1",
			latest:     "v0.2",
			wantUpdate: true,
		},
		{
			name:    "invalid version",
			current: "1.11.2",
			latest:  "latest",
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := compareVersions(test.current, test.latest)
			if test.wantErr {
				if err == nil {
					t.Fatal("compareVersions() expected an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("compareVersions() error = %v", err)
			}
			if got != test.wantUpdate {
				t.Fatalf("compareVersions(%q, %q) = %t, want %t", test.current, test.latest, got, test.wantUpdate)
			}
		})
	}
}
