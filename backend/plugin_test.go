package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	plugin "github.com/Paca-AI/plugin-sdk-go"
	"github.com/Paca-AI/plugin-sdk-go/plugintest"
)

// ── Helpers ───────────────────────────────────────────────────────────────────

const (
	testProjectID  = "project-1"
	testTaskID     = "task-1"
	testMemberID   = "member-1"
	testMember2ID  = "member-2"
	otherProjectID = "project-2"
)

func setupPlugin(t *testing.T) *plugintest.Context {
	t.Helper()
	tc := plugintest.NewContext(t)

	// Seed the tasks table (public schema, accessed via search_path)
	tc.DB.SeedRows("tasks", []string{"id", "project_id", "deleted_at"}, [][]any{
		{testTaskID, testProjectID, nil},
	})
	// Seed project_members so memberBelongsToProject can validate member_id.
	// testMember2ID belongs to the same project as the caller; the third row
	// belongs to a different project entirely, for the cross-project
	// rejection test.
	tc.DB.SeedRows("project_members", []string{"id", "project_id", "deleted_at"}, [][]any{
		{testMemberID, testProjectID, nil},
		{testMember2ID, testProjectID, nil},
		{"member-other-project", otherProjectID, nil},
	})
	// Seed empty plugin table so INSERT/SELECT/DELETE can operate on it.
	tc.DB.SeedRows("task_time_logs",
		[]string{"id", "task_id", "member_id", "spent_date", "minutes_spent", "note", "created_by", "created_at", "updated_at"},
		nil)

	var p timeLoggingPlugin
	if err := p.Init(tc.PluginContext()); err != nil {
		t.Fatal("Init failed:", err)
	}
	return tc
}

// callerReqAs returns a request identity for a different project member than
// the default caller, used to test ownership enforcement.
func callerReqAs(memberID string) plugintest.Request {
	req := callerReq()
	req.Caller.CallerID = memberID
	return req
}

func callerReq() plugintest.Request {
	return plugintest.Request{
		Caller: plugin.CallerIdentity{
			ProjectID:  testProjectID,
			CallerID:   testMemberID,
			CallerRole: "PROJECT_MEMBER",
		},
		PathParams: map[string]string{},
	}
}

func withPathParams(req plugintest.Request, params map[string]string) plugintest.Request {
	m := make(map[string]string, len(req.PathParams)+len(params))
	for k, v := range req.PathParams {
		m[k] = v
	}
	for k, v := range params {
		m[k] = v
	}
	req.PathParams = m
	return req
}

func withQuery(req plugintest.Request, query map[string]string) plugintest.Request {
	m := make(map[string]string, len(req.Query)+len(query))
	for k, v := range req.Query {
		m[k] = v
	}
	for k, v := range query {
		m[k] = v
	}
	req.Query = m
	return req
}

// ── Time log CRUD tests ───────────────────────────────────────────────────────

func TestListTimeLogs_Empty(t *testing.T) {
	tc := setupPlugin(t)
	res := tc.Call("GET", "/tasks/:taskId/time-logs",
		withPathParams(callerReq(), map[string]string{"taskId": testTaskID}))

	if res.StatusCode != 200 {
		t.Fatalf("expected 200, got %d: %s", res.StatusCode, res.BodyString())
	}
	var env struct {
		Success bool      `json:"success"`
		Data    []timeLog `json:"data"`
	}
	if err := json.Unmarshal(res.Body, &env); err != nil {
		t.Fatal(err)
	}
	if !env.Success || len(env.Data) != 0 {
		t.Fatalf("expected empty list, got %+v", env.Data)
	}
}

func TestCreateAndListTimeLog(t *testing.T) {
	tc := setupPlugin(t)

	res := tc.Call("POST", "/tasks/:taskId/time-logs",
		withPathParams(callerReq(), map[string]string{"taskId": testTaskID}).
			WithJSONBody(map[string]any{
				"spent_date":    "2026-07-13",
				"minutes_spent": 90,
				"note":          "Worked on the API",
			}))

	if res.StatusCode != 201 {
		t.Fatalf("expected 201, got %d: %s", res.StatusCode, res.BodyString())
	}
	var createEnv struct {
		Data timeLog `json:"data"`
	}
	if err := json.Unmarshal(res.Body, &createEnv); err != nil {
		t.Fatal(err)
	}
	if createEnv.Data.MinutesSpent != 90 {
		t.Fatalf("unexpected minutes_spent: %d", createEnv.Data.MinutesSpent)
	}
	if createEnv.Data.MemberID != testMemberID {
		t.Fatalf("expected member_id to default to caller, got %s", createEnv.Data.MemberID)
	}

	listRes := tc.Call("GET", "/tasks/:taskId/time-logs",
		withPathParams(callerReq(), map[string]string{"taskId": testTaskID}))

	if listRes.StatusCode != 200 {
		t.Fatalf("expected 200, got %d: %s", listRes.StatusCode, listRes.BodyString())
	}
	var listEnv struct {
		Data []timeLog `json:"data"`
	}
	if err := json.Unmarshal(listRes.Body, &listEnv); err != nil {
		t.Fatal(err)
	}
	if len(listEnv.Data) != 1 || listEnv.Data[0].MinutesSpent != 90 {
		t.Fatalf("expected 1 time log, got %+v", listEnv.Data)
	}
}

