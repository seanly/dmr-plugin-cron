package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/rpc"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/seanly/dmr/pkg/plugin/proto"
)

// CronPlugin implements proto.DMRPluginInterface and HostClientSetter.
type CronPlugin struct {
	mu       sync.Mutex
	startOnce sync.Once

	cfg cronPluginConfig
	hostClient  *rpc.Client
	storage     Storage
	cron        *cron.Cron
	reloadStop  chan struct{}
	runMu       sync.Mutex // serial RunAgent
	shutdownCtx context.Context
	cancelRun   context.CancelFunc
}

type cronPluginConfig struct {
	Timezone       string `json:"timezone"`
	ConfigBaseDir  string `json:"config_base_dir"`
	ReloadInterval string `json:"reload_interval"`
	Storage        struct {
		Driver string `json:"driver"`
		Path   string `json:"path"`
		DSN    string `json:"dsn"`
	} `json:"storage"`
}

// NewCronPlugin creates a CronPlugin with defaults.
func NewCronPlugin() *CronPlugin {
	return &CronPlugin{}
}

func (p *CronPlugin) Init(req *proto.InitRequest, resp *proto.InitResponse) error {
	raw := req.ConfigJSON
	if raw == "" || raw == "null" {
		return fmt.Errorf("cron plugin: config is required")
	}
	if err := json.Unmarshal([]byte(raw), &p.cfg); err != nil {
		return fmt.Errorf("parse cron config: %w", err)
	}
	if p.cfg.Storage.Driver == "" {
		return fmt.Errorf("cron plugin: storage.driver is required")
	}
	if tz := p.cfg.Timezone; tz != "" {
		if _, err := time.LoadLocation(tz); err != nil {
			return fmt.Errorf("timezone: %w", err)
		}
	}

	st, err := openStorage(p.cfg.Storage.Driver, p.cfg.Storage.Path, p.cfg.Storage.DSN, p.cfg.ConfigBaseDir)
	if err != nil {
		return err
	}
	if p.storage != nil {
		_ = p.storage.Close()
	}
	p.storage = st

	p.shutdownCtx, p.cancelRun = context.WithCancel(context.Background())
	log.Printf("dmr-plugin-cron: init driver=%s config_base_dir=%q", p.cfg.Storage.Driver, p.cfg.ConfigBaseDir)

	// go-plugin calls InitHost (SetHostClient) before Plugin.Init on the child; do not start
	// the scheduler until both storage and hostClient are set.
	p.tryStartScheduler()
	return nil
}

// SetHostClient is invoked when reverse RPC to the host is ready (often before Init returns).
func (p *CronPlugin) SetHostClient(client any) {
	rpcClient, ok := client.(*rpc.Client)
	if !ok {
		log.Printf("dmr-plugin-cron: SetHostClient unexpected type %T", client)
		return
	}
	p.mu.Lock()
	p.hostClient = rpcClient
	p.mu.Unlock()

	log.Println("dmr-plugin-cron: host RPC client attached")
	p.tryStartScheduler()
}

// tryStartScheduler starts runSchedulerLoop exactly once when storage and host are both ready.
func (p *CronPlugin) tryStartScheduler() {
	p.mu.Lock()
	ready := p.storage != nil && p.hostClient != nil
	p.mu.Unlock()
	if !ready {
		return
	}
	p.startOnce.Do(func() {
		log.Println("dmr-plugin-cron: storage and host ready, starting scheduler")
		go p.runSchedulerLoop()
	})
}

func (p *CronPlugin) runSchedulerLoop() {
	loc := p.effectiveLocation()

	reloadDur := time.Duration(0)
	if s := p.cfg.ReloadInterval; s != "" {
		d, err := time.ParseDuration(s)
		if err != nil {
			log.Printf("dmr-plugin-cron: invalid reload_interval %q: %v", s, err)
		} else {
			reloadDur = d
		}
	}

	p.applyJobs(loc)

	if reloadDur > 0 {
		p.reloadStop = make(chan struct{})
		go func() {
			t := time.NewTicker(reloadDur)
			defer t.Stop()
			for {
				select {
				case <-t.C:
					p.applyJobs(loc)
				case <-p.reloadStop:
					return
				}
			}
		}()
	}
}

func (p *CronPlugin) effectiveLocation() *time.Location {
	loc := time.Local
	if tz := p.cfg.Timezone; tz != "" {
		l, err := time.LoadLocation(tz)
		if err != nil {
			log.Printf("dmr-plugin-cron: timezone error: %v, using Local", err)
		} else {
			loc = l
		}
	}
	return loc
}

// reloadFromStorage reloads jobs from storage and rebuilds the robfig scheduler.
func (p *CronPlugin) reloadFromStorage() {
	p.applyJobs(p.effectiveLocation())
}

