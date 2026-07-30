package main

import (
	"encoding/json"
	"strings"

	plugin "github.com/Paca-AI/plugin-sdk-go"
)

// This file implements the automation-graph Condition and Action node
// types this plugin contributes, registered in Init via ctx.Condition and
// ctx.Action. The corresponding Trigger (time_logging.entry_created,
// emitted from createTimeLog in timelogs.go) needs no handler here — the
// engine matches triggers purely on event topic, per AutomationManifest's
// EventTopic field (see domain/plugin/entity.go in the core).
//
// Node types are namespaced under "com.paca.time-logging", matching the
// "automation" block in plugin.json.

const (
	// automationConditionTotalMinutesExceeds checks whether the sum of
	// minutes logged on a task (optionally filtered to a single member)
	// exceeds a configured threshold.
	automationConditionTotalMinutesExceeds = "com.paca.time-logging.total_minutes_exceeds"

	// automationActionLogTime creates a time-log entry on the task, the
	// same operation createTimeLog performs over HTTP.
	automationActionLogTime = "com.paca.time-logging.log_time"
)

// registerAutomationNodes wires this plugin's Condition/Action handlers
// into ctx. Called once from Init.
func (p *timeLoggingPlugin) registerAutomationNodes(ctx *plugin.Context) {
	ctx.Condition(automationConditionTotalMinutesExceeds, p.conditionTotalMinutesExceeds)
	ctx.Action(automationActionLogTime, p.actionLogTime)
}

// ─── Condition: com.paca.time-logging.total_minutes_exceeds ──────────────────

func (p *timeLoggingPlugin) conditionTotalMinutesExceeds(req *plugin.ConditionRequest) plugin.ConditionResult {
	var cfg struct {
		ThresholdMinutes int    `json:"threshold_minutes"`
		MemberID         string `json:"member_id"` // optional: filter to one member; empty = every member
	}
	if err := json.Unmarshal(req.Config, &cfg); err != nil || cfg.ThresholdMinutes <= 0 {
		p.log.Error("time-logging: total_minutes_exceeds condition: invalid config")
		return plugin.ConditionResult{Matched: false}
	}

	// Summed in Go rather than via SQL SUM/COALESCE: functionally identical
	// against the real Postgres backend, but also portable to the
	// plugintest in-memory DB used in this plugin's own test suite, which
	// only supports simple column-projection + WHERE matching (see
	// plugin-sdk-go/plugintest/backends.go), not aggregate functions.
	var (
		result *plugin.DBQueryResult
		err    error
	)
	if cfg.MemberID != "" {
		result, err = p.db.Query(
			`SELECT minutes_spent FROM task_time_logs WHERE task_id = $1 AND member_id = $2`,
			req.Task.ID, cfg.MemberID,
		)
	} else {
		result, err = p.db.Query(
			`SELECT minutes_spent FROM task_time_logs WHERE task_id = $1`,
			req.Task.ID,
		)
	}
	if err != nil {
		p.log.Error("time-logging: total_minutes_exceeds condition: query failed: " + err.Error())
		return plugin.ConditionResult{Matched: false}
	}

	total := 0
	for _, row := range result.Rows {
		total += newRowScanner(result.Columns, row).intVal("minutes_spent")
	}
	return plugin.ConditionResult{Matched: total > cfg.ThresholdMinutes}
}

// ─── Action: com.paca.time-logging.log_time ───────────────────────────────────

func (p *timeLoggingPlugin) actionLogTime(req *plugin.ActionRequest) plugin.ActionResult {
	var cfg struct {
		MemberID     string `json:"member_id"`
		SpentDate    string `json:"spent_date"`
		MinutesSpent int    `json:"minutes_spent"`
		Note         string `json:"note"`
	}
	if err := json.Unmarshal(req.Config, &cfg); err != nil {
		return plugin.ActionResult{Applied: false, Error: "invalid config"}
	}
	if cfg.MemberID == "" {
		return plugin.ActionResult{Applied: false, Error: "config.member_id is required"}
	}
	if cfg.SpentDate == "" {
		return plugin.ActionResult{Applied: false, Error: "config.spent_date is required"}
	}
	if cfg.MinutesSpent <= 0 {
		return plugin.ActionResult{Applied: false, Error: "config.minutes_spent must be greater than zero"}
	}

	// Idempotency: a plugin action can be retried by the automation
	// engine, so treat a prior run having already logged this exact
	// idempotency key as an already-applied no-op rather than
	// double-logging time. The key is stored in the note's trailing
	// metadata since the schema has no dedicated column for it. Fetched
	// and scanned in Go (rather than via a SQL LIKE) for the same
	// portability reason as the condition's aggregation above — the
	// plugintest in-memory DB's WHERE parser only supports "=" and
	// IS [NOT] NULL, not LIKE.
	if req.IdempotencyKey != "" {
		marker := "[automation:" + req.IdempotencyKey + "]"
		existing, err := p.db.Query(`SELECT note FROM task_time_logs WHERE task_id = $1`, req.Task.ID)
		if err == nil {
			for _, row := range existing.Rows {
				if strings.Contains(newRowScanner(existing.Columns, row).str("note"), marker) {
					return plugin.ActionResult{Applied: false}
				}
			}
		}
	}

	note := cfg.Note
	if req.IdempotencyKey != "" {
		if note != "" {
			note += " "
		}
		note += "[automation:" + req.IdempotencyKey + "]"
	}

	now := nowStr()
	if _, err := p.db.Exec(
		`INSERT INTO task_time_logs (task_id, member_id, spent_date, minutes_spent, note, created_by, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		req.Task.ID, cfg.MemberID, cfg.SpentDate, cfg.MinutesSpent, note, cfg.MemberID, now, now,
	); err != nil {
		return plugin.ActionResult{Applied: false, Error: "insert time log: " + err.Error()}
	}
	return plugin.ActionResult{Applied: true}
}