func TestCreateTimeLog_MissingSpentDate(t *testing.T) {
	tc := setupPlugin(t)

	res := tc.Call("POST", "/tasks/:taskId/time-logs",
		withPathParams(callerReq(), map[string]string{"taskId": testTaskID}).
			WithJSONBody(map[string]any{"minutes_spent": 30}))

	if res.StatusCode != 400 {
		t.Fatalf("expected 400, got %d", res.StatusCode)
	}
}

func TestCreateTimeLog_InvalidMinutes(t *testing.T) {
	tc := setupPlugin(t)

	res := tc.Call("POST", "/tasks/:taskId/time-logs",
		withPathParams(callerReq(), map[string]string{"taskId": testTaskID}).
			WithJSONBody(map[string]any{"spent_date": "2026-07-13", "minutes_spent": 0}))

	if res.StatusCode != 400 {
		t.Fatalf("expected 400, got %d", res.StatusCode)
	}
}

func TestUpdateTimeLog(t *testing.T) {
	tc := setupPlugin(t)

	createRes := tc.Call("POST", "/tasks/:taskId/time-logs",
		withPathParams(callerReq(), map[string]string{"taskId": testTaskID}).
			WithJSONBody(map[string]any{
				"spent_date":    "2026-07-13",
				"minutes_spent": 60,
			}))
	if createRes.StatusCode != 201 {
		t.Fatalf("expected 201, got %d: %s", createRes.StatusCode, createRes.BodyString())
	}
	var createEnv struct {
		Data timeLog `json:"data"`
	}
	_ = json.Unmarshal(createRes.Body, &createEnv)

	patchRes := tc.Call("PATCH", "/tasks/:taskId/time-logs/:logId",
		withPathParams(callerReq(), map[string]string{
			"taskId": testTaskID,
			"logId":  createEnv.Data.ID,
		}).WithJSONBody(map[string]any{"minutes_spent": 120, "note": "Updated"}))

	if patchRes.StatusCode != 200 {
		t.Fatalf("expected 200, got %d: %s", patchRes.StatusCode, patchRes.BodyString())
	}
	var patchEnv struct {
		Data timeLog `json:"data"`
	}
	_ = json.Unmarshal(patchRes.Body, &patchEnv)

	if patchEnv.Data.ID != createEnv.Data.ID {
		t.Fatalf("expected same time log id, got %s", patchEnv.Data.ID)
	}
	if patchEnv.Data.MinutesSpent != 120 {
		t.Fatalf("expected minutes_spent=120, got %d", patchEnv.Data.MinutesSpent)
	}
	if patchEnv.Data.Note != "Updated" {
		t.Fatalf("expected note to be updated, got %q", patchEnv.Data.Note)
	}
	if patchEnv.Data.SpentDate != "2026-07-13" {
		t.Fatalf("expected spent_date to remain unchanged, got %q", patchEnv.Data.SpentDate)
	}
}

func TestUpdateTimeLog_InvalidMinutes(t *testing.T) {
	tc := setupPlugin(t)

	createRes := tc.Call("POST", "/tasks/:taskId/time-logs",
		withPathParams(callerReq(), map[string]string{"taskId": testTaskID}).
			WithJSONBody(map[string]any{"spent_date": "2026-07-13", "minutes_spent": 60}))
	var createEnv struct {
		Data timeLog `json:"data"`
	}
	_ = json.Unmarshal(createRes.Body, &createEnv)

	patchRes := tc.Call("PATCH", "/tasks/:taskId/time-logs/:logId",
		withPathParams(callerReq(), map[string]string{
			"taskId": testTaskID,
			"logId":  createEnv.Data.ID,
		}).WithJSONBody(map[string]any{"minutes_spent": -5}))

	if patchRes.StatusCode != 400 {
		t.Fatalf("expected 400, got %d", patchRes.StatusCode)
	}
}

func TestDeleteTimeLog(t *testing.T) {
	tc := setupPlugin(t)

	createRes := tc.Call("POST", "/tasks/:taskId/time-logs",
		withPathParams(callerReq(), map[string]string{"taskId": testTaskID}).
			WithJSONBody(map[string]any{"spent_date": "2026-07-13", "minutes_spent": 45}))
	var env struct {
		Data timeLog `json:"data"`
	}
	_ = json.Unmarshal(createRes.Body, &env)

	delRes := tc.Call("DELETE", "/tasks/:taskId/time-logs/:logId",
		withPathParams(callerReq(), map[string]string{
			"taskId": testTaskID,
			"logId":  env.Data.ID,
		}))

	if delRes.StatusCode != 204 {
		t.Fatalf("expected 204, got %d: %s", delRes.StatusCode, delRes.BodyString())
	}

	listRes := tc.Call("GET", "/tasks/:taskId/time-logs",
		withPathParams(callerReq(), map[string]string{"taskId": testTaskID}))
	var listEnv struct {
		Data []timeLog `json:"data"`
	}
	_ = json.Unmarshal(listRes.Body, &listEnv)
	if len(listEnv.Data) != 0 {
		t.Fatalf("expected time log to be deleted, got %+v", listEnv.Data)
	}
}

