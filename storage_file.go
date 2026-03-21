package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type fileStorage struct {
	mu   sync.Mutex
	path string
}

func newFileStorage(path string) *fileStorage {
	return &fileStorage{path: path}
}

type jobsFileDoc struct {
	Jobs []Job `json:"jobs"`
}

func (f *fileStorage) loadDocUnlocked(ctx context.Context) (jobsFileDoc, error) {
	_ = ctx
	var doc jobsFileDoc
	data, err := os.ReadFile(f.path)
	if err != nil {
		if os.IsNotExist(err) {
			return doc, nil
		}
		return doc, fmt.Errorf("read jobs file: %w", err)
	}
	if len(data) == 0 {
		return doc, nil
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return doc, fmt.Errorf("parse jobs JSON: %w", err)
	}
	return doc, nil
}

func (f *fileStorage) saveDocUnlocked(doc jobsFileDoc) error {
	if err := os.MkdirAll(filepath.Dir(f.path), 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	dir := filepath.Dir(f.path)
	tmp, err := os.CreateTemp(dir, ".cron_jobs_*.tmp")
	if err != nil {
		return fmt.Errorf("temp file: %w", err)
	}
	tmpName := tmp.Name()
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if werr != nil {
		_ = os.Remove(tmpName)
		return werr
	}
	if cerr != nil {
		_ = os.Remove(tmpName)
		return cerr
	}
	if err := os.Rename(tmpName, f.path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

func (f *fileStorage) LoadJobs(ctx context.Context) ([]Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	doc, err := f.loadDocUnlocked(ctx)
	if err != nil {
		return nil, err
	}
	return doc.Jobs, nil
}

func (f *fileStorage) GetJob(ctx context.Context, id string) (*Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	doc, err := f.loadDocUnlocked(ctx)
	if err != nil {
		return nil, err
	}
	for i := range doc.Jobs {
		if doc.Jobs[i].ID == id {
			j := doc.Jobs[i]
			return &j, nil
		}
	}
	return nil, nil
}

func (f *fileStorage) UpsertJob(ctx context.Context, j Job) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	doc, err := f.loadDocUnlocked(ctx)
	if err != nil {
		return err
	}
	replaced := false
	for i := range doc.Jobs {
		if doc.Jobs[i].ID == j.ID {
			doc.Jobs[i] = j
			replaced = true
			break
		}
	}
	if !replaced {
		doc.Jobs = append(doc.Jobs, j)
	}
	return f.saveDocUnlocked(doc)
}

func (f *fileStorage) DeleteJob(ctx context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	doc, err := f.loadDocUnlocked(ctx)
	if err != nil {
		return err
	}
	out := doc.Jobs[:0]
	found := false
	for _, j := range doc.Jobs {
		if j.ID == id {
			found = true
			continue
		}
		out = append(out, j)
	}
	if !found {
		return ErrJobNotFound
	}
	doc.Jobs = out
	return f.saveDocUnlocked(doc)
}

func (f *fileStorage) Close() error { return nil }
