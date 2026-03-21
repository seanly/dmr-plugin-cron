package main

import (
	"context"
	"path/filepath"
	"testing"
)

func TestFileStorageUpsertDelete(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "jobs.json")
	s := newFileStorage(path)
	ctx := context.Background()

	if err := s.UpsertJob(ctx, Job{ID: "a", Schedule: "0 * * * *", TapeName: "web", Prompt: "p", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	jobs, err := s.LoadJobs(ctx)
	if err != nil || len(jobs) != 1 || jobs[0].ID != "a" {
		t.Fatalf("load %+v err=%v", jobs, err)
	}
	got, err := s.GetJob(ctx, "a")
	if err != nil || got == nil || got.Prompt != "p" {
		t.Fatalf("get %+v err=%v", got, err)
	}
	if err := s.UpsertJob(ctx, Job{ID: "a", Schedule: "1 * * * *", TapeName: "web", Prompt: "p2", Enabled: false}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetJob(ctx, "a")
	if got.Schedule != "1 * * * *" || got.Enabled {
		t.Fatalf("upsert replace failed: %+v", got)
	}
	if err := s.DeleteJob(ctx, "missing"); err != ErrJobNotFound {
		t.Fatalf("delete missing: %v", err)
	}
	if err := s.DeleteJob(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	jobs, _ = s.LoadJobs(ctx)
	if len(jobs) != 0 {
		t.Fatalf("expected empty, got %d", len(jobs))
	}
}