// ── Project-wide list tests ──────────────────────────────────────────────────

func TestListProjectTimeLogs(t *testing.T) {
	tc := setupPlugin(t)
	tc.Permissions.Grant(permManageAllTimeLogs) // needed to seed an entry for testMember2ID below

	tc.Call("POST", "/tasks/:taskId/time-logs",
		withPathParams(callerReq(), map[string]string{"taskId": testTaskID}).
			WithJSONBody(map[string]any{"spent_date": "2026-07-11", "minutes_spent": 30}))
	tc.Call("POST", "/tasks/:taskId/time-logs",
		withPathParams(callerReq(), map[string]string{"taskId": testTaskID}).
			WithJSONBody(map[string]any{
				"spent_date":    "2026-07-12",
				"minutes_spent": 60,
				"member_id":     testMember2ID,
			}))

	res := tc.Call("GET", "/time-logs", callerReq())
	if res.StatusCode != 200 {
		t.Fatalf("expected 200, got %d: %s", res.StatusCode, res.BodyString())
	}
	var env struct {
		Data timeLogPage `json:"data"`
	}
	if err := json.Unmarshal(res.Body, &env); err != nil {
		t.Fatal(err)
	}
	if len(env.Data.Logs) != 2 {
		t.Fatalf("expected 2 time logs across the project, got %+v", env.Data.Logs)
	}
	if env.Data.Total != 2 {
		t.Fatalf("expected total=2, got %d", env.Data.Total)
	}
	if env.Data.TotalMinutes != 90 {
		t.Fatalf("expected total_minutes=90, got %d", env.Data.TotalMinutes)
	}
	// Most recent spent_date first.
	if env.Data.Logs[0].SpentDate != "2026-07-12" {
		t.Fatalf("expected most recent entry first, got %+v", env.Data.Logs)
	}
}

func TestListProjectTimeLogs_Empty(t *testing.T) {
	tc := setupPlugin(t)
	res := tc.Call("GET", "/time-logs", callerReq())

	if res.StatusCode != 200 {
		t.Fatalf("expected 200, got %d: %s", res.StatusCode, res.BodyString())
	}
	var env struct {
		Data timeLogPage `json:"data"`
	}
	if err := json.Unmarshal(res.Body, &env); err != nil {
		t.Fatal(err)
	}
	if len(env.Data.Logs) != 0 {
		t.Fatalf("expected empty list, got %+v", env.Data.Logs)
	}
	if env.Data.Total != 0 || env.Data.TotalMinutes != 0 {
		t.Fatalf("expected zeroed totals, got %+v", env.Data)
	}
}

func TestListProjectTimeLogs_FilterByMember(t *testing.T) {
	tc := setupPlugin(t)
	tc.Permissions.Grant(permManageAllTimeLogs) // needed to seed an entry for testMember2ID below

	tc.Call("POST", "/tasks/:taskId/time-logs",
		withPathParams(callerReq(), map[string]string{"taskId": testTaskID}).
			WithJSONBody(map[string]any{"spent_date": "2026-07-11", "minutes_spent": 30}))
	tc.Call("POST", "/tasks/:taskId/time-logs",
		withPathParams(callerReq(), map[string]string{"taskId": testTaskID}).
			WithJSONBody(map[string]any{
				"spent_date":    "2026-07-12",
				"minutes_spent": 60,
				"member_id":     testMember2ID,
			}))

	res := tc.Call("GET", "/time-logs", withQuery(callerReq(), map[string]string{"member_id": testMember2ID}))
	if res.StatusCode != 200 {
		t.Fatalf("expected 200, got %d: %s", res.StatusCode, res.BodyString())
	}
	var env struct {
		Data timeLogPage `json:"data"`
	}
	if err := json.Unmarshal(res.Body, &env); err != nil {
		t.Fatal(err)
	}
	if len(env.Data.Logs) != 1 || env.Data.Logs[0].MemberID != testMember2ID {
		t.Fatalf("expected 1 entry for member-2, got %+v", env.Data.Logs)
	}
	if env.Data.Total != 1 || env.Data.TotalMinutes != 60 {
		t.Fatalf("expected total=1 total_minutes=60, got %+v", env.Data)
	}
}

