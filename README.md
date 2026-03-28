# dmr-plugin-cron

External DMR plugin: loads scheduled jobs from **storage only** (no inline `jobs` in the main DMR config) and calls the host **`RunAgent`** on each tick. Requires **`dmr serve`** (reverse RPC is not enabled for `dmr chat` / `dmr run`).

## Build

```bash
make build          # or: go build -o dmr-plugin-cron .
make test
make install        # copies binary to ~/.dmr/plugins/
```

Point `path` for the `cron` plugin entry in DMR **`config.toml`** at this binary (or `~/.dmr/plugins/dmr-plugin-cron` after `make install`).

## DMR `config.toml`

Use a `[[plugins]]` block; the `[plugins.config]` table attaches to the **preceding** plugin entry.

```toml
[[plugins]]
name = "cron"
enabled = true
path = "/absolute/path/to/dmr-plugin-cron"

[plugins.config]
timezone = "Asia/Shanghai"
reload_interval = "30s"   # optional: reload jobs from storage
# optional: error | info (default) | debug — controls stderr volume from this plugin
log_level = "info"

[plugins.config.storage]
driver = "file"
# Relative to the directory containing the main DMR config file
path = "data/cron_jobs.json"
```

DMR injects **`config_base_dir`** (absolute path of the config file’s directory) into the plugin JSON; do not set it manually.

**`log_level`** (optional): `error` — only failures (RPC/agent errors, load errors, timeouts); `info` (default) — also lifecycle, a **single line per scheduler reload** (`scheduler reloaded: N enabled job(s)`), and invalid-job notices; `debug` — also every `registered job …` line and successful run step counts. Hosts using go-plugin may still label plugin stderr as DEBUG; this knob mainly reduces **how many** lines are emitted.

### Session tape and `cronAdd`

**`cronAdd` does not take `tape_name` in tool arguments.** The job’s tape is always the host-provided **`SessionTape`** (the current agent session). Run `cronAdd` from the IM/web session where the scheduled `RunAgent` should execute (e.g. open the Feishu/Weixin DM with the agent, then ask it to add the cron job).

If **`SessionTape`** is empty (old host or no session), `cronAdd` errors; you can still add jobs by editing the cron storage JSON/SQLite manually.

**Host / plugin RPC:** Rebuild **all** external plugins against the same `github.com/seanly/dmr` revision as the host so `CallTool` carries `SessionTape`.

### Storage drivers

| `driver`   | Fields | Notes |
|------------|--------|--------|
| `file`     | `path` | JSON file with top-level `jobs` array |
| `sqlite`   | `path` **or** `dsn` | Default DSN built as `file:<abs>?_pragma=...` when using `path` |
| `postgres` | `dsn`  | Standard libpq connection string |

### JSON file shape (`driver: file`)

```json
{
  "jobs": [
    {
      "id": "water-8am",
      "schedule": "0 8 * * *",
      "tape_name": "feishu:p2p:YOUR_CHAT_ID",
      "prompt": "Reminder: send the user a short message to drink water using the Feishu tools.",
      "enabled": true,
      "run_once": false
    }
  ]
}
```

Set **`run_once`: `true`** for a one-shot job: after the first **successful** `RunAgent` (no RPC error and empty agent `error`), the plugin **deletes** the job from storage and reloads the scheduler. Failed runs keep the job until the next cron tick.

### SQL schema (`sqlite` / `postgres`)

Table **`cron_jobs`**:

| Column     | Type (sqlite) | Type (postgres) |
|------------|---------------|-------------------|
| `job_id`   | TEXT PK       | TEXT PK           |
| `schedule` | TEXT          | TEXT              |
| `tape_name`| TEXT          | TEXT              |
| `prompt`   | TEXT          | TEXT              |
| `enabled`  | INTEGER (0/1) | BOOLEAN           |
| `run_once` | INTEGER (0/1) | BOOLEAN (default false) |

The plugin creates the table on startup if missing and migrates older databases with `ALTER TABLE` to add `run_once` when needed.

## Tools (`ProvideTools` / `CallTool`)

When the plugin is enabled with TOML `name = "cron"`, the host registers these tools (names must match exactly):

| Tool | Description |
|------|-------------|
| `cronList` | List jobs. Optional arg `enabled_only` (bool). |
| `cronShow` | Arg `id` — return one job or error if missing. |
| `cronReload` | Reload storage and rebuild the scheduler (same as `reload_interval`). |
| `cronAdd` | Upsert a job on the **current session tape** only: args `schedule`, `prompt`; optional `id`, `enabled`, **`run_once`**. Returns `tape_name` in the result. **Reloads scheduler after write.** |
| `cronRemove` | Arg `id` — delete job; **reloads scheduler**. Returns error if id not found. |

**IM channels:** add cron jobs **in the same chat** you want runs bound to; the tool cannot target another tape.

**Concurrency:** File storage uses an internal mutex for read/write; after `cronAdd` / `cronRemove`, the plugin reloads the robfig scheduler without restarting `dmr serve`.

## OPA / approvals

- `cronAdd` and `cronRemove` persist jobs and affect future `RunAgent` runs (`cronAdd` is scoped to the current session tape) — treat them as **high risk** in production.
- Recommended: extend **`opa_policy`** rules so `cronAdd` / `cronRemove` are **`require_approval`** or **deny**, while `cronList` / `cronShow` / `cronReload` can stay **allow** (adjust to your threat model).
- In DMR, see the `opa_policy` plugin and your custom `.rego` files for how `input.tool` (e.g. `cronAdd`) is evaluated.

## Behaviour

- **Schedule**: 5-field cron (`robfig/cron` standard), evaluated in `timezone` (or host local if unset).
- **Execution**: one global mutex serializes `RunAgent` calls.
- **Shutdown**: stops cron, waits for in-flight jobs up to ~45s; cancels runs with a 30-minute RPC safeguard per job.
- **Feishu / Weixin**: run `cronAdd` from that DM’s session so `SessionTape` is the right `feishu:p2p:` / `weixin:p2p:` tape; put delivery instructions in `prompt`; enable the channel plugin on the host.
- **One-shot reminders**: set `run_once: true` on `cronAdd` (or in the JSON file) so the job is removed after one successful execution; repeating schedules without `run_once` stay until `cronRemove` or manual edit.
- **Approvals / OPA**: same as any other `RunAgent` run; unattended `require_approval` may deny.

## Relative paths

- `storage.path` and relative `file:` segments inside `storage.dsn` (sqlite) are resolved against **`config_base_dir`**.
- If `config_base_dir` is missing (old DMR), the plugin falls back to **CWD** and logs a warning.
