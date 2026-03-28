package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/rpc"
	"strings"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/seanly/dmr/pkg/plugin/proto"
)

// cronLogLevel controls stderr volume from this plugin (see log_level in config).
type cronLogLevel int

const (
	cronLogError cronLogLevel = iota // errors and serious failures only
	cronLogInfo                      // + lifecycle, reload summary, validation notices
	cronLogDebug                     // + per-job registration, run success detail
)

func parseCronLogLevel(s string) cronLogLevel {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "error":
		return cronLogError
	case "debug":
		return cronLogDebug
	case "info", "":
		return cronLogInfo
	default:
		return cronLogInfo
	}
}

// CronPlugin implements proto.DMRPluginInterface and HostClientSetter.
type CronPlugin struct {
	mu       sync.Mutex
	startOnce sync.Once

	logLevel cronLogLevel
	cfg cronPluginConfig
	hostClient  *rpc.Client
	storage     Storage
	cron        *cron.Cron
	reloadStop  chan struct{}
	runMu       sync.Mutex // serial RunAgent
	// immediateRun prevents duplicate goroutines for the same job id while an immediate run is in flight.
	immediateRun sync.Map // string (job ID) -> struct{}
	// asyncReloadWg tracks background applyJobs from tools / cleanup so Shutdown can wait.
	asyncReloadWg sync.WaitGroup
	shutdownCtx context.Context
	cancelRun   context.CancelFunc
}

type cronPluginConfig struct {
	Timezone       string `json:"timezone"`
	ConfigBaseDir  string `json:"config_base_dir"`
	ReloadInterval string `json:"reload_interval"`
	LogLevel       string `json:"log_level"`
	Storage        struct {
		Driver string `json:"driver"`
		Path   string `json:"path"`
		DSN    string `json:"dsn"`
	} `json:"storage"`
}

// NewCronPlugin creates a CronPlugin with defaults.
func NewCronPlugin() *CronPlugin {
	// Default info so logs before Init (e.g. SetHostClient) match post-Init default log_level.
	return &CronPlugin{logLevel: cronLogInfo}
}

func (p *CronPlugin) logErrorf(format string, args ...any) {
	log.Printf("dmr-plugin-cron: "+format, args...)
}

func (p *CronPlugin) logInfof(format string, args ...any) {
	if p.logLevel >= cronLogInfo {
		log.Printf("dmr-plugin-cron: "+format, args...)
	}
}

func (p *CronPlugin) logDebugf(format string, args ...any) {
	if p.logLevel >= cronLogDebug {
		log.Printf("dmr-plugin-cron: "+format, args...)
	}
}

func (p *CronPlugin) Init(req *proto.InitRequest, resp *proto.InitResponse) error {
	raw := req.ConfigJSON
	if raw == "" || raw == "null" {
		return fmt.Errorf("cron plugin: config is required")
	}
	if err := json.Unmarshal([]byte(raw), &p.cfg); err != nil {
		return fmt.Errorf("parse cron config: %w", err)
	}
	p.logLevel = parseCronLogLevel(p.cfg.LogLevel)
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
	p.logInfof("init driver=%s config_base_dir=%q", p.cfg.Storage.Driver, p.cfg.ConfigBaseDir)

	// go-plugin calls InitHost (SetHostClient) before Plugin.Init on the child; do not start
	// the scheduler until both storage and hostClient are set.
	p.tryStartScheduler()
	return nil
}

// SetHostClient is invoked when reverse RPC to the host is ready (often before Init returns).
func (p *CronPlugin) SetHostClient(client any) {
	rpcClient, ok := client.(*rpc.Client)
	if !ok {
		p.logErrorf("SetHostClient unexpected type %T", client)
		return
	}
	p.mu.Lock()
	p.hostClient = rpcClient
	p.mu.Unlock()

	p.logInfof("host RPC client attached")
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
		p.logInfof("storage and host ready, starting scheduler")
		go p.runSchedulerLoop()
	})
}

