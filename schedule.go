package main

import "strings"

// isImmediateSchedule reports whether schedule means "run once as soon as possible"
// instead of a robfig/cron expression. Matching is case-insensitive; outer space trimmed.
func isImmediateSchedule(schedule string) bool {
	s := strings.ToLower(strings.TrimSpace(schedule))
	return s == "now" || s == "@now"
}
