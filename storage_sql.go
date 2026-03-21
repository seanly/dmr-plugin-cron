package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	_ "github.com/lib/pq"
	_ "modernc.org/sqlite"
)

type sqlStorage struct {
	db     *sql.DB
	driver string
}

func newSQLStorage(driver, dsn string) (*sqlStorage, error) {
	db, err := sql.Open(driverName(driver), dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping %s: %w", driver, err)
	}
	s := &sqlStorage{db: db, driver: driver}
	if err := s.ensureSchema(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := s.ensureRunOnceColumn(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func driverName(d string) string {
	if d == "postgres" {
		return "postgres"
	}
	return "sqlite"
}

func (s *sqlStorage) ensureSchema() error {
	const sqliteDDL = `
CREATE TABLE IF NOT EXISTS cron_jobs (
  job_id TEXT PRIMARY KEY,
  schedule TEXT NOT NULL,
  tape_name TEXT NOT NULL,
  prompt TEXT NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 1
);`
	const pgDDL = `
CREATE TABLE IF NOT EXISTS cron_jobs (
  job_id TEXT PRIMARY KEY,
  schedule TEXT NOT NULL,
  tape_name TEXT NOT NULL,
  prompt TEXT NOT NULL,
  enabled BOOLEAN NOT NULL DEFAULT TRUE
);`
	if s.driver == "postgres" {
		_, err := s.db.Exec(pgDDL)
		return err
	}
	_, err := s.db.Exec(sqliteDDL)
	return err
}

// ensureRunOnceColumn adds run_once for databases created before the field existed.
func (s *sqlStorage) ensureRunOnceColumn() error {
	if s.driver == "postgres" {
		_, err := s.db.Exec(`ALTER TABLE cron_jobs ADD COLUMN IF NOT EXISTS run_once BOOLEAN NOT NULL DEFAULT FALSE`)
		return err
	}
	_, err := s.db.Exec(`ALTER TABLE cron_jobs ADD COLUMN run_once INTEGER NOT NULL DEFAULT 0`)
	if err == nil {
		return nil
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "duplicate column") {
		return nil
	}
	return err
}

func (s *sqlStorage) LoadJobs(ctx context.Context) ([]Job, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT job_id, schedule, tape_name, prompt, enabled, run_once FROM cron_jobs ORDER BY job_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var jobs []Job
	for rows.Next() {
		var j Job
		var en int
		var ro int
		if s.driver == "postgres" {
			var ben bool
			var bro bool
			if err := rows.Scan(&j.ID, &j.Schedule, &j.TapeName, &j.Prompt, &ben, &bro); err != nil {
				return nil, err
			}
			j.Enabled = ben
			j.RunOnce = bro
		} else {
			if err := rows.Scan(&j.ID, &j.Schedule, &j.TapeName, &j.Prompt, &en, &ro); err != nil {
				return nil, err
			}
			j.Enabled = en != 0
			j.RunOnce = ro != 0
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

func (s *sqlStorage) GetJob(ctx context.Context, id string) (*Job, error) {
	var j Job
	if s.driver == "postgres" {
		var ben bool
		var bro bool
		err := s.db.QueryRowContext(ctx,
			`SELECT job_id, schedule, tape_name, prompt, enabled, run_once FROM cron_jobs WHERE job_id = $1`, id,
		).Scan(&j.ID, &j.Schedule, &j.TapeName, &j.Prompt, &ben, &bro)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		j.Enabled = ben
		j.RunOnce = bro
		return &j, nil
	}
	var en int
	var ro int
	err := s.db.QueryRowContext(ctx,
		`SELECT job_id, schedule, tape_name, prompt, enabled, run_once FROM cron_jobs WHERE job_id = ?`, id,
	).Scan(&j.ID, &j.Schedule, &j.TapeName, &j.Prompt, &en, &ro)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	j.Enabled = en != 0
	j.RunOnce = ro != 0
	return &j, nil
}

func (s *sqlStorage) UpsertJob(ctx context.Context, j Job) error {
	if s.driver == "postgres" {
		_, err := s.db.ExecContext(ctx, `
INSERT INTO cron_jobs (job_id, schedule, tape_name, prompt, enabled, run_once)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (job_id) DO UPDATE SET
  schedule = EXCLUDED.schedule,
  tape_name = EXCLUDED.tape_name,
  prompt = EXCLUDED.prompt,
  enabled = EXCLUDED.enabled,
  run_once = EXCLUDED.run_once`,
			j.ID, j.Schedule, j.TapeName, j.Prompt, j.Enabled, j.RunOnce)
		return err
	}
	en := 0
	if j.Enabled {
		en = 1
	}
	ro := 0
	if j.RunOnce {
		ro = 1
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO cron_jobs (job_id, schedule, tape_name, prompt, enabled, run_once)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(job_id) DO UPDATE SET
  schedule = excluded.schedule,
  tape_name = excluded.tape_name,
  prompt = excluded.prompt,
  enabled = excluded.enabled,
  run_once = excluded.run_once`,
		j.ID, j.Schedule, j.TapeName, j.Prompt, en, ro)
	return err
}

func (s *sqlStorage) DeleteJob(ctx context.Context, id string) error {
	var res sql.Result
	var err error
	if s.driver == "postgres" {
		res, err = s.db.ExecContext(ctx, `DELETE FROM cron_jobs WHERE job_id = $1`, id)
	} else {
		res, err = s.db.ExecContext(ctx, `DELETE FROM cron_jobs WHERE job_id = ?`, id)
	}
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrJobNotFound
	}
	return nil
}

func (s *sqlStorage) Close() error {
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}
