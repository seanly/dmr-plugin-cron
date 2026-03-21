package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/seanly/dmr/pkg/plugin/proto"
)

func TestCallToolCronAddListRemove(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "jobs.json")
	p := NewCronPlugin()
	p.shutdownCtx = context.Background()
	p.storage = newFileStorage(path)

	var resp proto.CallToolResponse
	err := p.CallTool(&proto.CallToolRequest{
		Name:     "cron.add",
		ArgsJSON: `{"schedule":"0 * * * *","tape_name":"web","prompt":"hello","id":"fixed-id"}`,
	}, &resp)
	if err != nil || resp.Error != "" {
		t.Fatalf("add: err=%v resp.Error=%s", err, resp.Error)
	}
	var addOut map[string]any
	_ = json.Unmarshal([]byte(resp.ResultJSON), &addOut)
	if addOut["id"] != "fixed-id" {
		t.Fatalf("id: %v", addOut)
	}

	resp = proto.CallToolResponse{}
	_ = p.CallTool(&proto.CallToolRequest{Name: "cron.list", ArgsJSON: `{}`}, &resp)
	if resp.Error != "" {
		t.Fatal(resp.Error)
	}
	var listOut struct {
		Jobs []Job `json:"jobs"`
	}
	if err := json.Unmarshal([]byte(resp.ResultJSON), &listOut); err != nil || len(listOut.Jobs) != 1 {
		t.Fatalf("list: %s err=%v", resp.ResultJSON, err)
	}

	resp = proto.CallToolResponse{}
	_ = p.CallTool(&proto.CallToolRequest{Name: "cron.show", ArgsJSON: `{"id":"fixed-id"}`}, &resp)
	if resp.Error != "" {
		t.Fatal(resp.Error)
	}

	resp = proto.CallToolResponse{}
	_ = p.CallTool(&proto.CallToolRequest{Name: "cron.remove", ArgsJSON: `{"id":"fixed-id"}`}, &resp)
	if resp.Error != "" {
		t.Fatal(resp.Error)
	}

	resp = proto.CallToolResponse{}
	_ = p.CallTool(&proto.CallToolRequest{Name: "cron.list", ArgsJSON: `{}`}, &resp)
	_ = json.Unmarshal([]byte(resp.ResultJSON), &listOut)
	if len(listOut.Jobs) != 0 {
		t.Fatalf("expected empty after remove: %+v", listOut.Jobs)
	}
}

func TestCallToolCronAddRunOncePersists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "jobs.json")
	p := NewCronPlugin()
	p.shutdownCtx = context.Background()
	p.storage = newFileStorage(path)

	var resp proto.CallToolResponse
	err := p.CallTool(&proto.CallToolRequest{
		Name:     "cron.add",
		ArgsJSON: `{"schedule":"0 * * * *","tape_name":"web","prompt":"ping","id":"once-1","run_once":true}`,
	}, &resp)
	if err != nil || resp.Error != "" {
		t.Fatalf("add: err=%v resp.Error=%s", err, resp.Error)
	}

	resp = proto.CallToolResponse{}
	_ = p.CallTool(&proto.CallToolRequest{Name: "cron.show", ArgsJSON: `{"id":"once-1"}`}, &resp)
	if resp.Error != "" {
		t.Fatal(resp.Error)
	}
	var j Job
	if e := json.Unmarshal([]byte(resp.ResultJSON), &j); e != nil {
		t.Fatal(e)
	}
	if !j.RunOnce || j.ID != "once-1" {
		t.Fatalf("expected run_once job: %+v", j)
	}
}
