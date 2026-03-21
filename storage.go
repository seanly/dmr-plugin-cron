package main

import (
	"context"
	"errors"
	"fmt"
)

// ErrJobNotFound is returned by DeleteJob when the id does not exist.
var ErrJobNotFound = errors.New("cron job not found")

// Storage loads and mutates cron jobs (file, sqlite, postgres).
type Storage interface {
	LoadJobs(ctx context.Context) ([]Job, error)
	// GetJob returns (nil, nil) if the job does not exist.
	GetJob(ctx context.Context, id string) (*Job, error)
	UpsertJob(ctx context.Context, j Job) error
	// DeleteJob returns ErrJobNotFound if id is missing.
	DeleteJob(ctx context.Context, id string) error
	Close() error
}

func openStorage(driver, path, dsn, configBaseDir string) (Storage, error) {
	switch driver {
	case "file":
		if path == "" {
			return nil, fmt.Errorf("storage.path is required for driver=file")
		}
		abs := resolveStoragePath(path, configBaseDir)
		return newFileStorage(abs), nil
	case "sqlite":
		var d string
		if dsn != "" {
			d = resolveSQLiteDSN(dsn, configBaseDir)
		} else if path != "" {
			abs := resolveStoragePath(path, configBaseDir)
			d = "file:" + abs + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
		} else {
			return nil, fmt.Errorf("storage.dsn or storage.path is required for driver=sqlite")
		}
		return newSQLStorage("sqlite", d)
	case "postgres":
		if dsn == "" {
			return nil, fmt.Errorf("storage.dsn is required for driver=postgres")
		}
		return newSQLStorage("postgres", dsn)
	default:
		return nil, fmt.Errorf("unsupported storage.driver %q (want file, sqlite, postgres)", driver)
	}
}