func TestListProjectTimeLogs_FilterByDateRange(t *testing.T) {
	tc := setupPlugin(t)

	tc.Call("POST", "/tasks/:taskId/time-logs",
		withPathParams(callerReq(), map[string]string{"taskId": testTaskID}).
			WithJSONBody(map[string]any{"spent_date": "2026-07-01", "minutes_spent": 30}))
	tc.Call("POST", "/tasks/:taskId/time-logs",
		withPathParams(callerReq(), map[string]string{"taskId": testTaskID}).
			WithJSONBody(map[string]any{"spent_date": "2026-07-15", "minutes_spent": 45}))
	tc.Call("POST", "/tasks/:taskId/time-logs",
		withPathParams(callerReq(), map[string]string{"taskId": testTaskID}).
			WithJSONBody(map[string]any{"spent_date": "2026-07-30", "minutes_spent": 60}))

	res := tc.Call("GET", "/time-logs", withQuery(callerReq(), map[string]string{
		"date_from": "2026-07-10",
		"date_to":   "2026-07-20",
	}))
	if res.StatusCode != 200 {
		t.Fatalf("expected 200, got %d: %s", res.StatusCode, res.BodyString())
	}
	var env struct {
		Data timeLogPage `json:"data"`
	}
	if err := json.Unmarshal(res.Body, &env); err != nil {
		t.Fatal(err)
	}
	if len(env.Data.Logs) != 1 || env.Data.Logs[0].SpentDate != "2026-07-15" {
		t.Fatalf("expected only the 2026-07-15 entry, got %+v", env.Data.Logs)
	}
}

func TestListProjectTimeLogs_Pagination(t *testing.T) {
	tc := setupPlugin(t)

	dates := []string{"2026-07-01", "2026-07-02", "2026-07-03", "2026-07-04", "2026-07-05"}
	for _, d := range dates {
		tc.Call("POST", "/tasks/:taskId/time-logs",
			withPathParams(callerReq(), map[string]string{"taskId": testTaskID}).
				WithJSONBody(map[string]any{"spent_date": d, "minutes_spent": 10}))
	}

	res := tc.Call("GET", "/time-logs", withQuery(callerReq(), map[string]string{
		"page":      "2",
		"page_size": "2",
	}))
	if res.StatusCode != 200 {
		t.Fatalf("expected 200, got %d: %s", res.StatusCode, res.BodyString())
	}
	var env struct {
		Data timeLogPage `json:"data"`
	}
	if err := json.Unmarshal(res.Body, &env); err != nil {
		t.Fatal(err)
	}
	if len(env.Data.Logs) != 2 {
		t.Fatalf("expected 2 logs on page 2, got %+v", env.Data.Logs)
	}
	if env.Data.Total != 5 || env.Data.TotalMinutes != 50 {
		t.Fatalf("expected total=5 total_minutes=50 (unaffected by pagination), got %+v", env.Data)
	}
	if env.Data.Page != 2 || env.Data.PageSize != 2 {
		t.Fatalf("expected page=2 page_size=2 echoed back, got page=%d page_size=%d", env.Data.Page, env.Data.PageSize)
	}
	// Most recent first: page 1 = 07-05, 07-04; page 2 = 07-03, 07-02.
	if env.Data.Logs[0].SpentDate != "2026-07-03" || env.Data.Logs[1].SpentDate != "2026-07-02" {
		t.Fatalf("unexpected page 2 contents, got %+v", env.Data.Logs)
	}
}

// ── Summary tests ─────────────────────────────────────────────────────────────

func TestTimeLogsSummary(t *testing.T) {
	tc := setupPlugin(t)
	tc.Permissions.Grant(permManageAllTimeLogs) // needed to seed an entry for testMember2ID below

	// Two entries for the caller, one for a different member.
	tc.Call("POST", "/tasks/:taskId/time-logs",
		withPathParams(callerReq(), map[string]string{"taskId": testTaskID}).
			WithJSONBody(map[string]any{"spent_date": "2026-07-11", "minutes_spent": 30}))
	tc.Call("POST", "/tasks/:taskId/time-logs",
		withPathParams(callerReq(), map[string]string{"taskId": testTaskID}).
			WithJSONBody(map[string]any{"spent_date": "2026-07-12", "minutes_spent": 45}))
	tc.Call("POST", "/tasks/:taskId/time-logs",
		withPathParams(callerReq(), map[string]string{"taskId": testTaskID}).
			WithJSONBody(map[string]any{
				"spent_date":    "2026-07-12",
				"minutes_spent": 60,
				"member_id":     testMember2ID,
			}))

	res := tc.Call("GET", "/time-logs/summary", callerReq())
	if res.StatusCode != 200 {
		t.Fatalf("expected 200, got %d: %s", res.StatusCode, res.BodyString())
	}
	var env struct {
		Data []memberTotal `json:"data"`
	}
	if err := json.Unmarshal(res.Body, &env); err != nil {
		t.Fatal(err)
	}
	if len(env.Data) != 2 {
		t.Fatalf("expected 2 member totals, got %+v", env.Data)
	}

	totals := map[string]int{}
	for _, mt := range env.Data {
		totals[mt.MemberID] = mt.MinutesSpent
	}
	if totals[testMemberID] != 75 {
		t.Fatalf("expected caller total 75, got %d", totals[testMemberID])
	}
	if totals[testMember2ID] != 60 {
		t.Fatalf("expected member-2 total 60, got %d", totals[testMember2ID])
	}
}

