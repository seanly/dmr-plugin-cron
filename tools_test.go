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
		Name:        "cronAdd",
		ArgsJSON:    `{"schedule":"0 * * * *","prompt":"hello","id":"fixed-id"}`,
		SessionTape: "web",
	}, &resp)
	if err != nil || resp.Error != "" {
		t.Fatalf("add: err=%v resp.Error=%s", err, resp.Error)
	}
	var addOut map[string]any
	_ = json.Unmarshal([]byte(resp.ResultJSON), &addOut)
	if addOut["id"] != "fixed-id" {
		t.Fatalf("id: %v", addOut)
	}
	if addOut["tape_name"] != "web" {
		t.Fatalf("tape_name: %v", addOut)
	}

	resp = proto.CallToolResponse{}
	_ = p.CallTool(&proto.CallToolRequest{Name: "cronList", ArgsJSON: `{}`}, &resp)
	if resp.Error != "" {
		t.Fatal(resp.Error)
	}
	var listOut struct {
		Jobs []Job `json:"jobs"`
	}
	if err := json.Unmarshal([]byte(resp.ResultJSON), &listOut); err != nil || len(listOut.Jobs) != 1 {
		t.Fatalf("list: %s err=%v", resp.ResultJSON, err)
	}
	if listOut.Jobs[0].TapeName != "web" {
		t.Fatalf("stored tape: %+v", listOut.Jobs[0])
	}

	resp = proto.CallToolResponse{}
	_ = p.CallTool(&proto.CallToolRequest{Name: "cronShow", ArgsJSON: `{"id":"fixed-id"}`}, &resp)
	if resp.Error != "" {
		t.Fatal(resp.Error)
	}

	resp = proto.CallToolResponse{}
	_ = p.CallTool(&proto.CallToolRequest{Name: "cronRemove", ArgsJSON: `{"id":"fixed-id"}`}, &resp)
	if resp.Error != "" {
		t.Fatal(resp.Error)
	}

	resp = proto.CallToolResponse{}
	_ = p.CallTool(&proto.CallToolRequest{Name: "cronList", ArgsJSON: `{}`}, &resp)
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
		Name:        "cronAdd",
		ArgsJSON:    `{"schedule":"0 * * * *","prompt":"ping","id":"once-1","run_once":true}`,
		SessionTape: "web",
	}, &resp)
	if err != nil || resp.Error != "" {
		t.Fatalf("add: err=%v resp.Error=%s", err, resp.Error)
	}

	resp = proto.CallToolResponse{}
	_ = p.CallTool(&proto.CallToolRequest{Name: "cronShow", ArgsJSON: `{"id":"once-1"}`}, &resp)
	if resp.Error != "" {
		t.Fatal(resp.Error)
	}
	var j Job
	if e := json.Unmarshal([]byte(resp.ResultJSON), &j); e != nil {
		t.Fatal(e)
	}
	if !j.RunOnce || j.ID != "once-1" || j.TapeName != "web" {
		t.Fatalf("expected run_once job: %+v", j)
	}
}

func TestCallToolCronAddRequiresSessionTape(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "jobs.json")
	p := NewCronPlugin()
	p.shutdownCtx = context.Background()
	p.storage = newFileStorage(path)

	var resp proto.CallToolResponse
	err := p.CallTool(&proto.CallToolRequest{
		Name:        "cronAdd",
		ArgsJSON:    `{"schedule":"0 * * * *","prompt":"hello"}`,
		SessionTape: "",
	}, &resp)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error == "" {
		t.Fatal("expected error when SessionTape empty")
	}

	resp = proto.CallToolResponse{}
	_ = p.CallTool(&proto.CallToolRequest{
		Name:        "cronAdd",
		ArgsJSON:    `{"schedule":"0 * * * *","prompt":"hello","id":"ok1"}`,
		SessionTape: "session-a",
	}, &resp)
	if resp.Error != "" {
		t.Fatal(resp.Error)
	}
}
