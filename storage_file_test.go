package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestFileStorageLoadJobs(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "jobs.json")
	content := `{"jobs":[{"id":"a","schedule":"0 0 * * *","tape_name":"web","prompt":"hi","enabled":true}]}`
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	s := newFileStorage(p)
	jobs, err := s.LoadJobs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].ID != "a" || !jobs[0].Enabled {
		t.Fatalf("%+v", jobs)
	}
}