// ── 404 guard tests ───────────────────────────────────────────────────────────

func TestListTimeLogs_UnknownTask(t *testing.T) {
	tc := setupPlugin(t)
	res := tc.Call("GET", "/tasks/:taskId/time-logs",
		withPathParams(callerReq(), map[string]string{"taskId": "nonexistent"}))

	if res.StatusCode != 404 {
		t.Fatalf("expected 404, got %d", res.StatusCode)
	}
}

func TestDeleteTimeLog_NotFound(t *testing.T) {
	tc := setupPlugin(t)
	res := tc.Call("DELETE", "/tasks/:taskId/time-logs/:logId",
		withPathParams(callerReq(), map[string]string{
			"taskId": testTaskID,
			"logId":  "nonexistent",
		}))

	if res.StatusCode != 404 {
		t.Fatalf("expected 404, got %d", res.StatusCode)
	}
}

// ── member_id project-membership validation ──────────────────────────────────

func TestCreateTimeLog_MemberFromDifferentProject_Rejected(t *testing.T) {
	tc := setupPlugin(t)

	res := tc.Call("POST", "/tasks/:taskId/time-logs",
		withPathParams(callerReq(), map[string]string{"taskId": testTaskID}).
			WithJSONBody(map[string]any{
				"spent_date":    "2026-07-13",
				"minutes_spent": 30,
				"member_id":     "member-other-project",
			}))

	if res.StatusCode != 400 {
		t.Fatalf("expected 400, got %d: %s", res.StatusCode, res.BodyString())
	}
}

func TestCreateTimeLog_UnknownMember_Rejected(t *testing.T) {
	tc := setupPlugin(t)

	res := tc.Call("POST", "/tasks/:taskId/time-logs",
		withPathParams(callerReq(), map[string]string{"taskId": testTaskID}).
			WithJSONBody(map[string]any{
				"spent_date":    "2026-07-13",
				"minutes_spent": 30,
				"member_id":     "nonexistent-member",
			}))

	if res.StatusCode != 400 {
		t.Fatalf("expected 400, got %d: %s", res.StatusCode, res.BodyString())
	}
}

func TestCreateTimeLog_MemberFromSameProject_ForbiddenWithoutManageAll(t *testing.T) {
	tc := setupPlugin(t)

	res := tc.Call("POST", "/tasks/:taskId/time-logs",
		withPathParams(callerReq(), map[string]string{"taskId": testTaskID}).
			WithJSONBody(map[string]any{
				"spent_date":    "2026-07-13",
				"minutes_spent": 30,
				"member_id":     testMember2ID,
			}))

	if res.StatusCode != 403 {
		t.Fatalf("expected 403, got %d: %s", res.StatusCode, res.BodyString())
	}
}

func TestCreateTimeLog_MemberFromSameProject_ManageAll_Allowed(t *testing.T) {
	tc := setupPlugin(t)
	tc.Permissions.Grant(permManageAllTimeLogs)

	res := tc.Call("POST", "/tasks/:taskId/time-logs",
		withPathParams(callerReq(), map[string]string{"taskId": testTaskID}).
			WithJSONBody(map[string]any{
				"spent_date":    "2026-07-13",
				"minutes_spent": 30,
				"member_id":     testMember2ID,
			}))

	if res.StatusCode != 201 {
		t.Fatalf("expected 201, got %d: %s", res.StatusCode, res.BodyString())
	}
	var env struct {
		Data timeLog `json:"data"`
	}
	_ = json.Unmarshal(res.Body, &env)
	if env.Data.MemberID != testMember2ID {
		t.Fatalf("expected member_id=%s, got %s", testMember2ID, env.Data.MemberID)
	}
}

func TestCreateTimeLog_OwnMemberID_AllowedWithoutManageAll(t *testing.T) {
	tc := setupPlugin(t)

	res := tc.Call("POST", "/tasks/:taskId/time-logs",
		withPathParams(callerReq(), map[string]string{"taskId": testTaskID}).
			WithJSONBody(map[string]any{
				"spent_date":    "2026-07-13",
				"minutes_spent": 30,
				"member_id":     testMemberID,
			}))

	if res.StatusCode != 201 {
		t.Fatalf("expected 201, got %d: %s", res.StatusCode, res.BodyString())
	}
}

// ── Ownership enforcement (time_logging.manage_all) ───────────────────────────