func (p *CronPlugin) applyJobs(loc *time.Location) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.storage == nil {
		log.Printf("dmr-plugin-cron: applyJobs skipped: storage not initialized")
		return
	}

	if p.cron != nil {
		ctx := p.cron.Stop()
		<-ctx.Done()
		p.cron = nil
	}

	jobs, err := p.storage.LoadJobs(context.Background())
	if err != nil {
		log.Printf("dmr-plugin-cron: LoadJobs: %v", err)
		return
	}

	c := cron.New(cron.WithLocation(loc))
	for _, j := range jobs {
		if !j.Enabled {
			continue
		}
		if j.ID == "" || j.Schedule == "" || j.TapeName == "" {
			log.Printf("dmr-plugin-cron: skip invalid job (missing id/schedule/tape_name): %+v", j)
			continue
		}
		job := j
		_, err := c.AddFunc(j.Schedule, func() { p.runJob(job) })
		if err != nil {
			log.Printf("dmr-plugin-cron: job %q invalid schedule %q: %v", j.ID, j.Schedule, err)
			continue
		}
		if job.RunOnce {
			log.Printf("dmr-plugin-cron: registered job %q schedule=%q tape=%q run_once=true", j.ID, j.Schedule, j.TapeName)
		} else {
			log.Printf("dmr-plugin-cron: registered job %q schedule=%q tape=%q", j.ID, j.Schedule, j.TapeName)
		}
	}
	c.Start()
	p.cron = c
}

func (p *CronPlugin) runJob(j Job) {
	p.runMu.Lock()
	defer p.runMu.Unlock()

	p.mu.Lock()
	hc := p.hostClient
	ctx := p.shutdownCtx
	p.mu.Unlock()

	if hc == nil {
		log.Printf("dmr-plugin-cron: job %q skipped: host not connected", j.ID)
		return
	}
	if ctx.Err() != nil {
		return
	}

	req := &proto.RunAgentRequest{
		TapeName:            j.TapeName,
		Prompt:              j.Prompt,
		HistoryAfterEntryID: 0,
	}
	var resp proto.RunAgentResponse
	done := make(chan error, 1)
	go func() {
		done <- hc.Call("Plugin.RunAgent", req, &resp)
	}()

	var success bool
	select {
	case err := <-done:
		if err != nil {
			log.Printf("dmr-plugin-cron: job %q RunAgent RPC error: %v", j.ID, err)
			return
		}
		if resp.Error != "" {
			log.Printf("dmr-plugin-cron: job %q agent error: %s", j.ID, resp.Error)
		} else {
			success = true
			log.Printf("dmr-plugin-cron: job %q completed steps=%d", j.ID, resp.Steps)
		}
	case <-ctx.Done():
		log.Printf("dmr-plugin-cron: job %q cancelled during shutdown", j.ID)
	case <-time.After(30 * time.Minute):
		log.Printf("dmr-plugin-cron: job %q RunAgent timeout (30m)", j.ID)
	}

	if success && j.RunOnce {
		id := j.ID
		go p.cleanupRunOnceJob(id)
	}
}

// cleanupRunOnceJob deletes a one-shot job from storage and reloads the scheduler.
// It runs asynchronously so runJob can return before applyJobs stops the robfig cron (avoids deadlock).
func (p *CronPlugin) cleanupRunOnceJob(id string) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	p.mu.Lock()
	st := p.storage
	p.mu.Unlock()
	if st == nil {
		return
	}
	if err := st.DeleteJob(ctx, id); err != nil {
		log.Printf("dmr-plugin-cron: run_once job %q delete failed: %v", id, err)
		return
	}
	log.Printf("dmr-plugin-cron: run_once job %q removed after successful run", id)
	p.reloadFromStorage()
}

func (p *CronPlugin) Shutdown(req *proto.ShutdownRequest, resp *proto.ShutdownResponse) error {
	if p.cancelRun != nil {
		p.cancelRun()
	}
	if p.reloadStop != nil {
		close(p.reloadStop)
		p.reloadStop = nil
	}
	p.mu.Lock()
	if p.cron != nil {
		ctx := p.cron.Stop()
		p.cron = nil
		p.mu.Unlock()
		select {
		case <-ctx.Done():
		case <-time.After(45 * time.Second):
			log.Println("dmr-plugin-cron: cron.Stop timed out")
		}
	} else {
		p.mu.Unlock()
	}
	if p.storage != nil {
		_ = p.storage.Close()
		p.storage = nil
	}
	return nil
}

func (p *CronPlugin) RequestApproval(req *proto.ApprovalRequest, resp *proto.ApprovalResult) error {
	resp.Choice = 0
	resp.Comment = "cron plugin does not handle approvals"
	return nil
}

func (p *CronPlugin) RequestBatchApproval(req *proto.BatchApprovalRequest, resp *proto.BatchApprovalResult) error {
	resp.Choice = 0
	return nil
}

var _ proto.HostClientSetter = (*CronPlugin)(nil)
