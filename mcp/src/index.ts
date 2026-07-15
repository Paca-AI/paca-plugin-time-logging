import {
	PluginAPIClient,
	type PluginMCPContext,
	type PluginMCPEntry,
	type Tool,
	errorResult,
	textResult,
} from "@paca-ai/plugin-sdk-mcp";

// ── Domain types ──────────────────────────────────────────────────────────────

interface TimeLog {
	id: string;
	task_id: string;
	member_id: string;
	spent_date: string;
	minutes_spent: number;
	note: string;
	created_at: string;
	updated_at: string;
}

interface MemberTotal {
	member_id: string;
	minutes_spent: number;
}

interface TimeLogPage {
	logs: TimeLog[];
	total: number;
	total_minutes: number;
	page: number;
	page_size: number;
}

// Matches the backend's maxPageSize cap (backend/timelogs.go). Requesting
// the max page size keeps this tool's "list everything" behavior working
// for the common case; very large projects are reported as truncated below
// rather than silently cut off.
const MAX_PAGE_SIZE = 100;

// ── Formatting helpers ────────────────────────────────────────────────────────

function formatMinutes(minutes: number): string {
	if (minutes <= 0) return "0m";
	const h = Math.floor(minutes / 60);
	const m = minutes % 60;
	if (h === 0) return `${m}m`;
	if (m === 0) return `${h}h`;
	return `${h}h ${m}m`;
}

function formatTimeLog(tl: TimeLog): string {
	const note = tl.note ? ` — ${tl.note}` : "";
	return `  ${tl.spent_date}: ${formatMinutes(tl.minutes_spent)} by ${tl.member_id} (ID: ${tl.id})${note}`;
}

// ── Tool definitions ──────────────────────────────────────────────────────────

const UUID_DESC =
	"The technical UUID of the %s (e.g., '550e8400-e29b-41d4-a716-446655440000').";

const tools: Tool[] = [
	{
		name: "time_logging_list_time_logs",
		description: "List all time-log entries recorded against a specific task.",
		inputSchema: {
			type: "object",
			properties: {
				projectId: {
					type: "string",
					description: UUID_DESC.replace("%s", "project") + " Use list_projects to get the project ID.",
				},
				taskId: {
					type: "string",
					description: UUID_DESC.replace("%s", "task") + " Use list_tasks to get the task ID.",
				},
			},
			required: ["projectId", "taskId"],
		},
	},
	{
		name: "time_logging_log_time",
		description:
			"Log time spent on a task. Records a manual time entry with a date, a duration in minutes, and an optional note.",
		inputSchema: {
			type: "object",
			properties: {
				projectId: {
					type: "string",
					description: UUID_DESC.replace("%s", "project") + " Use list_projects to get the project ID.",
				},
				taskId: {
					type: "string",
					description: UUID_DESC.replace("%s", "task") + " Use list_tasks to get the task ID.",
				},
				spentDate: {
					type: "string",
					description: "The date the time was spent, in YYYY-MM-DD format.",
				},
				minutesSpent: {
					type: "number",
					description: "Duration of the time entry, in minutes (must be greater than zero).",
				},
				note: {
					type: "string",
					description: "Optional note describing what the time was spent on.",
				},
				memberId: {
					type: "string",
					description:
						"UUID of the project member the time should be attributed to (optional; defaults to the caller).",
				},
			},
			required: ["projectId", "taskId", "spentDate", "minutesSpent"],
		},
	},
	{
		name: "time_logging_update_time_log",
		description: "Update an existing time-log entry's date, duration, or note.",
		inputSchema: {
			type: "object",
			properties: {
				projectId: {
					type: "string",
					description: UUID_DESC.replace("%s", "project") + " Use list_projects to get the project ID.",
				},
				taskId: {
					type: "string",
					description: UUID_DESC.replace("%s", "task") + " Use list_tasks to get the task ID.",
				},
				logId: {
					type: "string",
					description:
						UUID_DESC.replace("%s", "time log") + " Use time_logging_list_time_logs to get the log ID.",
				},
				spentDate: {
					type: "string",
					description: "New date for the entry, in YYYY-MM-DD format (optional).",
				},
				minutesSpent: {
					type: "number",
					description: "New duration in minutes (optional; must be greater than zero).",
				},
				note: {
					type: "string",
					description: "New note text (optional).",
				},
			},
			required: ["projectId", "taskId", "logId"],
		},
	},
	{
		name: "time_logging_delete_time_log",
		description: "Delete a time-log entry from a task.",
		inputSchema: {
			type: "object",
			properties: {
				projectId: {
					type: "string",
					description: UUID_DESC.replace("%s", "project") + " Use list_projects to get the project ID.",
				},
				taskId: {
					type: "string",
					description: UUID_DESC.replace("%s", "task") + " Use list_tasks to get the task ID.",
				},
				logId: {
					type: "string",
					description:
						UUID_DESC.replace("%s", "time log") + " Use time_logging_list_time_logs to get the log ID.",
				},
			},
			required: ["projectId", "taskId", "logId"],
		},
	},
	{
		name: "time_logging_project_summary",
		description:
			"Get total time logged per project member, across all tasks in a project.",
		inputSchema: {
			type: "object",
			properties: {
				projectId: {
					type: "string",
					description: UUID_DESC.replace("%s", "project") + " Use list_projects to get the project ID.",
				},
			},
			required: ["projectId"],
		},
	},
	{
		name: "time_logging_list_project_time_logs",
		description:
			"List every time-log entry across all tasks in a project, most recent first. Use this to see the full logged-time history for a project, rather than one task at a time.",
		inputSchema: {
			type: "object",
			properties: {
				projectId: {
					type: "string",
					description: UUID_DESC.replace("%s", "project") + " Use list_projects to get the project ID.",
				},
			},
			required: ["projectId"],
		},
	},
];