func TestUpdateTimeLog_Forbidden_NotOwner(t *testing.T) {
	tc := setupPlugin(t)

	createRes := tc.Call("POST", "/tasks/:taskId/time-logs",
		withPathParams(callerReq(), map[string]string{"taskId": testTaskID}).
			WithJSONBody(map[string]any{"spent_date": "2026-07-13", "minutes_spent": 60}))
	var createEnv struct {
		Data timeLog `json:"data"`
	}
	_ = json.Unmarshal(createRes.Body, &createEnv)

	// member-2 (not the owner, and without manage_all) tries to edit member-1's entry.
	res := tc.Call("PATCH", "/tasks/:taskId/time-logs/:logId",
		withPathParams(callerReqAs(testMember2ID), map[string]string{
			"taskId": testTaskID,
			"logId":  createEnv.Data.ID,
		}).WithJSONBody(map[string]any{"minutes_spent": 90}))

	if res.StatusCode != 403 {
		t.Fatalf("expected 403, got %d: %s", res.StatusCode, res.BodyString())
	}
}

func TestUpdateTimeLog_ManageAll_AllowsOtherMembersEntries(t *testing.T) {
	tc := setupPlugin(t)

	createRes := tc.Call("POST", "/tasks/:taskId/time-logs",
		withPathParams(callerReq(), map[string]string{"taskId": testTaskID}).
			WithJSONBody(map[string]any{"spent_date": "2026-07-13", "minutes_spent": 60}))
	var createEnv struct {
		Data timeLog `json:"data"`
	}
	_ = json.Unmarshal(createRes.Body, &createEnv)

	tc.Permissions.Grant(permManageAllTimeLogs)

	res := tc.Call("PATCH", "/tasks/:taskId/time-logs/:logId",
		withPathParams(callerReqAs(testMember2ID), map[string]string{
			"taskId": testTaskID,
			"logId":  createEnv.Data.ID,
		}).WithJSONBody(map[string]any{"minutes_spent": 90}))

	if res.StatusCode != 200 {
		t.Fatalf("expected 200, got %d: %s", res.StatusCode, res.BodyString())
	}
}

func TestDeleteTimeLog_Forbidden_NotOwner(t *testing.T) {
	tc := setupPlugin(t)

	createRes := tc.Call("POST", "/tasks/:taskId/time-logs",
		withPathParams(callerReq(), map[string]string{"taskId": testTaskID}).
			WithJSONBody(map[string]any{"spent_date": "2026-07-13", "minutes_spent": 45}))
	var env struct {
		Data timeLog `json:"data"`
	}
	_ = json.Unmarshal(createRes.Body, &env)

	res := tc.Call("DELETE", "/tasks/:taskId/time-logs/:logId",
		withPathParams(callerReqAs(testMember2ID), map[string]string{
			"taskId": testTaskID,
			"logId":  env.Data.ID,
		}))

	if res.StatusCode != 403 {
		t.Fatalf("expected 403, got %d: %s", res.StatusCode, res.BodyString())
	}

	// The entry must still exist — a 403 must not have deleted it anyway.
	listRes := tc.Call("GET", "/tasks/:taskId/time-logs",
		withPathParams(callerReq(), map[string]string{"taskId": testTaskID}))
	var listEnv struct {
		Data []timeLog `json:"data"`
	}
	_ = json.Unmarshal(listRes.Body, &listEnv)
	if len(listEnv.Data) != 1 {
		t.Fatalf("expected the entry to survive the forbidden delete, got %+v", listEnv.Data)
	}
}

func TestDeleteTimeLog_ManageAll_AllowsOtherMembersEntries(t *testing.T) {
	tc := setupPlugin(t)

	createRes := tc.Call("POST", "/tasks/:taskId/time-logs",
		withPathParams(callerReq(), map[string]string{"taskId": testTaskID}).
			WithJSONBody(map[string]any{"spent_date": "2026-07-13", "minutes_spent": 45}))
	var env struct {
		Data timeLog `json:"data"`
	}
	_ = json.Unmarshal(createRes.Body, &env)

	tc.Permissions.Grant(permManageAllTimeLogs)

	res := tc.Call("DELETE", "/tasks/:taskId/time-logs/:logId",
		withPathParams(callerReqAs(testMember2ID), map[string]string{
			"taskId": testTaskID,
			"logId":  env.Data.ID,
		}))

	if res.StatusCode != 204 {
		t.Fatalf("expected 204, got %d: %s", res.StatusCode, res.BodyString())
	}
}

// ── Cross-project admin edit/delete ──────────────────────────────────────────
//
// updateTimeLogGlobal/deleteTimeLogGlobal perform no in-handler permission
// check of their own — like the other global-scope handlers in this file
// (listAllTimeLogs, timeLogsSummaryAll, timeLogsUsersAll), they rely entirely
// on the host's requirePermissions(scope=global, time_logging.manage_all)
// route gate, which plugintest.Context.Call does not simulate. These tests
// exercise the handlers' own DB logic, not that gate.

