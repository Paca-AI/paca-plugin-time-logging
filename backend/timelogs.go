package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	plugin "github.com/Paca-AI/plugin-sdk-go"
)

// ── Domain types ──────────────────────────────────────────────────────────────

type timeLog struct {
	ID           string `json:"id"`
	TaskID       string `json:"task_id"`
	MemberID     string `json:"member_id"`
	SpentDate    string `json:"spent_date"`
	MinutesSpent int    `json:"minutes_spent"`
	Note         string `json:"note"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

type memberTotal struct {
	MemberID     string `json:"member_id"`
	MinutesSpent int    `json:"minutes_spent"`
}

type projectTotal struct {
	ProjectID    string `json:"project_id"`
	ProjectName  string `json:"project_name"`
	MinutesSpent int    `json:"minutes_spent"`
}

// userTotalAll is a cross-project user time total, annotated with a display
// name since the caller has no single project context to resolve it from
// (mirrors why timeLogWithContext carries names inline too). Grouped by
// users.id rather than project_members.id — see timeLogsUsersAll.
type userTotalAll struct {
	UserID       string `json:"user_id"`
	UserName     string `json:"user_name"`
	MinutesSpent int    `json:"minutes_spent"`
}

// timeLogWithContext is a time log entry annotated with the project and
// member display info needed to render a cross-project list, since the
// caller has no single project context to resolve those names from.
type timeLogWithContext struct {
	ID           string `json:"id"`
	ProjectID    string `json:"project_id"`
	ProjectName  string `json:"project_name"`
	TaskID       string `json:"task_id"`
	TaskTitle    string `json:"task_title"`
	MemberID     string `json:"member_id"`
	MemberName   string `json:"member_name"`
	SpentDate    string `json:"spent_date"`
	MinutesSpent int    `json:"minutes_spent"`
	Note         string `json:"note"`
	CreatedAt    string `json:"created_at"`
}

// pageMeta is embedded in paginated list responses.
type pageMeta struct {
	Total        int `json:"total"`
	TotalMinutes int `json:"total_minutes"`
	Page         int `json:"page"`
	PageSize     int `json:"page_size"`
}

type timeLogPage struct {
	Logs []timeLog `json:"logs"`
	pageMeta
}

type timeLogAllPage struct {
	Logs []timeLogWithContext `json:"logs"`
	pageMeta
}

// viewerInfo is the caller's identity + permission state within the current
// project, returned by GET /time-logs/me. The frontend uses it to decide
// whether to offer the "log time" form and edit/delete controls at all
// (CanWriteTasks — logging/editing time entries is gated by the same
// tasks.write permission as the routes themselves) and, separately, whether
// an entry belonging to another member may be edited/deleted (CanManageAll).
type viewerInfo struct {
	MemberID      string `json:"member_id"`
	CanWriteTasks bool   `json:"can_write_tasks"`
	CanManageAll  bool   `json:"can_manage_all"`
}

// viewerAllInfo is the caller's global permission state for the admin
// (cross-project) time tracking page, returned by GET /time-logs/viewer-all.
// There's no per-project ownership concept at this scope, so unlike
// viewerInfo this carries only the one flag the admin page's edit/delete
// controls need.
type viewerAllInfo struct {
	CanManageAll bool `json:"can_manage_all"`
}

// maxPageSize caps page_size on paginated time-log list endpoints.
const maxPageSize = 100

// parsePositiveInt parses s as a positive integer, falling back to def when
// s is empty, malformed, or not positive.
func parsePositiveInt(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return def
	}
	return n
}

// ── Row scanner helper ────────────────────────────────────────────────────────

type scanner struct {
	idx map[string]int
	row []any
}

func newRowScanner(cols []string, row []any) *scanner {
	idx := make(map[string]int, len(cols))
	for i, c := range cols {
		idx[c] = i
	}
	return &scanner{idx: idx, row: row}
}

func (s *scanner) str(col string) string {
	i, ok := s.idx[col]
	if !ok || i >= len(s.row) || s.row[i] == nil {
		return ""
	}
	switch v := s.row[i].(type) {
	case string:
		return v
	case *string:
		if v == nil {
			return ""
		}
		return *v
	default:
		return fmt.Sprintf("%v", s.row[i])
	}
}

func (s *scanner) intVal(col string) int {
	i, ok := s.idx[col]
	if !ok || i >= len(s.row) || s.row[i] == nil {
		return 0
	}
	if v, ok := s.row[i].(float64); ok {
		return int(v)
	}
	if v, ok := s.row[i].(int); ok {
		return v
	}
	return 0
}

// ── Time log route handlers ──────────────────────────────────────────────────

// viewer handles GET /time-logs/me.
func (p *timeLoggingPlugin) viewer(req *plugin.Request, res *plugin.Response) {
	ok(res, viewerInfo{
		MemberID:      req.Caller.CallerID,
		CanWriteTasks: p.perm.Check("tasks.write"),
		CanManageAll:  p.perm.Check(permManageAllTimeLogs),
	})
}

// viewerAll handles GET /time-logs/viewer-all (global/admin scope). Lets the
// admin page decide whether to offer edit/delete controls, backed by the
// global-scope updateTimeLogGlobal/deleteTimeLogGlobal routes.
func (p *timeLoggingPlugin) viewerAll(req *plugin.Request, res *plugin.Response) {
	ok(res, viewerAllInfo{CanManageAll: p.perm.Check(permManageAllTimeLogs)})
}

// listTimeLogs handles GET /tasks/:taskId/time-logs
// Returns all time-log entries for the task (every member's), most recent
// spent_date first. Gated by tasks.read only, not time_logging.view_all —
// intentionally: per-task time logs are part of that task's own visible
// history (like comments or activity), open to anyone who can already open
// the task. time_logging.view_all instead gates project-wide rollups
// (listProjectTimeLogs, timeLogsSummary) where the concern is aggregate
// reporting across a whole project, not a single task the caller can already
// see.
func (p *timeLoggingPlugin) listTimeLogs(req *plugin.Request, res *plugin.Response) {
	taskID := req.PathParam("taskId")
	projectID := req.Caller.ProjectID

	if !p.taskBelongsToProject(taskID, projectID, res) {
		return
	}

	logs, err := p.fetchTimeLogsForTask(taskID)
	if err != nil {
		p.log.Error("listTimeLogs: " + err.Error())
		res.Error(500, "failed to list time logs")
		return
	}
	ok(res, logs)
}

// createTimeLog handles POST /tasks/:taskId/time-logs
// Body: { "spent_date": "2026-07-13", "minutes_spent": 90, "note": "..." (optional) }
// member_id defaults to the caller unless explicitly provided, in which case
// the caller must hold time_logging.manage_all to attribute the entry to
// someone else — see canModify. Without it, a caller could otherwise
// fabricate time entries under another member's name, which is exactly the
// capability manage_all is meant to gate (update/delete already require it
// for the same reason).
func (p *timeLoggingPlugin) createTimeLog(req *plugin.Request, res *plugin.Response) {
	taskID := req.PathParam("taskId")
	projectID := req.Caller.ProjectID

	if !p.taskBelongsToProject(taskID, projectID, res) {
		return
	}

	type body struct {
		SpentDate    string  `json:"spent_date"`
		MinutesSpent int     `json:"minutes_spent"`
		Note         string  `json:"note"`
		MemberID     *string `json:"member_id"`
	}
	b, err := plugin.JSONBody[body](req)
	if err != nil {
		res.Error(400, "invalid request body")
		return
	}
	if b.SpentDate == "" {
		res.Error(400, "spent_date is required")
		return
	}
	if b.MinutesSpent <= 0 {
		res.Error(400, "minutes_spent must be greater than zero")
		return
	}

	memberID := req.Caller.CallerID
	if b.MemberID != nil && *b.MemberID != "" && *b.MemberID != memberID {
		if !p.memberBelongsToProject(*b.MemberID, projectID, res) {
			return
		}
		if !p.canModify(req, *b.MemberID) {
			res.Error(403, "you can only log time for yourself unless you hold time_logging.manage_all")
			return
		}
		memberID = *b.MemberID
	}

	now := nowStr()
	inserted, err := p.db.Query(
		`INSERT INTO task_time_logs (task_id, member_id, spent_date, minutes_spent, note, created_by, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`,
		taskID, memberID, b.SpentDate, b.MinutesSpent, b.Note, req.Caller.CallerID, now, now,
	)
	if err != nil || len(inserted.Rows) == 0 {
		if err != nil {
			p.log.Error("createTimeLog insert: " + err.Error())
		}
		res.Error(500, "failed to create time log")
		return
	}
	idSC := newRowScanner(inserted.Columns, inserted.Rows[0])
	id := idSC.str("id")
	tl := timeLog{
		ID:           id,
		TaskID:       taskID,
		MemberID:     memberID,
		SpentDate:    b.SpentDate,
		MinutesSpent: b.MinutesSpent,
		Note:         b.Note,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	plugin.RecordActivity(taskID, projectID, req.Caller.UserID, "task.time_log.created",
		map[string]any{
			"minutes_spent": b.MinutesSpent,
			"spent_date":    b.SpentDate,
			"_description":  fmt.Sprintf("logged %d minutes on %s", b.MinutesSpent, b.SpentDate),
		})
	created(res, tl)
}

// updateTimeLog handles PATCH /tasks/:taskId/time-logs/:logId
// All fields are optional; only provided fields are updated.
func (p *timeLoggingPlugin) updateTimeLog(req *plugin.Request, res *plugin.Response) {
	taskID := req.PathParam("taskId")
	logID := req.PathParam("logId")
	projectID := req.Caller.ProjectID

	if !p.taskBelongsToProject(taskID, projectID, res) {
		return
	}

	current, err := p.db.Query(
		`SELECT id, task_id, member_id, to_char(spent_date, 'YYYY-MM-DD') AS spent_date, minutes_spent, note, created_at
		 FROM task_time_logs
		 WHERE id = $1 AND task_id = $2`,
		logID, taskID,
	)
	if err != nil {
		p.log.Error("updateTimeLog fetch: " + err.Error())
		res.Error(500, "failed to update time log")
		return
	}
	if len(current.Rows) == 0 {
		res.Error(404, "time log not found")
		return
	}
	sc := newRowScanner(current.Columns, current.Rows[0])

	if !p.canModify(req, sc.str("member_id")) {
		res.Error(403, "you can only edit your own time log entries")
		return
	}

	type body struct {
		SpentDate    *string `json:"spent_date"`
		MinutesSpent *int    `json:"minutes_spent"`
		Note         *string `json:"note"`
	}
	b, err := plugin.JSONBody[body](req)
	if err != nil {
		res.Error(400, "invalid request body")
		return
	}
	if b.MinutesSpent != nil && *b.MinutesSpent <= 0 {
		res.Error(400, "minutes_spent must be greater than zero")
		return
	}

	spentDate := sc.str("spent_date")
	if b.SpentDate != nil && *b.SpentDate != "" {
		spentDate = *b.SpentDate
	}
	minutesSpent := sc.intVal("minutes_spent")
	if b.MinutesSpent != nil {
		minutesSpent = *b.MinutesSpent
	}
	note := sc.str("note")
	if b.Note != nil {
		note = *b.Note
	}

	now := nowStr()
	affected, err := p.db.Exec(
		`UPDATE task_time_logs
		 SET spent_date = $1, minutes_spent = $2, note = $3, updated_at = $4
		 WHERE id = $5 AND task_id = $6`,
		spentDate, minutesSpent, note, now, logID, taskID,
	)
	if err != nil {
		p.log.Error("updateTimeLog update: " + err.Error())
		res.Error(500, "failed to update time log")
		return
	}
	if affected == 0 {
		res.Error(404, "time log not found")
		return
	}
	tl := timeLog{
		ID:           logID,
		TaskID:       sc.str("task_id"),
		MemberID:     sc.str("member_id"),
		SpentDate:    spentDate,
		MinutesSpent: minutesSpent,
		Note:         note,
		CreatedAt:    sc.str("created_at"),
		UpdatedAt:    now,
	}
	plugin.RecordActivity(taskID, projectID, req.Caller.UserID, "task.time_log.updated",
		map[string]any{
			"minutes_spent": minutesSpent,
			"spent_date":    spentDate,
			"_description":  fmt.Sprintf("updated time log to %d minutes on %s", minutesSpent, spentDate),
		})
	ok(res, tl)
}

// deleteTimeLog handles DELETE /tasks/:taskId/time-logs/:logId
func (p *timeLoggingPlugin) deleteTimeLog(req *plugin.Request, res *plugin.Response) {
	taskID := req.PathParam("taskId")
	logID := req.PathParam("logId")
	projectID := req.Caller.ProjectID

	if !p.taskBelongsToProject(taskID, projectID, res) {
		return
	}

	existing, err := p.db.Query(
		`SELECT member_id, minutes_spent, to_char(spent_date, 'YYYY-MM-DD') AS spent_date FROM task_time_logs WHERE id = $1 AND task_id = $2`,
		logID, taskID,
	)
	if err != nil {
		p.log.Error("deleteTimeLog fetch: " + err.Error())
		res.Error(500, "failed to delete time log")
		return
	}
	if len(existing.Rows) == 0 {
		res.Error(404, "time log not found")
		return
	}
	sc := newRowScanner(existing.Columns, existing.Rows[0])
	minutesSpent := sc.intVal("minutes_spent")
	spentDate := sc.str("spent_date")

	if !p.canModify(req, sc.str("member_id")) {
		res.Error(403, "you can only delete your own time log entries")
		return
	}

	affected, err := p.db.Exec(
		`DELETE FROM task_time_logs WHERE id = $1 AND task_id = $2`,
		logID, taskID,
	)
	if err != nil {
		p.log.Error("deleteTimeLog: " + err.Error())
		res.Error(500, "failed to delete time log")
		return
	}
	if affected == 0 {
		res.Error(404, "time log not found")
		return
	}
	plugin.RecordActivity(taskID, projectID, req.Caller.UserID, "task.time_log.deleted",
		map[string]any{
			"minutes_spent": minutesSpent,
			"spent_date":    spentDate,
			"_description":  fmt.Sprintf("deleted time log of %d minutes on %s", minutesSpent, spentDate),
		})
	res.NoContent()
}

// ── Cross-project admin edit/delete ──────────────────────────────────────────
//
// updateTimeLogGlobal and deleteTimeLogGlobal back the admin (cross-project)
// time tracking page's edit/delete controls. They operate on a bare logId
// with no :projectId/:taskId in the path — unlike updateTimeLog/deleteTimeLog,
// there's no single project to scope the route to, and the caller may not
// even be a member of the entry's project. The host route gate requires the
// global-scope time_logging.manage_all permission (see plugin.json), which
// per its own declared description already grants "edit or delete any
// project member's time log entries, across every project" — so unlike
// canModify's self-or-manage_all check, no additional per-row ownership test
// is needed here: reaching the handler at all already proves the caller
// holds the one permission this capability is gated on.

// updateTimeLogGlobal handles PATCH /time-logs/all/:logId (global/admin scope).
func (p *timeLoggingPlugin) updateTimeLogGlobal(req *plugin.Request, res *plugin.Response) {
	logID := req.PathParam("logId")

	current, err := p.db.Query(
		`SELECT id, task_id, member_id, to_char(spent_date, 'YYYY-MM-DD') AS spent_date, minutes_spent, note, created_at
		 FROM task_time_logs WHERE id = $1`,
		logID,
	)
	if err != nil {
		p.log.Error("updateTimeLogGlobal fetch: " + err.Error())
		res.Error(500, "failed to update time log")
		return
	}
	if len(current.Rows) == 0 {
		res.Error(404, "time log not found")
		return
	}
	sc := newRowScanner(current.Columns, current.Rows[0])
	taskID := sc.str("task_id")
	projectID, err := p.projectIDForTask(taskID)
	if err != nil {
		p.log.Error("updateTimeLogGlobal task lookup: " + err.Error())
		res.Error(500, "failed to update time log")
		return
	}

	type body struct {
		SpentDate    *string `json:"spent_date"`
		MinutesSpent *int    `json:"minutes_spent"`
		Note         *string `json:"note"`
	}
	b, err := plugin.JSONBody[body](req)
	if err != nil {
		res.Error(400, "invalid request body")
		return
	}
	if b.MinutesSpent != nil && *b.MinutesSpent <= 0 {
		res.Error(400, "minutes_spent must be greater than zero")
		return
	}

	spentDate := sc.str("spent_date")
	if b.SpentDate != nil && *b.SpentDate != "" {
		spentDate = *b.SpentDate
	}
	minutesSpent := sc.intVal("minutes_spent")
	if b.MinutesSpent != nil {
		minutesSpent = *b.MinutesSpent
	}
	note := sc.str("note")
	if b.Note != nil {
		note = *b.Note
	}

	now := nowStr()
	affected, err := p.db.Exec(
		`UPDATE task_time_logs SET spent_date = $1, minutes_spent = $2, note = $3, updated_at = $4 WHERE id = $5`,
		spentDate, minutesSpent, note, now, logID,
	)
	if err != nil {
		p.log.Error("updateTimeLogGlobal update: " + err.Error())
		res.Error(500, "failed to update time log")
		return
	}
	if affected == 0 {
		res.Error(404, "time log not found")
		return
	}
	tl := timeLog{
		ID:           logID,
		TaskID:       taskID,
		MemberID:     sc.str("member_id"),
		SpentDate:    spentDate,
		MinutesSpent: minutesSpent,
		Note:         note,
		CreatedAt:    sc.str("created_at"),
		UpdatedAt:    now,
	}
	plugin.RecordActivity(taskID, projectID, req.Caller.UserID, "task.time_log.updated",
		map[string]any{
			"minutes_spent": minutesSpent,
			"spent_date":    spentDate,
			"_description":  fmt.Sprintf("updated time log to %d minutes on %s", minutesSpent, spentDate),
		})
	ok(res, tl)
}

// deleteTimeLogGlobal handles DELETE /time-logs/all/:logId (global/admin scope).
func (p *timeLoggingPlugin) deleteTimeLogGlobal(req *plugin.Request, res *plugin.Response) {
	logID := req.PathParam("logId")

	existing, err := p.db.Query(
		`SELECT task_id, minutes_spent, to_char(spent_date, 'YYYY-MM-DD') AS spent_date FROM task_time_logs WHERE id = $1`,
		logID,
	)
	if err != nil {
		p.log.Error("deleteTimeLogGlobal fetch: " + err.Error())
		res.Error(500, "failed to delete time log")
		return
	}
	if len(existing.Rows) == 0 {
		res.Error(404, "time log not found")
		return
	}
	sc := newRowScanner(existing.Columns, existing.Rows[0])
	taskID := sc.str("task_id")
	minutesSpent := sc.intVal("minutes_spent")
	spentDate := sc.str("spent_date")
	projectID, err := p.projectIDForTask(taskID)
	if err != nil {
		p.log.Error("deleteTimeLogGlobal task lookup: " + err.Error())
		res.Error(500, "failed to delete time log")
		return
	}

	affected, err := p.db.Exec(`DELETE FROM task_time_logs WHERE id = $1`, logID)
	if err != nil {
		p.log.Error("deleteTimeLogGlobal: " + err.Error())
		res.Error(500, "failed to delete time log")
		return
	}
	if affected == 0 {
		res.Error(404, "time log not found")
		return
	}
	plugin.RecordActivity(taskID, projectID, req.Caller.UserID, "task.time_log.deleted",
		map[string]any{
			"minutes_spent": minutesSpent,
			"spent_date":    spentDate,
			"_description":  fmt.Sprintf("deleted time log of %d minutes on %s", minutesSpent, spentDate),
		})
	res.NoContent()
}

// listProjectTimeLogs handles GET /time-logs
// Returns a page of time-log entries across all tasks in the caller's
// project, most recent spent_date first, so the project can show a single
// combined tracking view instead of per-task lists only.
//
// Supports optional query params: member_id, date_from, date_to (spent_date
// bounds, inclusive, YYYY-MM-DD), page, page_size. Filtering, sorting,
// pagination, and the total_minutes sum are all computed here rather than by
// the caller, so every consumer (web UI, MCP tools) sees the same numbers.
func (p *timeLoggingPlugin) listProjectTimeLogs(req *plugin.Request, res *plugin.Response) {
	projectID := req.Caller.ProjectID

	tasksResult, err := p.db.Query(
		`SELECT id FROM tasks WHERE project_id = $1`,
		projectID,
	)
	if err != nil {
		p.log.Error("listProjectTimeLogs tasks fetch: " + err.Error())
		res.Error(500, "failed to list time logs")
		return
	}

	logs := make([]timeLog, 0)
	for _, taskRow := range tasksResult.Rows {
		taskSC := newRowScanner(tasksResult.Columns, taskRow)
		taskID := taskSC.str("id")

		taskLogs, err := p.fetchTimeLogsForTask(taskID)
		if err != nil {
			p.log.Error("listProjectTimeLogs logs fetch: " + err.Error())
			res.Error(500, "failed to list time logs")
			return
		}
		logs = append(logs, taskLogs...)
	}

	memberID := req.QueryParam("member_id")
	dateFrom := req.QueryParam("date_from")
	dateTo := req.QueryParam("date_to")

	filtered := make([]timeLog, 0, len(logs))
	totalMinutes := 0
	for _, l := range logs {
		if memberID != "" && l.MemberID != memberID {
			continue
		}
		if dateFrom != "" && l.SpentDate < dateFrom {
			continue
		}
		if dateTo != "" && l.SpentDate > dateTo {
			continue
		}
		filtered = append(filtered, l)
		totalMinutes += l.MinutesSpent
	}

	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].SpentDate != filtered[j].SpentDate {
			return filtered[i].SpentDate > filtered[j].SpentDate
		}
		return filtered[i].CreatedAt > filtered[j].CreatedAt
	})

	pageSize := parsePositiveInt(req.QueryParam("page_size"), 10)
	if pageSize > maxPageSize {
		pageSize = maxPageSize
	}
	total := len(filtered)
	totalPages := (total + pageSize - 1) / pageSize
	if totalPages < 1 {
		totalPages = 1
	}
	page := parsePositiveInt(req.QueryParam("page"), 1)
	if page > totalPages {
		page = totalPages
	}

	start := (page - 1) * pageSize
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	pageLogs := make([]timeLog, end-start)
	copy(pageLogs, filtered[start:end])

	ok(res, timeLogPage{
		Logs: pageLogs,
		pageMeta: pageMeta{
			Total:        total,
			TotalMinutes: totalMinutes,
			Page:         page,
			PageSize:     pageSize,
		},
	})
}

// timeLogsSummary handles GET /time-logs/summary
// Returns total minutes spent per project member, across all tasks in the
// caller's project. Aggregation is done in Go (rather than SQL JOIN/GROUP BY)
// since a task's time logs are fetched per-task, matching the fetch pattern
// used elsewhere in this plugin.
func (p *timeLoggingPlugin) timeLogsSummary(req *plugin.Request, res *plugin.Response) {
	projectID := req.Caller.ProjectID

	tasksResult, err := p.db.Query(
		`SELECT id FROM tasks WHERE project_id = $1`,
		projectID,
	)
	if err != nil {
		p.log.Error("timeLogsSummary tasks fetch: " + err.Error())
		res.Error(500, "failed to compute time log summary")
		return
	}

	totalsByMember := make(map[string]int)
	order := make([]string, 0)

	for _, taskRow := range tasksResult.Rows {
		taskSC := newRowScanner(tasksResult.Columns, taskRow)
		taskID := taskSC.str("id")

		logsResult, err := p.db.Query(
			`SELECT member_id, minutes_spent FROM task_time_logs WHERE task_id = $1`,
			taskID,
		)
		if err != nil {
			p.log.Error("timeLogsSummary logs fetch: " + err.Error())
			res.Error(500, "failed to compute time log summary")
			return
		}
		for _, row := range logsResult.Rows {
			sc := newRowScanner(logsResult.Columns, row)
			memberID := sc.str("member_id")
			if _, seen := totalsByMember[memberID]; !seen {
				order = append(order, memberID)
			}
			totalsByMember[memberID] += sc.intVal("minutes_spent")
		}
	}

	totals := make([]memberTotal, 0, len(order))
	for _, memberID := range order {
		totals = append(totals, memberTotal{
			MemberID:     memberID,
			MinutesSpent: totalsByMember[memberID],
		})
	}
	sort.Slice(totals, func(i, j int) bool {
		return totals[i].MinutesSpent > totals[j].MinutesSpent
	})
	ok(res, totals)
}

// ── Cross-project admin summary ──────────────────────────────────────────────

// timeLogsSummaryAll handles GET /time-logs/summary-all (global/admin scope,
// no project context). Returns total minutes spent per project across every
// project in the instance, via a single SQL JOIN + GROUP BY rather than the
// per-task fetch loop used by the project-scoped handlers above — there is no
// single caller project to anchor an N+1 walk against here.
//
// This backs the admin page's Project filter dropdown, so it accepts the
// *other* active filters — user_id, date_from, date_to — and recomputes
// totals (and which projects even appear) against just that scope. It
// deliberately does NOT accept project_id: this endpoint enumerates the
// project dimension itself, so self-filtering it would collapse the dropdown
// to whatever's already selected, making it impossible to switch projects.
func (p *timeLoggingPlugin) timeLogsSummaryAll(req *plugin.Request, res *plugin.Response) {
	userID := req.QueryParam("user_id")
	dateFrom := req.QueryParam("date_from")
	dateTo := req.QueryParam("date_to")

	where := []string{"pr.deleted_at IS NULL"}
	args := make([]any, 0, 3)
	argN := 1
	if userID != "" {
		where = append(where, fmt.Sprintf("pm.user_id = $%d", argN))
		args = append(args, userID)
		argN++
	}
	if dateFrom != "" {
		where = append(where, fmt.Sprintf("tl.spent_date >= $%d", argN))
		args = append(args, dateFrom)
		argN++
	}
	if dateTo != "" {
		where = append(where, fmt.Sprintf("tl.spent_date <= $%d", argN))
		args = append(args, dateTo)
		argN++
	}
	whereSQL := strings.Join(where, " AND ")

	result, err := p.db.Query(
		fmt.Sprintf(
			`SELECT pr.id AS project_id, pr.name AS project_name,
			        COALESCE(SUM(tl.minutes_spent), 0) AS minutes_spent
			 FROM projects pr
			 LEFT JOIN tasks t ON t.project_id = pr.id
			 LEFT JOIN task_time_logs tl ON tl.task_id = t.id
			 LEFT JOIN project_members pm ON pm.id = tl.member_id
			 WHERE %s
			 GROUP BY pr.id, pr.name
			 HAVING COALESCE(SUM(tl.minutes_spent), 0) > 0
			 ORDER BY minutes_spent DESC`, whereSQL),
		args...,
	)
	if err != nil {
		p.log.Error("timeLogsSummaryAll: " + err.Error())
		res.Error(500, "failed to compute time log summary")
		return
	}

	totals := make([]projectTotal, 0, len(result.Rows))
	for _, row := range result.Rows {
		sc := newRowScanner(result.Columns, row)
		totals = append(totals, projectTotal{
			ProjectID:    sc.str("project_id"),
			ProjectName:  sc.str("project_name"),
			MinutesSpent: sc.intVal("minutes_spent"),
		})
	}
	ok(res, totals)
}

// listAllTimeLogs handles GET /time-logs/all (global/admin scope, no project
// context). Returns a page of time-log entries across every project in the
// instance, annotated with project and member display names, so the admin
// UI can show one combined entries table instead of per-project rollups
// only. Uses a single SQL JOIN with dynamic WHERE/LIMIT/OFFSET rather than
// the per-task Go-side fetch loop used by the project-scoped handler above —
// there is no single caller project to anchor an N+1 walk against here, and
// looping over every project instance-wide would not scale.
//
// Supports optional query params: project_id, user_id, date_from, date_to
// (spent_date bounds, inclusive, YYYY-MM-DD), page, page_size. Filtering by
// user_id (users.id) rather than member_id (project_members.id) matters here
// specifically: the same physical person has a different project_members row
// — and thus a different member_id — in every project they belong to, so a
// member_id filter could only ever match one project's worth of that
// person's entries. user_id is stable across projects, so one filter
// selection covers all of a person's entries instance-wide. That requires
// joining through project_members in the WHERE clause, so both queries below
// carry the same project_members/users joins even though only the page query
// needs project_members/users for display purposes.
//
// user_id filtering feeds into the same WHERE clause used by both the
// aggregate and page queries below, so total_minutes always reflects it too.
func (p *timeLoggingPlugin) listAllTimeLogs(req *plugin.Request, res *plugin.Response) {
	projectID := req.QueryParam("project_id")
	userID := req.QueryParam("user_id")
	dateFrom := req.QueryParam("date_from")
	dateTo := req.QueryParam("date_to")

	where := []string{"pr.deleted_at IS NULL"}
	args := make([]any, 0, 4)
	argN := 1
	if projectID != "" {
		where = append(where, fmt.Sprintf("pr.id = $%d", argN))
		args = append(args, projectID)
		argN++
	}
	if userID != "" {
		where = append(where, fmt.Sprintf("pm.user_id = $%d", argN))
		args = append(args, userID)
		argN++
	}
	if dateFrom != "" {
		where = append(where, fmt.Sprintf("tl.spent_date >= $%d", argN))
		args = append(args, dateFrom)
		argN++
	}
	if dateTo != "" {
		where = append(where, fmt.Sprintf("tl.spent_date <= $%d", argN))
		args = append(args, dateTo)
		argN++
	}
	whereSQL := strings.Join(where, " AND ")

	aggResult, err := p.db.Query(
		fmt.Sprintf(
			`SELECT COUNT(*) AS total, COALESCE(SUM(tl.minutes_spent), 0) AS total_minutes
			 FROM task_time_logs tl
			 JOIN tasks t ON t.id = tl.task_id
			 JOIN projects pr ON pr.id = t.project_id
			 LEFT JOIN project_members pm ON pm.id = tl.member_id
			 WHERE %s`, whereSQL),
		args...,
	)
	if err != nil {
		p.log.Error("listAllTimeLogs aggregate: " + err.Error())
		res.Error(500, "failed to list time logs")
		return
	}
	total := 0
	totalMinutes := 0
	if len(aggResult.Rows) > 0 {
		sc := newRowScanner(aggResult.Columns, aggResult.Rows[0])
		total = sc.intVal("total")
		totalMinutes = sc.intVal("total_minutes")
	}

	pageSize := parsePositiveInt(req.QueryParam("page_size"), 10)
	if pageSize > maxPageSize {
		pageSize = maxPageSize
	}
	totalPages := (total + pageSize - 1) / pageSize
	if totalPages < 1 {
		totalPages = 1
	}
	page := parsePositiveInt(req.QueryParam("page"), 1)
	if page > totalPages {
		page = totalPages
	}
	offset := (page - 1) * pageSize

	pageArgs := append(append([]any{}, args...), pageSize, offset)
	pageResult, err := p.db.Query(
		fmt.Sprintf(
			`SELECT tl.id, pr.id AS project_id, pr.name AS project_name, tl.task_id, t.title AS task_title,
			        tl.member_id, COALESCE(u.full_name, '') AS member_name,
			        to_char(tl.spent_date, 'YYYY-MM-DD') AS spent_date, tl.minutes_spent, tl.note, tl.created_at
			 FROM task_time_logs tl
			 JOIN tasks t ON t.id = tl.task_id
			 JOIN projects pr ON pr.id = t.project_id
			 LEFT JOIN project_members pm ON pm.id = tl.member_id
			 LEFT JOIN users u ON u.id = pm.user_id
			 WHERE %s
			 ORDER BY tl.spent_date DESC, tl.created_at DESC
			 LIMIT $%d OFFSET $%d`, whereSQL, argN, argN+1),
		pageArgs...,
	)
	if err != nil {
		p.log.Error("listAllTimeLogs page: " + err.Error())
		res.Error(500, "failed to list time logs")
		return
	}

	logs := make([]timeLogWithContext, 0, len(pageResult.Rows))
	for _, row := range pageResult.Rows {
		sc := newRowScanner(pageResult.Columns, row)
		logs = append(logs, timeLogWithContext{
			ID:           sc.str("id"),
			ProjectID:    sc.str("project_id"),
			ProjectName:  sc.str("project_name"),
			TaskID:       sc.str("task_id"),
			TaskTitle:    sc.str("task_title"),
			MemberID:     sc.str("member_id"),
			MemberName:   sc.str("member_name"),
			SpentDate:    sc.str("spent_date"),
			MinutesSpent: sc.intVal("minutes_spent"),
			Note:         sc.str("note"),
			CreatedAt:    sc.str("created_at"),
		})
	}

	ok(res, timeLogAllPage{
		Logs: logs,
		pageMeta: pageMeta{
			Total:        total,
			TotalMinutes: totalMinutes,
			Page:         page,
			PageSize:     pageSize,
		},
	})
}

// timeLogsUsersAll handles GET /time-logs/users-all (global/admin scope, no
// project context). Returns total minutes spent per user across every
// project in the instance, so the admin UI can populate a "User" filter
// without a global "list all members" call (plugins have no such call —
// project_members are project-scoped). Grouped by users.id rather than
// task_time_logs.member_id: the same person has a different member_id in
// every project they belong to, so grouping by member_id would split one
// person's total across multiple rows (one per project) instead of one row
// per person. Every row here has already logged time by construction
// (task_time_logs is the driving table), so unlike timeLogsSummaryAll's
// project rollup there's no need to filter out zero-total rows with a
// HAVING clause.
//
// This backs the admin page's User filter dropdown, so it accepts the
// *other* active filters — project_id, date_from, date_to — and recomputes
// totals (and which users even appear) against just that scope. It
// deliberately does NOT accept user_id: this endpoint enumerates the user
// dimension itself, so self-filtering it would collapse the dropdown to
// whatever's already selected, making it impossible to switch users.
func (p *timeLoggingPlugin) timeLogsUsersAll(req *plugin.Request, res *plugin.Response) {
	projectID := req.QueryParam("project_id")
	dateFrom := req.QueryParam("date_from")
	dateTo := req.QueryParam("date_to")

	where := []string{"pr.deleted_at IS NULL"}
	args := make([]any, 0, 3)
	argN := 1
	if projectID != "" {
		where = append(where, fmt.Sprintf("pr.id = $%d", argN))
		args = append(args, projectID)
		argN++
	}
	if dateFrom != "" {
		where = append(where, fmt.Sprintf("tl.spent_date >= $%d", argN))
		args = append(args, dateFrom)
		argN++
	}
	if dateTo != "" {
		where = append(where, fmt.Sprintf("tl.spent_date <= $%d", argN))
		args = append(args, dateTo)
		argN++
	}
	whereSQL := strings.Join(where, " AND ")

	result, err := p.db.Query(
		fmt.Sprintf(
			`SELECT u.id AS user_id, COALESCE(u.full_name, '') AS user_name,
			        SUM(tl.minutes_spent) AS minutes_spent
			 FROM task_time_logs tl
			 JOIN tasks t ON t.id = tl.task_id
			 JOIN projects pr ON pr.id = t.project_id
			 LEFT JOIN project_members pm ON pm.id = tl.member_id
			 LEFT JOIN users u ON u.id = pm.user_id
			 WHERE %s
			 GROUP BY u.id, u.full_name
			 ORDER BY minutes_spent DESC`, whereSQL),
		args...,
	)
	if err != nil {
		p.log.Error("timeLogsUsersAll: " + err.Error())
		res.Error(500, "failed to compute user totals")
		return
	}

	totals := make([]userTotalAll, 0, len(result.Rows))
	for _, row := range result.Rows {
		sc := newRowScanner(result.Columns, row)
		totals = append(totals, userTotalAll{
			UserID:       sc.str("user_id"),
			UserName:     sc.str("user_name"),
			MinutesSpent: sc.intVal("minutes_spent"),
		})
	}
	ok(res, totals)
}

// ── Task deleted event ────────────────────────────────────────────────────────

// handleTaskDeleted handles the task.deleted event.
// The task_time_logs table has ON DELETE CASCADE from tasks, but this
// handler ensures any remaining data is cleaned up if the cascade fails.
func (p *timeLoggingPlugin) handleTaskDeleted(evt *plugin.Event) {
	type payload struct {
		TaskID string `json:"task_id"`
	}
	ev, err := plugin.JSONPayload[payload](evt)
	if err != nil || ev.TaskID == "" {
		p.log.Warn("task.deleted: invalid payload")
		return
	}
	if _, err := p.db.Exec(
		`DELETE FROM task_time_logs WHERE task_id = $1`, ev.TaskID,
	); err != nil {
		p.log.Error("task.deleted cleanup: " + err.Error())
	}
}

// ── Internal helpers ──────────────────────────────────────────────────────────

// canModify reports whether the caller may act on a time log entry owned by
// memberID: either it's their own entry, or they hold the
// time_logging.manage_all custom permission. Used both to gate edit/delete of
// an existing entry and, in createTimeLog, to gate attributing a new entry to
// someone other than the caller.
func (p *timeLoggingPlugin) canModify(req *plugin.Request, memberID string) bool {
	return memberID == req.Caller.CallerID || p.perm.Check(permManageAllTimeLogs)
}

// projectIDForTask looks up the project_id of a task, for the global-scope
// admin handlers (updateTimeLogGlobal/deleteTimeLogGlobal) which only have a
// task_id (read off the time-log row itself) and need the project_id to
// record activity. A single-table lookup rather than a JOIN in the callers'
// own query, matching this file's other single-table query style.
func (p *timeLoggingPlugin) projectIDForTask(taskID string) (string, error) {
	result, err := p.db.Query(`SELECT project_id FROM tasks WHERE id = $1`, taskID)
	if err != nil {
		return "", err
	}
	if len(result.Rows) == 0 {
		return "", nil
	}
	return newRowScanner(result.Columns, result.Rows[0]).str("project_id"), nil
}

// taskBelongsToProject validates that the given task exists in the project.
// Sets a 404 error on res and returns false when validation fails.
func (p *timeLoggingPlugin) taskBelongsToProject(taskID, projectID string, res *plugin.Response) bool {
	result, err := p.db.Query(
		`SELECT id FROM tasks
		 WHERE id = $1 AND project_id = $2 AND deleted_at IS NULL`,
		taskID, projectID,
	)
	if err != nil {
		p.log.Error("taskBelongsToProject: " + err.Error())
		res.Error(500, "internal error")
		return false
	}
	if len(result.Rows) == 0 {
		res.Error(404, "task not found")
		return false
	}
	return true
}

// memberBelongsToProject validates that memberID is a project_members row of
// projectID, so a caller can't attribute a time-log entry to a member of a
// different project (or a nonexistent one) by passing an arbitrary member_id.
// Sets a 400 error on res and returns false when validation fails.
func (p *timeLoggingPlugin) memberBelongsToProject(memberID, projectID string, res *plugin.Response) bool {
	result, err := p.db.Query(
		`SELECT id FROM project_members
		 WHERE id = $1 AND project_id = $2 AND deleted_at IS NULL`,
		memberID, projectID,
	)
	if err != nil {
		p.log.Error("memberBelongsToProject: " + err.Error())
		res.Error(500, "internal error")
		return false
	}
	if len(result.Rows) == 0 {
		res.Error(400, "member_id does not belong to this project")
		return false
	}
	return true
}

// fetchTimeLogsForTask returns all time logs for a task ordered by spent_date desc.
func (p *timeLoggingPlugin) fetchTimeLogsForTask(taskID string) ([]timeLog, error) {
	result, err := p.db.Query(
		`SELECT id, task_id, member_id, to_char(spent_date, 'YYYY-MM-DD') AS spent_date, minutes_spent, note, created_at, updated_at
		 FROM task_time_logs
		 WHERE task_id = $1
		 ORDER BY spent_date DESC, created_at DESC`,
		taskID,
	)
	if err != nil {
		return nil, err
	}

	logs := make([]timeLog, 0, len(result.Rows))
	for _, row := range result.Rows {
		sc := newRowScanner(result.Columns, row)
		logs = append(logs, timeLog{
			ID:           sc.str("id"),
			TaskID:       sc.str("task_id"),
			MemberID:     sc.str("member_id"),
			SpentDate:    sc.str("spent_date"),
			MinutesSpent: sc.intVal("minutes_spent"),
			Note:         sc.str("note"),
			CreatedAt:    sc.str("created_at"),
			UpdatedAt:    sc.str("updated_at"),
		})
	}
	return logs, nil
}
