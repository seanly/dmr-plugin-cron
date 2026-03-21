package main

import (
	"log"
	"os"
	"path/filepath"
	"strings"
)

// resolveStoragePath turns cfg into an absolute path for file/sqlite path fields.
func resolveStoragePath(p, configBaseDir string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	if strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return p
		}
		p = filepath.Join(home, p[2:])
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	base := strings.TrimSpace(configBaseDir)
	if base == "" {
		cwd, err := os.Getwd()
		if err != nil {
			log.Printf("dmr-plugin-cron: config_base_dir empty and Getwd failed: %v; using relative path %q as-is", err, p)
			return p
		}
		log.Printf("dmr-plugin-cron: warning: config_base_dir not set; resolving %q relative to CWD %s", p, cwd)
		abs, err := filepath.Abs(filepath.Join(cwd, p))
		if err != nil {
			return filepath.Join(cwd, p)
		}
		return abs
	}
	abs, err := filepath.Abs(filepath.Join(base, p))
	if err != nil {
		return filepath.Join(base, p)
	}
	return abs
}

// resolveSQLiteDSN expands a modernc/sqlite DSN: if it uses file:relative, join with configBaseDir.
func resolveSQLiteDSN(dsn, configBaseDir string) string {
	dsn = strings.TrimSpace(dsn)
	if dsn == "" {
		return ""
	}
	const prefix = "file:"
	if !strings.HasPrefix(dsn, prefix) {
		return dsn
	}
	rest := strings.TrimPrefix(dsn, prefix)
	// strip query for path check
	pathPart := rest
	if i := strings.Index(rest, "?"); i >= 0 {
		pathPart = rest[:i]
	}
	if pathPart == "" {
		return dsn
	}
	if filepath.IsAbs(pathPart) || strings.HasPrefix(pathPart, "~/") {
		return dsn
	}
	base := strings.TrimSpace(configBaseDir)
	if base == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return dsn
		}
		base = cwd
	}
	absPath, err := filepath.Abs(filepath.Join(base, pathPart))
	if err != nil {
		absPath = filepath.Join(base, pathPart)
	}
	if i := strings.Index(rest, "?"); i >= 0 {
		return prefix + absPath + rest[i:]
	}
	return prefix + absPath
}
