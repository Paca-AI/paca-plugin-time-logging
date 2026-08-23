# com.paca.time-logging

First-party Paca plugin that lets project members log time spent on tasks.
Each time-log entry is a manual record with a date, a duration (in minutes),
and an optional note. Entries are attributed to a project member, and the
plugin exposes a per-project summary of total time logged by member.

---

## Architecture

The plugin follows the standard three-part plugin structure:

```
time-logging/
├── backend/   — Go WASM plugin (runs inside the API host)
├── frontend/  — React micro-frontend (Module Federation remote)
└── mcp/       — MCP tool server (optional, for AI agent access)
```

### Backend (`backend/`)

- Written in Go, compiled to `wasip1/wasm` for production.
- Registered as `com.paca.time-logging` in the plugin registry.
- Owns its database schema (`plugin_data_com_paca_time_logging`) and runs its
  own migration on startup.
- Listens on the `task.deleted` event to clean up any orphaned time logs
  (the `task_time_logs.task_id` foreign key also cascades on delete).

### Frontend (`frontend/`)

- Vite + React + TanStack Query.
- Exposed as a Module Federation remote (`com_paca_time_logging`) with three
  entry points:
  - `./TimeLogsSection` — rendered via the `task.detail.section` extension
    point. Lets a member add/view/delete time-log entries for the current
    task. The "log time" form and delete button both require
    `viewer.can_write_tasks` (`tasks.write`), mirroring the routes' own
    gate; delete is further limited by `canManageTimeLog` to the caller's
    own entries, or any entry with `time_logging.manage_all`.
  - `./ProjectTimeTrackingPage` — rendered via the `project.page` extension
    point at its own routed page (`/projects/:projectId/plugins/com.paca.time-logging/time-tracking`),
    reached through a nav item this plugin registers in the project sidebar.
    Gated by the project-scoped `time_logging.view_all` custom permission —
    the nav item is hidden for members without it. Lists every time-log
    entry across every task in the project (date, member, task, note,
    duration), filterable by member and date range, with inline edit/delete.
  - `./AdminTimeTrackingPage` — rendered via the `admin.page` extension point
    at its own routed page (`/admin/plugins/com.paca.time-logging/time-tracking`),
    reached through a nav item this plugin registers in the admin sidebar.
    Cross-project view: every time-log entry across every project in the
    instance, filterable by project, user, and date range. Gated by the
    plugin's own `time_logging.view_all` global custom permission, and
    deliberately read-only (see the doc comment on `AdminTimeTrackingPageInner`
    for why editing doesn't belong here).
- Communicates with the backend through the standard plugin API path:  
  `GET|POST|PATCH|DELETE /api/v1/plugins/com.paca.time-logging/projects/:projectId/tasks/:taskId/time-logs/…`
  (project-scoped routes) and `GET /api/v1/plugins/com.paca.time-logging/time-logs/…`
  (global/admin-scoped routes, no `:projectId`).

### Authorization

Logging, editing, and deleting time-log entries all require the built-in
`tasks.write` permission — tracking time against a task is treated as a
task-editing action, same as the task itself. On top of that, a caller may
only edit or delete their **own** entries by default (`PATCH`/`DELETE` on
`/tasks/:taskId/time-logs/:logId` return `403` otherwise).
`GET /time-logs/me` (`viewerInfo`) returns both `can_write_tasks` and
`can_manage_all` so every list view (task detail, project-wide) can decide
whether to offer the "log time" form and edit/delete controls consistently,
via the shared `canManageTimeLog` helper — but the backend re-checks both on
every write, so it remains the actual authorization boundary.

The plugin declares four custom permissions (`customPermissions` in
`plugin.json`) — note that both `time_logging.manage_all` and
`time_logging.view_all` are declared **twice**, once per scope, since the
same key name reads naturally in both role editors and the two scopes never
share a permission map:

- `time_logging.manage_all` (scope `project`) — a project owner can grant
  this to a project role to let its holders edit/delete any project
  member's entries within that project, in addition to their own.
- `time_logging.manage_all` (scope `global`) — grants the same
  edit/delete-any-entry capability, but instance-wide across every project,
  not just one. It appears in the *global* role editor. Both variants are
  checked identically via the SDK's `ctx.Permissions().Check(...)`, backed
  by the host's `paca.permission_check` function, which merges a caller's
  global and project permissions for project-scoped checks — so a global
  grant satisfies the check in every project without needing a matching
  per-project grant.
- `time_logging.view_all` (scope `project`) — required to reach the
  project's "Time Tracking" nav item/page and its `/time-logs` and
  `/time-logs/summary` endpoints (the project-wide, every-member view — not
  `/time-logs/me`, which stays open to any project member). It appears in
  the *project* role editor and is enforced entirely by the host's
  `requirePermissions` route middleware — no additional in-handler check.
  **Migration note:** before this permission existed, any project member
  with baseline `projects.read` could see this view; existing project roles
  will need it granted explicitly to keep that access after upgrading.
- `time_logging.view_all` (scope `global`) — required to reach the
  admin-sidebar "Time Tracking" page and its `/time-logs/summary-all`,
  `/time-logs/all`, and `/time-logs/users-all` endpoints. It appears in the
  *global* role editor and is enforced entirely by the host's
  `requirePermissions` route middleware (declared per-route in
  `plugin.json`) — there is no additional in-handler check for it, since
  those routes are otherwise fully public within the instance once granted.

### MCP (`mcp/`)

- Exposes `time_logging_list_time_logs`, `time_logging_log_time`,
  `time_logging_update_time_log`, `time_logging_delete_time_log`,
  `time_logging_project_summary`, and `time_logging_list_project_time_logs`
  tools so AI agents can log and query time on behalf of a user.

---

## API Endpoints

### Project-scoped

Prefixed with `/projects/:projectId` by the host. The caller must be an
authenticated member of the project.

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/tasks/:taskId/time-logs` | List all time-log entries for a task (requires `tasks.read`) |
| `POST` | `/tasks/:taskId/time-logs` | Log time against a task (requires `tasks.write`) |
| `PATCH` | `/tasks/:taskId/time-logs/:logId` | Update a time-log entry (requires `tasks.write`, plus own entries or any entry with `time_logging.manage_all`) |
| `DELETE` | `/tasks/:taskId/time-logs/:logId` | Delete a time-log entry (same rule as `PATCH`) |
| `GET` | `/time-logs` | List every time-log entry across all tasks in the project (requires `time_logging.view_all`) |
| `GET` | `/time-logs/summary` | Total minutes logged per project member (requires `time_logging.view_all`) |
| `GET` | `/time-logs/me` | The caller's `member_id` and whether they hold `time_logging.manage_all` |

### Global/admin-scoped

No `:projectId` — gated by the plugin's own `time_logging.view_all` global
custom permission.

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/time-logs/summary-all` | Total minutes logged per project, across every project |
| `GET` | `/time-logs/all` | Every time-log entry across every project, with project/member display names |
| `GET` | `/time-logs/users-all` | Total minutes logged per user, across every project |

### Request / Response examples

**Log time** — `POST /tasks/:taskId/time-logs`
```json
{ "spent_date": "2026-07-13", "minutes_spent": 90, "note": "Worked on the API" }
```
```json
{
  "data": {
    "id": "a1b2c3d4-…",
    "task_id": "…",
    "member_id": "…",
    "spent_date": "2026-07-13",
    "minutes_spent": 90,
    "note": "Worked on the API",
    "created_at": "2026-07-13T10:00:00Z",
    "updated_at": "2026-07-13T10:00:00Z"
  }
}
```

**Update a time-log entry** — `PATCH /tasks/:taskId/time-logs/:logId`
```json
{ "minutes_spent": 120 }
```

**Project summary** — `GET /time-logs/summary`
```json
{
  "data": [
    { "member_id": "member-1", "minutes_spent": 480 },
    { "member_id": "member-2", "minutes_spent": 120 }
  ]
}
```

---

## Database Schema

Tables live in the `plugin_data_com_paca_time_logging` schema and are created
by `backend/migrations/0001_create_task_time_logs.sql`.

```
task_time_logs
  id             UUID PK
  task_id        UUID → public.tasks(id) ON DELETE CASCADE
  member_id      UUID → public.project_members(id)
  spent_date     DATE
  minutes_spent  INTEGER  (> 0)
  note           TEXT
  created_by     UUID → public.project_members(id)
  created_at     TIMESTAMPTZ
  updated_at     TIMESTAMPTZ
```

---

## Development

### Backend

```bash
cd backend

# Run tests
go test -v ./...

# Lint
golangci-lint run --timeout=5m

# Build WASM binary (requires TinyGo — see https://tinygo.org/getting-started/install/)
tinygo build -target=wasip1 -buildmode=c-shared -o time-logging.wasm .
```

### Frontend

```bash
cd frontend

# Install dependencies
bun install

# Typecheck
bun run typecheck

# Development build (watch)
bun run dev

# Production build (outputs remoteEntry.js)
bun run build
```

The frontend uses the `@paca-ai/plugin-sdk-react` package.  
Shared singletons (`react`, `react-dom`, `@tanstack/react-query`) are provided
by the host shell and must not be bundled.

### MCP

```bash
cd mcp

bun install
bun run typecheck
bun run build
```

---

## Extension Points

### `task.detail.section` — `TimeLogsSection`

| Prop | Type | Description |
|------|------|-------------|
| `projectId` | `string` | Current project ID |
| `taskId` | `string` | Current task ID |
| `canEdit` | `boolean` | Whether the caller has write permission |

### `project.page` — `ProjectTimeTrackingPage`

Routed at `/projects/:projectId/plugins/com.paca.time-logging/time-tracking`
via the `project` nav item declared in `plugin.json`.

| Prop | Type | Description |
|------|------|-------------|
| `projectId` | `string` | Current project ID |

### `admin.page` — `AdminTimeTrackingPage`

Routed at `/admin/plugins/com.paca.time-logging/time-tracking` via the
`admin` nav item declared in `plugin.json`. Takes no props beyond the shared
`BaseExtensionProps` (`api`, `ui`, `meta`) — there is no `projectId` in this
scope, so `api.listTasks()`/`listMembers()`/etc. must not be called here.