// ── Entry ─────────────────────────────────────────────────────────────────────

const entry: PluginMCPEntry = {
	tools,

	async handleToolCall(
		name: string,
		args: Record<string, unknown>,
		context: PluginMCPContext,
	) {
		const api = new PluginAPIClient(context);

		try {
			switch (name) {
				case "time_logging_list_time_logs": {
					const { projectId, taskId } = args as {
						projectId: string;
						taskId: string;
					};
					const logs = await api.pluginGet<TimeLog[]>(
						`projects/${projectId}/tasks/${taskId}/time-logs`,
					);
					if (logs.length === 0) {
						return textResult("No time logged for this task yet.");
					}
					const total = logs.reduce((sum, l) => sum + l.minutes_spent, 0);
					const formatted = logs.map(formatTimeLog).join("\n");
					return textResult(
						`Time logs (total ${formatMinutes(total)}):\n\n${formatted}`,
					);
				}

				case "time_logging_log_time": {
					const { projectId, taskId, spentDate, minutesSpent, note, memberId } =
						args as {
							projectId: string;
							taskId: string;
							spentDate: string;
							minutesSpent: number;
							note?: string;
							memberId?: string;
						};
					const body: Record<string, unknown> = {
						spent_date: spentDate,
						minutes_spent: minutesSpent,
					};
					if (note !== undefined) body.note = note;
					if (memberId !== undefined) body.member_id = memberId;
					const log = await api.pluginPost<TimeLog>(
						`projects/${projectId}/tasks/${taskId}/time-logs`,
						body,
					);
					return textResult(`Time logged:\n${formatTimeLog(log)}`);
				}

				case "time_logging_update_time_log": {
					const { projectId, taskId, logId, spentDate, minutesSpent, note } =
						args as {
							projectId: string;
							taskId: string;
							logId: string;
							spentDate?: string;
							minutesSpent?: number;
							note?: string;
						};
					const body: Record<string, unknown> = {};
					if (spentDate !== undefined) body.spent_date = spentDate;
					if (minutesSpent !== undefined) body.minutes_spent = minutesSpent;
					if (note !== undefined) body.note = note;
					const log = await api.pluginPatch<TimeLog>(
						`projects/${projectId}/tasks/${taskId}/time-logs/${logId}`,
						body,
					);
					return textResult(`Time log updated:\n${formatTimeLog(log)}`);
				}

				case "time_logging_delete_time_log": {
					const { projectId, taskId, logId } = args as {
						projectId: string;
						taskId: string;
						logId: string;
					};
					await api.pluginDelete(
						`projects/${projectId}/tasks/${taskId}/time-logs/${logId}`,
					);
					return textResult(`Time log ${logId} deleted successfully.`);
				}

				case "time_logging_project_summary": {
					const { projectId } = args as { projectId: string };
					const totals = await api.pluginGet<MemberTotal[]>(
						`projects/${projectId}/time-logs/summary`,
					);
					if (totals.length === 0) {
						return textResult("No time has been logged in this project yet.");
					}
					const formatted = totals
						.map((t) => `  ${t.member_id}: ${formatMinutes(t.minutes_spent)}`)
						.join("\n");
					return textResult(`Time logged by member:\n\n${formatted}`);
				}

				case "time_logging_list_project_time_logs": {
					const { projectId } = args as { projectId: string };
					const result = await api.pluginGet<TimeLogPage>(
						`projects/${projectId}/time-logs?page_size=${MAX_PAGE_SIZE}`,
					);
					if (result.logs.length === 0) {
						return textResult("No time logs recorded for this project yet.");
					}
					const formatted = result.logs.map(formatTimeLog).join("\n");
					const truncated =
						result.total > result.logs.length
							? ` (showing the most recent ${result.logs.length} of ${result.total} entries)`
							: "";
					return textResult(
						`All time logs for project (total ${formatMinutes(result.total_minutes)})${truncated}:\n\n${formatted}`,
					);
				}

				default:
					return errorResult(`Unknown tool: ${name}`);
			}
		} catch (err) {
			const message = err instanceof Error ? err.message : String(err);
			return errorResult(`Tool ${name} failed: ${message}`);
		}
	},
};

export default entry;
