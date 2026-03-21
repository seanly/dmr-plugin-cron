package main

// Job is one scheduled task loaded from storage.
type Job struct {
	ID        string `json:"id"`
	Schedule  string `json:"schedule"`
	TapeName  string `json:"tape_name"`
	Prompt    string `json:"prompt"`
	Enabled   bool   `json:"enabled"`
	// RunOnce, when true, deletes this job from storage after one successful RunAgent (RPC ok and no agent error). Omitted/false in JSON keeps repeating schedules.
	RunOnce bool `json:"run_once,omitempty"`
}
