package main

import (
	"context"
	"testing"
)

func TestSQLStorageSQLiteMemory(t *testing.T) {
	s, err := newSQLStorage("sqlite", "file::memory:?cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ctx := context.Background()
	jobs, err := s.LoadJobs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 0 {
		t.Fatalf("expected no rows, got %d", len(jobs))
	}

	if err := s.UpsertJob(ctx, Job{ID: "x", Schedule: "0 * * * *", TapeName: "t", Prompt: "p", Enabled: true, RunOnce: true}); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetJob(ctx, "x")
	if err != nil || got == nil {
		t.Fatalf("get: %+v %v", got, err)
	}
	if !got.RunOnce {
		t.Fatalf("run_once: %+v", got)
	}
	if err := s.DeleteJob(ctx, "x"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteJob(ctx, "x"); err != ErrJobNotFound {
		t.Fatalf("second delete: %v", err)
	}
}
