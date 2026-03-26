package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/seanly/dmr/pkg/plugin/proto"
)

func cronToolDefs() []proto.ToolDef {
	return []proto.ToolDef{
		{
			Name:        "cronList",
			Description: "List cron jobs loaded from the plugin storage (file or database).",
			ParametersJSON: `{
  "type": "object",
  "properties": {
    "enabled_only": {
      "type": "boolean",
      "description": "If true, return only jobs with enabled=true."
    }
  }
}`,
		},
		{
			Name:        "cronShow",
			Description: "Show one cron job by id.",
			ParametersJSON: `{
  "type": "object",
  "properties": {
    "id": { "type": "string", "description": "Job id (job_id)." }
  },
  "required": ["id"]
}`,
		},
		{
			Name:        "cronReload",
			Description: "Reload jobs from storage and rebuild the internal scheduler (same as reload_interval tick).",
			ParametersJSON: `{
  "type": "object",
  "properties": {}
}`,
		},
		{
			Name:        "cronAdd",
			Description: "Create or replace a cron job (upsert). After success, the scheduler reloads. You must set tape_name (e.g. feishu:p2p:<chat_id>) — CallTool does not receive the current tape automatically. Use run_once=true for one-shot reminders (auto-deleted after first successful run).",
			ParametersJSON: `{
  "type": "object",
  "properties": {
    "id": {
      "type": "string",
      "description": "Optional stable id; a new UUID is generated if omitted."
    },
    "schedule": {
      "type": "string",
      "description": "5-field cron (minute hour day-of-month month day-of-week, e.g. 0 20 * * *), robfig descriptor such as @every 1h, or now / @now for one immediate run (removed from storage after first successful RunAgent; failures keep the job for retry on next reload)."
    },
    "tape_name": {
      "type": "string",
      "description": "DMR tape name where RunAgent will run (e.g. feishu:p2p:oc_xxx)."
    },
    "prompt": {
      "type": "string",
      "description": "User prompt passed to RunAgent when the job fires."
    },
    "enabled": {
      "type": "boolean",
      "description": "Default true if omitted."
    },
    "run_once": {
      "type": "boolean",
      "description": "If true, remove this job from storage after the first successful RunAgent (no RPC error and empty agent error). Failed runs keep the job for the next schedule tick. Default false."
    }
  },
  "required": ["schedule", "tape_name", "prompt"]
}`,
		},
		{
			Name:        "cronRemove",
			Description: "Delete a cron job by id, then reload the scheduler.",
			ParametersJSON: `{
  "type": "object",
  "properties": {
    "id": { "type": "string", "description": "Job id to delete." }
  },
  "required": ["id"]
}`,
		},
	}
}

func (p *CronPlugin) ProvideTools(_ *proto.ProvideToolsRequest, resp *proto.ProvideToolsResponse) error {
	resp.Tools = cronToolDefs()
	return nil
}

func (p *CronPlugin) CallTool(req *proto.CallToolRequest, resp *proto.CallToolResponse) error {
	if p.storage == nil {
		resp.Error = "cron storage not initialized"
		return nil
	}
	var args map[string]any
	if req.ArgsJSON != "" && req.ArgsJSON != "null" {
		if err := json.Unmarshal([]byte(req.ArgsJSON), &args); err != nil {
			resp.Error = fmt.Sprintf("invalid args JSON: %v", err)
			return nil
		}
	}
	if args == nil {
		args = map[string]any{}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	switch req.Name {
	case "cronList":
		return p.toolList(ctx, args, resp)
	case "cronShow":
		return p.toolShow(ctx, args, resp)
	case "cronReload":
		p.reloadFromStorage()
		return writeResult(resp, map[string]any{"ok": true})
	case "cronAdd":
		return p.toolAdd(ctx, args, resp)
	case "cronRemove":
		return p.toolRemove(ctx, args, resp)
	default:
		resp.Error = fmt.Sprintf("unknown tool %q", req.Name)
		return nil
	}
}

func writeResult(resp *proto.CallToolResponse, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		resp.Error = err.Error()
		return nil
	}
	resp.ResultJSON = string(b)
	return nil
}

