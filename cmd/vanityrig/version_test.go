package main

import "testing"

func TestResolveVersionFallsBackToDevWithoutModuleVersion(t *testing.T) {
	// Under `go test`, ReadBuildInfo reports "(devel)" for the main module, so
	// resolveVersion must keep the ldflags/default value.
	old := version
	t.Cleanup(func() { version = old })
	version = "dev"
	if got := resolveVersion(); got != "dev" {
		t.Fatalf("resolveVersion() = %q, want %q", got, "dev")
	}
	version = "1.2.3"
	if got := resolveVersion(); got != "1.2.3" {
		t.Fatalf("resolveVersion() with ldflags = %q, want 1.2.3", got)
	}
}
