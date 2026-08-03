package main

import (
	"testing"

	plugin "github.com/Paca-AI/plugin-sdk-go"
	"github.com/Paca-AI/plugin-sdk-go/plugintest"
)

func conditionReqWithConfig(cfg any) plugintest.ConditionRequest {
	return plugintest.ConditionRequest{Task: plugin.TaskSnapshot{ID: testTaskID}}.WithJSONConfig(cfg)
}

func actionReqWithConfig(cfg any) plugintest.ActionRequest {
	return plugintest.ActionRequest{Task: plugin.TaskSnapshot{ID: testTaskID}}.WithJSONConfig(cfg)
}

func actionReqWithConfigAndKey(cfg any, key string) plugintest.ActionRequest {
	return plugintest.ActionRequest{Task: plugin.TaskSnapshot{ID: testTaskID}, IdempotencyKey: key}.WithJSONConfig(cfg)
}

// ── Condition: total_minutes_exceeds ──────────────────────────────────────────

func TestConditionTotalMinutesExceeds_InvalidConfig(t *testing.T) {
	tc := setupPlugin(t)
	result := tc.EvaluateCondition(automationConditionTotalMinutesExceeds, conditionReqWithConfig(map[string]any{}))
	if result.Matched {
		t.Fatal("expected Matched=false for missing threshold_minutes")
	}
}

func TestConditionTotalMinutesExceeds_NoLogsDoesNotMatch(t *testing.T) {
	tc := setupPlugin(t)
	cfg := map[string]any{"threshold_minutes": 60}
	result := tc.EvaluateCondition(automationConditionTotalMinutesExceeds, conditionReqWithConfig(cfg))
	if result.Matched {
		t.Fatal("expected Matched=false when no time has been logged")
	}
}

func TestConditionTotalMinutesExceeds_SumsAcrossEntries(t *testing.T) {
	tc := setupPlugin(t)
	tc.DB.SeedRows("task_time_logs",
		[]string{"id", "task_id", "member_id", "spent_date", "minutes_spent", "note", "created_by", "created_at", "updated_at"},
		[][]any{
			{"log-1", testTaskID, testMemberID, "2026-07-01", 40, "", testMemberID, "t", "t"},
			{"log-2", testTaskID, testMember2ID, "2026-07-02", 30, "", testMember2ID, "t", "t"},
		})

	cfg := map[string]any{"threshold_minutes": 60}
	result := tc.EvaluateCondition(automationConditionTotalMinutesExceeds, conditionReqWithConfig(cfg))
	if !result.Matched {
		t.Fatal("expected Matched=true: 40+30=70 > 60")
	}

	cfg2 := map[string]any{"threshold_minutes": 100}
	result2 := tc.EvaluateCondition(automationConditionTotalMinutesExceeds, conditionReqWithConfig(cfg2))
	if result2.Matched {
		t.Fatal("expected Matched=false: 40+30=70 is not > 100")
	}
}

func TestConditionTotalMinutesExceeds_FiltersByMember(t *testing.T) {
	tc := setupPlugin(t)
	tc.DB.SeedRows("task_time_logs",
		[]string{"id", "task_id", "member_id", "spent_date", "minutes_spent", "note", "created_by", "created_at", "updated_at"},
		[][]any{
			{"log-1", testTaskID, testMemberID, "2026-07-01", 40, "", testMemberID, "t", "t"},
			{"log-2", testTaskID, testMember2ID, "2026-07-02", 100, "", testMember2ID, "t", "t"},
		})

	cfg := map[string]any{"threshold_minutes": 60, "member_id": testMemberID}
	result := tc.EvaluateCondition(automationConditionTotalMinutesExceeds, conditionReqWithConfig(cfg))
	if result.Matched {
		t.Fatal("expected Matched=false: testMemberID alone only logged 40 minutes")
	}
}

// ── Action: log_time ───────────────────────────────────────────────────────────

func TestActionLogTime_MissingMemberID(t *testing.T) {
	tc := setupPlugin(t)
	result := tc.RunAction(automationActionLogTime, actionReqWithConfig(map[string]any{
		"spent_date": "2026-07-30", "minutes_spent": 30,
	}))
	if result.Applied {
		t.Fatal("expected Applied=false for missing member_id")
	}
}

func TestActionLogTime_MissingMinutes(t *testing.T) {
	tc := setupPlugin(t)
	result := tc.RunAction(automationActionLogTime, actionReqWithConfig(map[string]any{
		"member_id": testMemberID, "spent_date": "2026-07-30",
	}))
	if result.Applied {
		t.Fatal("expected Applied=false for missing/zero minutes_spent")
	}
}

func TestActionLogTime_Succeeds(t *testing.T) {
	tc := setupPlugin(t)
	cfg := map[string]any{"member_id": testMemberID, "spent_date": "2026-07-30", "minutes_spent": 45, "note": "auto-logged"}
	result := tc.RunAction(automationActionLogTime, actionReqWithConfig(cfg))
	if !result.Applied {
		t.Fatalf("expected Applied=true, got error: %s", result.Error)
	}

	rows := tc.DB.AllRows("task_time_logs")
	if len(rows) != 1 {
		t.Fatalf("expected 1 row inserted, got %d", len(rows))
	}
}

func TestActionLogTime_IdempotentOnRetry(t *testing.T) {
	tc := setupPlugin(t)
	cfg := map[string]any{"member_id": testMemberID, "spent_date": "2026-07-30", "minutes_spent": 45}
	req := actionReqWithConfigAndKey(cfg, "run-1-node-2")

	first := tc.RunAction(automationActionLogTime, req)
	if !first.Applied {
		t.Fatalf("expected first run Applied=true, got error: %s", first.Error)
	}

	second := tc.RunAction(automationActionLogTime, req)
	if second.Applied {
		t.Fatal("expected retried run with the same idempotency key to be a no-op (Applied=false)")
	}

	rows := tc.DB.AllRows("task_time_logs")
	if len(rows) != 1 {
		t.Fatalf("expected exactly 1 row after retry, got %d (time was double-logged)", len(rows))
	}
}