func (p *CronPlugin) toolList(ctx context.Context, args map[string]any, resp *proto.CallToolResponse) error {
	jobs, err := p.storage.LoadJobs(ctx)
	if err != nil {
		resp.Error = err.Error()
		return nil
	}
	only := false
	if v, ok := args["enabled_only"]; ok {
		switch t := v.(type) {
		case bool:
			only = t
		case string:
			only = strings.EqualFold(t, "true") || t == "1"
		}
	}
	if only {
		var out []Job
		for _, j := range jobs {
			if j.Enabled {
				out = append(out, j)
			}
		}
		jobs = out
	}
	return writeResult(resp, map[string]any{"jobs": jobs})
}

func (p *CronPlugin) toolShow(ctx context.Context, args map[string]any, resp *proto.CallToolResponse) error {
	id, err := requireStringArg(args, "id")
	if err != nil {
		resp.Error = err.Error()
		return nil
	}
	j, err := p.storage.GetJob(ctx, id)
	if err != nil {
		resp.Error = err.Error()
		return nil
	}
	if j == nil {
		resp.Error = fmt.Sprintf("job %q not found", id)
		return nil
	}
	return writeResult(resp, j)
}

func (p *CronPlugin) toolAdd(ctx context.Context, args map[string]any, resp *proto.CallToolResponse) error {
	schedule, err := requireStringArg(args, "schedule")
	if err != nil {
		resp.Error = err.Error()
		return nil
	}
	tape, err := requireStringArg(args, "tape_name")
	if err != nil {
		resp.Error = err.Error()
		return nil
	}
	prompt, err := requireStringArg(args, "prompt")
	if err != nil {
		resp.Error = err.Error()
		return nil
	}
	id := strings.TrimSpace(stringFromAny(args["id"]))
	if id == "" {
		id = "job-" + uuid.NewString()
	}
	enabled := true
	if v, ok := args["enabled"]; ok {
		enabled = boolFromAny(v, true)
	}
	runOnce := false
	if v, ok := args["run_once"]; ok {
		runOnce = boolFromAny(v, false)
	}
	job := Job{
		ID:        id,
		Schedule:  schedule,
		TapeName:  tape,
		Prompt:    prompt,
		Enabled:   enabled,
		RunOnce:   runOnce,
	}
	if err := p.storage.UpsertJob(ctx, job); err != nil {
		resp.Error = err.Error()
		return nil
	}
	p.reloadFromStorage()
	return writeResult(resp, map[string]any{"ok": true, "id": id})
}

func (p *CronPlugin) toolRemove(ctx context.Context, args map[string]any, resp *proto.CallToolResponse) error {
	id, err := requireStringArg(args, "id")
	if err != nil {
		resp.Error = err.Error()
		return nil
	}
	if err := p.storage.DeleteJob(ctx, id); err != nil {
		resp.Error = err.Error()
		return nil
	}
	p.reloadFromStorage()
	return writeResult(resp, map[string]any{"ok": true, "id": id})
}

func requireStringArg(args map[string]any, key string) (string, error) {
	s := strings.TrimSpace(stringFromAny(args[key]))
	if s == "" {
		return "", fmt.Errorf("missing or empty %q", key)
	}
	return s, nil
}

func stringFromAny(v any) string {
	if v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return fmt.Sprintf("%.0f", t)
	case bool:
		if t {
			return "true"
		}
		return "false"
	default:
		return fmt.Sprint(t)
	}
}

// boolFromAny parses JSON/tool args; if unparseable, returns defaultVal.
func boolFromAny(v any, defaultVal bool) bool {
	if v == nil {
		return defaultVal
	}
	switch t := v.(type) {
	case bool:
		return t
	case string:
		s := strings.TrimSpace(strings.ToLower(t))
		if s == "" {
			return defaultVal
		}
		return s == "true" || s == "1" || s == "yes"
	case float64:
		return t != 0
	case int:
		return t != 0
	case int64:
		return t != 0
	default:
		return defaultVal
	}
}