func (p *CronPlugin) runSchedulerLoop() {
	loc := p.effectiveLocation()

	reloadDur := time.Duration(0)
	if s := p.cfg.ReloadInterval; s != "" {
		d, err := time.ParseDuration(s)
		if err != nil {
			p.logInfof("invalid reload_interval %q: %v", s, err)
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
			p.logInfof("timezone error: %v, using Local", err)
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

// scheduleReloadFromStorage runs reloadFromStorage in a goroutine so tool CallTool handlers
// return before applyJobs calls cron.Stop(). Otherwise Stop waits for in-flight robfig
// callbacks (runJob), and runJob is blocked on Plugin.RunAgent while the host waits on
// CallTool — a nested RPC deadlock.
func (p *CronPlugin) scheduleReloadFromStorage() {
	p.asyncReloadWg.Add(1)
	go func() {
		defer p.asyncReloadWg.Done()
		p.reloadFromStorage()
	}()
}

func (p *CronPlugin) applyJobs(loc *time.Location) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.storage == nil {
		p.logErrorf("applyJobs skipped: storage not initialized")
		return
	}

	if p.cron != nil {
		ctx := p.cron.Stop()
		<-ctx.Done()
		p.cron = nil
	}

	jobs, err := p.storage.LoadJobs(context.Background())
	if err != nil {
		p.logErrorf("LoadJobs: %v", err)
		return
	}

	c := cron.New(cron.WithLocation(loc))
	registered := 0
	for _, j := range jobs {
		if !j.Enabled {
			continue
		}
		if j.ID == "" || j.Schedule == "" || j.TapeName == "" {
			p.logInfof("skip invalid job (missing id/schedule/tape_name): %+v", j)
			continue
		}
		job := j
		if isImmediateSchedule(j.Schedule) {
			p.queueImmediateRun(job)
			registered++
			p.logDebugf("queued immediate job %q tape=%q", j.ID, j.TapeName)
			continue
		}
		_, err := c.AddFunc(j.Schedule, func() { p.runJob(job) })
		if err != nil {
			p.logInfof("job %q invalid schedule %q: %v", j.ID, j.Schedule, err)
			continue
		}
		registered++
		if job.RunOnce {
			p.logDebugf("registered job %q schedule=%q tape=%q run_once=true", j.ID, j.Schedule, j.TapeName)
		} else {
			p.logDebugf("registered job %q schedule=%q tape=%q", j.ID, j.Schedule, j.TapeName)
		}
	}
	c.Start()
	p.cron = c
	p.logInfof("scheduler reloaded: %d enabled job(s)", registered)
}

// queueImmediateRun starts runJob once per job id until it finishes; applyJobs reloads do not stack duplicates.
func (p *CronPlugin) queueImmediateRun(j Job) {
	if _, loaded := p.immediateRun.LoadOrStore(j.ID, struct{}{}); loaded {
		p.logDebugf("immediate job %q skipped (already queued or running)", j.ID)
		return
	}
	job := j
	go func() {
		defer p.immediateRun.Delete(job.ID)
		p.runJob(job)
	}()
}

// cronPrefixedPrompt prepends the job tape_name before the stored prompt so the model always sees which
// DMR tape this scheduled run uses. Channel-specific delivery rules live in each plugin (e.g. Feishu/Weixin inbound hints).
func cronPrefixedPrompt(tapeName, prompt string) string {
	const stub = `【DMR·定时】本轮 RunAgent 的 tape_name=%q。

[Cron] tape_name=%q.

---

%s`
	return fmt.Sprintf(stub, tapeName, tapeName, prompt)
}

func (p *CronPlugin) runJob(j Job) {
	p.runMu.Lock()
	defer p.runMu.Unlock()

	p.mu.Lock()
	hc := p.hostClient
	ctx := p.shutdownCtx
	p.mu.Unlock()

	if hc == nil {
		p.logInfof("job %q skipped: host not connected", j.ID)
		return
	}
	if ctx.Err() != nil {
		return
	}

	// Create handoff anchor to isolate cron execution from user conversation history
	p.logInfof("job %q starting execution on tape %q", j.ID, j.TapeName)
	handoffReq := &proto.TapeHandoffRequest{
		TapeName:  j.TapeName,
		Name:      fmt.Sprintf("cron:%s", j.ID),
		StateJSON: fmt.Sprintf(`{"type":"cron","job_id":%q,"schedule":%q,"timestamp":%q}`, j.ID, j.Schedule, time.Now().Format(time.RFC3339)),
	}
	var handoffResp proto.TapeHandoffResponse
	handoffDone := make(chan error, 1)
	go func() {
		handoffDone <- hc.Call("Plugin.TapeHandoff", handoffReq, &handoffResp)
	}()

	var anchorEntryID int32
	select {
	case err := <-handoffDone:
		if err != nil {
			p.logErrorf("job %q TapeHandoff RPC error: %v", j.ID, err)
			return
		}
		anchorEntryID = int32(handoffResp.AnchorEntryID)
		p.logInfof("job %q created handoff anchor at entry %d", j.ID, anchorEntryID)
	case <-ctx.Done():
		p.logInfof("job %q cancelled during handoff", j.ID)
		return
	case <-time.After(10 * time.Second):
		p.logErrorf("job %q TapeHandoff timeout (10s)", j.ID)
		return
	}

	req := &proto.RunAgentRequest{
		TapeName:            j.TapeName,
		Prompt:              cronPrefixedPrompt(j.TapeName, j.Prompt),
		HistoryAfterEntryID: anchorEntryID,
	}
	p.logDebugf("job %q calling RunAgent with history after entry %d", j.ID, anchorEntryID)
	var resp proto.RunAgentResponse
	done := make(chan error, 1)
	go func() {
		done <- hc.Call("Plugin.RunAgent", req, &resp)
	}()

	var success bool
	select {
	case err := <-done:
		if err != nil {
			p.logErrorf("job %q RunAgent RPC error: %v", j.ID, err)
			return
		}
		if resp.Error != "" {
			p.logErrorf("job %q agent error: %s", j.ID, resp.Error)
		} else {
			success = true
			p.logInfof("job %q completed successfully (steps=%d, prompt_tokens=%d, completion_tokens=%d)", j.ID, resp.Steps, resp.PromptTokens, resp.CompletionTokens)
		}
	case <-ctx.Done():
		p.logInfof("job %q cancelled during shutdown", j.ID)
	case <-time.After(30 * time.Minute):
		p.logErrorf("job %q RunAgent timeout (30m)", j.ID)
	}

	if success && (j.RunOnce || isImmediateSchedule(j.Schedule)) {
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
		p.logErrorf("run_once job %q delete failed: %v", id, err)
		return
	}
	p.logDebugf("run_once job %q removed after successful run", id)
	p.scheduleReloadFromStorage()
}

func (p *CronPlugin) Shutdown(req *proto.ShutdownRequest, resp *proto.ShutdownResponse) error {
	if p.cancelRun != nil {
		p.cancelRun()
	}
	if p.reloadStop != nil {
		close(p.reloadStop)
		p.reloadStop = nil
	}
	done := make(chan struct{})
	go func() {
		p.asyncReloadWg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Minute):
		p.logErrorf("shutdown: timed out waiting for async scheduler reload(s)")
	}
	p.mu.Lock()
	if p.cron != nil {
		ctx := p.cron.Stop()
		p.cron = nil
		p.mu.Unlock()
		select {
		case <-ctx.Done():
		case <-time.After(45 * time.Second):
			p.logErrorf("cron.Stop timed out")
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
