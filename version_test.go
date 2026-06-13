package main

import (
	"strings"
	"testing"
)

func TestGetVersionString(t *testing.T) {
	s := GetVersionString()
	if !strings.Contains(s, VERSION_APPLICATION) {
		t.Fatalf("version string missing %q: %s", VERSION_APPLICATION, s)
	}
	if !strings.Contains(s, "homer-data-generator") {
		t.Fatalf("version string missing app name: %s", s)
	}
}
