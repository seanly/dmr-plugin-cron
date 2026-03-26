package main

import "testing"

func TestIsImmediateSchedule(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"now", true},
		{" Now ", true},
		{"@now", true},
		{" @NOW ", true},
		{"0 * * * *", false},
		{"@every 1m", false},
		{"", false},
	}
	for _, tc := range tests {
		if got := isImmediateSchedule(tc.in); got != tc.want {
			t.Errorf("isImmediateSchedule(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