func TestUpdateTimeLogGlobal_Success(t *testing.T) {
	tc := setupPlugin(t)

	createRes := tc.Call("POST", "/tasks/:taskId/time-logs",
		withPathParams(callerReq(), map[string]string{"taskId": testTaskID}).
			WithJSONBody(map[string]any{"spent_date": "2026-07-13", "minutes_spent": 60}))
	var createEnv struct {
		Data timeLog `json:"data"`
	}
	_ = json.Unmarshal(createRes.Body, &createEnv)

	res := tc.Call("PATCH", "/time-logs/all/:logId",
		withPathParams(callerReq(), map[string]string{"logId": createEnv.Data.ID}).
			WithJSONBody(map[string]any{"minutes_spent": 90, "note": "adjusted by admin"}))

	if res.StatusCode != 200 {
		t.Fatalf("expected 200, got %d: %s", res.StatusCode, res.BodyString())
	}
	var env struct {
		Data timeLog `json:"data"`
	}
	if err := json.Unmarshal(res.Body, &env); err != nil {
		t.Fatal(err)
	}
	if env.Data.MinutesSpent != 90 || env.Data.Note != "adjusted by admin" {
		t.Fatalf("expected updated fields, got %+v", env.Data)
	}
}

func TestUpdateTimeLogGlobal_NotFound(t *testing.T) {
	tc := setupPlugin(t)

	res := tc.Call("PATCH", "/time-logs/all/:logId",
		withPathParams(callerReq(), map[string]string{"logId": "nonexistent"}).
			WithJSONBody(map[string]any{"minutes_spent": 90}))

	if res.StatusCode != 404 {
		t.Fatalf("expected 404, got %d: %s", res.StatusCode, res.BodyString())
	}
}

func TestUpdateTimeLogGlobal_InvalidMinutes(t *testing.T) {
	tc := setupPlugin(t)

	createRes := tc.Call("POST", "/tasks/:taskId/time-logs",
		withPathParams(callerReq(), map[string]string{"taskId": testTaskID}).
			WithJSONBody(map[string]any{"spent_date": "2026-07-13", "minutes_spent": 60}))
	var createEnv struct {
		Data timeLog `json:"data"`
	}
	_ = json.Unmarshal(createRes.Body, &createEnv)

	res := tc.Call("PATCH", "/time-logs/all/:logId",
		withPathParams(callerReq(), map[string]string{"logId": createEnv.Data.ID}).
			WithJSONBody(map[string]any{"minutes_spent": 0}))

	if res.StatusCode != 400 {
		t.Fatalf("expected 400, got %d: %s", res.StatusCode, res.BodyString())
	}
}

func TestDeleteTimeLogGlobal_Success(t *testing.T) {
	tc := setupPlugin(t)

	createRes := tc.Call("POST", "/tasks/:taskId/time-logs",
		withPathParams(callerReq(), map[string]string{"taskId": testTaskID}).
			WithJSONBody(map[string]any{"spent_date": "2026-07-13", "minutes_spent": 45}))
	var createEnv struct {
		Data timeLog `json:"data"`
	}
	_ = json.Unmarshal(createRes.Body, &createEnv)

	res := tc.Call("DELETE", "/time-logs/all/:logId",
		withPathParams(callerReq(), map[string]string{"logId": createEnv.Data.ID}))

	if res.StatusCode != 204 {
		t.Fatalf("expected 204, got %d: %s", res.StatusCode, res.BodyString())
	}

	listRes := tc.Call("GET", "/tasks/:taskId/time-logs",
		withPathParams(callerReq(), map[string]string{"taskId": testTaskID}))
	var listEnv struct {
		Data []timeLog `json:"data"`
	}
	_ = json.Unmarshal(listRes.Body, &listEnv)
	if len(listEnv.Data) != 0 {
		t.Fatalf("expected the entry to be gone, got %+v", listEnv.Data)
	}
}

func TestDeleteTimeLogGlobal_NotFound(t *testing.T) {
	tc := setupPlugin(t)

	res := tc.Call("DELETE", "/time-logs/all/:logId",
		withPathParams(callerReq(), map[string]string{"logId": "nonexistent"}))

	if res.StatusCode != 404 {
		t.Fatalf("expected 404, got %d: %s", res.StatusCode, res.BodyString())
	}
}

// ── Global viewer endpoint ─────────────────────────────────────────────────────

func TestViewerAll_DefaultNoManageAll(t *testing.T) {
	tc := setupPlugin(t)

	res := tc.Call("GET", "/time-logs/viewer-all", callerReq())
	if res.StatusCode != 200 {
		t.Fatalf("expected 200, got %d: %s", res.StatusCode, res.BodyString())
	}
	var env struct {
		Data viewerAllInfo `json:"data"`
	}
	if err := json.Unmarshal(res.Body, &env); err != nil {
		t.Fatal(err)
	}
	if env.Data.CanManageAll {
		t.Fatalf("expected can_manage_all=false by default, got %+v", env.Data)
	}
}

