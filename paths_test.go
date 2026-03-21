package main

import (
	"path/filepath"
	"testing"
)

func TestResolveStoragePath_relativeToBase(t *testing.T) {
	base := "/etc/dmr"
	got := resolveStoragePath("data/jobs.json", base)
	want := filepath.Join(base, "data/jobs.json")
	if filepath.Clean(got) != filepath.Clean(want) {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestResolveStoragePath_absolute(t *testing.T) {
	got := resolveStoragePath("/tmp/x.json", "/etc")
	if got != filepath.Clean("/tmp/x.json") {
		t.Fatalf("got %q", got)
	}
}
