# dmr-plugin-cron

External DMR plugin: loads scheduled jobs from **storage only** (no inline `jobs` in YAML) and calls the host **`RunAgent`** on each tick. Requires **`dmr serve`** (reverse RPC is not enabled for `dmr chat` / `dmr run`).

## Build

```bash
make build          # or: go build -o dmr-plugin-cron .
make test
make install        # copies binary to ~/.dmr/plugins/
```

Point `plugins[].path` in DMR `config.yaml` at this binary (or `~/.dmr/plugins/dmr-plugin-cron` after `make install`).

## DMR `config.yaml`

```yaml
plugins:
  - name: cron
    enabled: true
    path: /absolute/path/to/dmr-plugin-cron
    config:
      timezone: Asia/Shanghai
      reload_interval: 30s   # optional: reload jobs from storage
      storage:
        driver: file
        # Relative to the directory containing the main DMR config file
        path: data/cron_jobs.json
```

DMR injects **`config_base_dir`** (absolute path of the config file’s directory) into the plugin JSON; do not set it manually.

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

When the plugin is enabled with YAML `name: cron`, the host registers these tools (names must match exactly):

| Tool | Description |
|------|-------------|
| `cronList` | List jobs. Optional arg `enabled_only` (bool). |
| `cronShow` | Arg `id` — return one job or error if missing. |
| `cronReload` | Reload storage and rebuild the scheduler (same as `reload_interval`). |
| `cronAdd` | Upsert a job: args `schedule`, `tape_name`, `prompt`; optional `id` (UUID generated if omitted), `enabled` (default true), **`run_once`** (default false — if true, job is deleted after first successful run). **Reloads scheduler after write.** |
| `cronRemove` | Arg `id` — delete job; **reloads scheduler**. Returns error if id not found. |

**Feishu / external channels:** `CallTool` does **not** receive the current tape. The model must pass **`tape_name`** explicitly (e.g. `feishu:p2p:<chat_id>` for the active DM). Put that in your Feishu system prompt when you want natural-language reminders.

**Concurrency:** File storage uses an internal mutex for read/write; after `cronAdd` / `cronRemove`, the plugin reloads the robfig scheduler without restarting `dmr serve`.

## OPA / approvals

- `cronAdd` and `cronRemove` persist jobs and affect future `RunAgent` runs on arbitrary tapes — treat them as **high risk** in production.
- Recommended: extend **`opa_policy`** rules so `cronAdd` / `cronRemove` are **`require_approval`** or **deny**, while `cronList` / `cronShow` / `cronReload` can stay **allow** (adjust to your threat model).
- In DMR, see the `opa_policy` plugin and your custom `.rego` files for how `input.tool` (e.g. `cronAdd`) is evaluated.

## Behaviour

- **Schedule**: 5-field cron (`robfig/cron` standard), evaluated in `timezone` (or host local if unset).
- **Execution**: one global mutex serializes `RunAgent` calls.
- **Shutdown**: stops cron, waits for in-flight jobs up to ~45s; cancels runs with a 30-minute RPC safeguard per job.
- **Feishu**: use `tape_name: feishu:p2p:<chat_id>` and put tool instructions in `prompt`; enable the Feishu plugin on the host.
- **One-shot reminders**: set `run_once: true` on `cronAdd` (or in the JSON file) so the job is removed after one successful execution; repeating schedules without `run_once` stay until `cronRemove` or manual edit.
- **Approvals / OPA**: same as any other `RunAgent` run; unattended `require_approval` may deny.

## Relative paths

- `storage.path` and relative `file:` segments inside `storage.dsn` (sqlite) are resolved against **`config_base_dir`**.
- If `config_base_dir` is missing (old DMR), the plugin falls back to **CWD** and logs a warning.
