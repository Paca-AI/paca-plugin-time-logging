// Package main implements the com.paca.time-logging backend WASM plugin.
//
// It provides CRUD routes for manual time-log entries scoped to tasks (date,
// duration in minutes, optional note), plus a per-project summary endpoint
// that totals time by project member. It also handles the task.deleted event
// to cascade-delete orphaned data.
package main

import (
	"time"

	plugin "github.com/Paca-AI/plugin-sdk-go"
)

// nowStr returns the current UTC time as an RFC3339Nano string.
func nowStr() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

// permManageAllTimeLogs is declared twice in plugin.json — once with
// scope "project" (grantable per project role) and once with scope
// "global" (grantable instance-wide) — sharing this one key. Either grant
// lets a caller create, edit, or delete time log entries attributed to other
// project members; a global grant applies in every project because
// paca.permission_check merges the caller's global and project permissions
// for project-scoped checks. Without either, a caller may only act on their
// own entries — see (*timeLoggingPlugin).canModify.
const permManageAllTimeLogs = "time_logging.manage_all"

// timeLoggingPlugin implements plugin.Plugin.
type timeLoggingPlugin struct {
	db   *plugin.DB
	log  *plugin.Logger
	perm *plugin.Permissions
}

// Init registers all routes and event handlers on the provided context.
func (p *timeLoggingPlugin) Init(ctx *plugin.Context) error {
	p.db = ctx.DB()
	p.log = ctx.Log()
	p.perm = ctx.Permissions()

	// Event handlers
	ctx.On("task.deleted", p.handleTaskDeleted)

	// Time log CRUD (task-scoped)
	ctx.Route("GET", "/tasks/:taskId/time-logs", p.listTimeLogs)
	ctx.Route("POST", "/tasks/:taskId/time-logs", p.createTimeLog)
	ctx.Route("PATCH", "/tasks/:taskId/time-logs/:logId", p.updateTimeLog)
	ctx.Route("DELETE", "/tasks/:taskId/time-logs/:logId", p.deleteTimeLog)

	// Project-scoped list and summary
	ctx.Route("GET", "/time-logs", p.listProjectTimeLogs)
	ctx.Route("GET", "/time-logs/summary", p.timeLogsSummary)
	ctx.Route("GET", "/time-logs/me", p.viewer)

	// Global/admin-scoped cross-project summary and entries list
	ctx.Route("GET", "/time-logs/summary-all", p.timeLogsSummaryAll)
	ctx.Route("GET", "/time-logs/all", p.listAllTimeLogs)
	ctx.Route("GET", "/time-logs/users-all", p.timeLogsUsersAll)
	ctx.Route("GET", "/time-logs/viewer-all", p.viewerAll)
	ctx.Route("PATCH", "/time-logs/all/:logId", p.updateTimeLogGlobal)
	ctx.Route("DELETE", "/time-logs/all/:logId", p.deleteTimeLogGlobal)

	// Automation graph nodes (Condition/Action)
	p.registerAutomationNodes(ctx)

	return nil
}

// Shutdown is a no-op for this plugin.
func (p *timeLoggingPlugin) Shutdown() {}

// envelope wraps successful API responses to match the host's standard format.
type envelope struct {
	Success bool `json:"success"`
	Data    any  `json:"data"`
}

func ok(res *plugin.Response, data any) {
	res.JSON(200, envelope{Success: true, Data: data})
}

func created(res *plugin.Response, data any) {
	res.JSON(201, envelope{Success: true, Data: data})
}