func TestViewerAll_ManageAllGranted(t *testing.T) {
	tc := setupPlugin(t)
	tc.Permissions.Grant(permManageAllTimeLogs)

	res := tc.Call("GET", "/time-logs/viewer-all", callerReq())
	var env struct {
		Data viewerAllInfo `json:"data"`
	}
	if err := json.Unmarshal(res.Body, &env); err != nil {
		t.Fatal(err)
	}
	if !env.Data.CanManageAll {
		t.Fatalf("expected can_manage_all=true after granting, got %+v", env.Data)
	}
}

// ── Viewer endpoint ────────────────────────────────────────────────────────────

func TestViewer_DefaultNoManageAll(t *testing.T) {
	tc := setupPlugin(t)

	res := tc.Call("GET", "/time-logs/me", callerReq())
	if res.StatusCode != 200 {
		t.Fatalf("expected 200, got %d: %s", res.StatusCode, res.BodyString())
	}
	var env struct {
		Data viewerInfo `json:"data"`
	}
	if err := json.Unmarshal(res.Body, &env); err != nil {
		t.Fatal(err)
	}
	if env.Data.MemberID != testMemberID {
		t.Fatalf("expected member_id=%s, got %+v", testMemberID, env.Data)
	}
	if env.Data.CanManageAll {
		t.Fatalf("expected can_manage_all=false by default, got %+v", env.Data)
	}
}

func TestViewer_ManageAllGranted(t *testing.T) {
	tc := setupPlugin(t)
	tc.Permissions.Grant(permManageAllTimeLogs)

	res := tc.Call("GET", "/time-logs/me", callerReq())
	var env struct {
		Data viewerInfo `json:"data"`
	}
	if err := json.Unmarshal(res.Body, &env); err != nil {
		t.Fatal(err)
	}
	if !env.Data.CanManageAll {
		t.Fatalf("expected can_manage_all=true after granting, got %+v", env.Data)
	}
}

func TestViewer_DefaultNoWriteTasks(t *testing.T) {
	tc := setupPlugin(t)

	res := tc.Call("GET", "/time-logs/me", callerReq())
	var env struct {
		Data viewerInfo `json:"data"`
	}
	if err := json.Unmarshal(res.Body, &env); err != nil {
		t.Fatal(err)
	}
	if env.Data.CanWriteTasks {
		t.Fatalf("expected can_write_tasks=false by default, got %+v", env.Data)
	}
}

func TestViewer_WriteTasksGranted(t *testing.T) {
	tc := setupPlugin(t)
	tc.Permissions.Grant("tasks.write")

	res := tc.Call("GET", "/time-logs/me", callerReq())
	var env struct {
		Data viewerInfo `json:"data"`
	}
	if err := json.Unmarshal(res.Body, &env); err != nil {
		t.Fatal(err)
	}
	if !env.Data.CanWriteTasks {
		t.Fatalf("expected can_write_tasks=true after granting, got %+v", env.Data)
	}
}

// ── Manifest / route-table parity ─────────────────────────────────────────────
//
// The host only enforces a route's declared plugin.json middlewares
// (optionalAuthn/requirePermissions/etc.) when the request path matches an
// entry in plugin.json's backend.routes. If Init() registers a route here
// that has no matching manifest entry (or the manifest path is wrong), the
// host silently falls back to its weak default policy — optionalAuthn +
// requireFreshPassword only, with no permission check at all — instead of
// failing closed. This test guards against that drift by asserting every
// manifest route resolves to a handler actually registered in Init().

func TestManifestRoutesMatchRegisteredRoutes(t *testing.T) {
	data, err := os.ReadFile("../plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Backend struct {
			Routes []struct {
				Method string `json:"method"`
				Path   string `json:"path"`
			} `json:"routes"`
		} `json:"backend"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Backend.Routes) == 0 {
		t.Fatal("plugin.json declares no backend routes")
	}

	tc := setupPlugin(t)
	for _, r := range m.Backend.Routes {
		relPath := stripProjectPrefix(r.Path)
		req := &plugin.Request{PathParams: map[string]string{}}
		res := plugin.NewResponse()
		if ok := plugin.DispatchRoute(tc.PluginContext(), r.Method, relPath, req, res); !ok {
			t.Errorf("plugin.json declares %s %s (relative path %s) but Init() registers no matching route — "+
				"this path would fall back to the host's default policy (no permission check)",
				r.Method, r.Path, relPath)
		}
	}
}

// stripProjectPrefix mirrors plugin-sdk-go's splitProjectPath: it strips a
// leading "/projects/<segment>" pair so a manifest path can be compared
// against the relative pattern registered via ctx.Route in Init().
func stripProjectPrefix(path string) string {
	const prefix = "/projects/"
	if !strings.HasPrefix(path, prefix) {
		return path
	}
	rest := strings.TrimPrefix(path, prefix)
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) < 2 {
		return "/"
	}
	return "/" + parts[1]
}
