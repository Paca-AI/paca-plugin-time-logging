import { PluginApiClient, PluginQueryClientProvider } from "@paca-ai/plugin-sdk-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Clock, Plus, Trash2 } from "lucide-react";
import { useMemo, useState } from "react";
import { PLUGIN_ID } from "./constants";
import {
  canManageTimeLog,
  formatMinutes,
  parseDurationToMinutes,
  todayDateString,
  useTimeLogViewer,
} from "./shared";
import type { TimeLog } from "./types";

// ── Props ─────────────────────────────────────────────────────────────────────

interface TimeLogsSectionProps {
  projectId: string;
  taskId: string;
}

// ── Component ─────────────────────────────────────────────────────────────────

/**
 * TimeLogsSection — the task-detail entry component exposed by the
 * time-logging plugin. Lets project members add manual time-log entries
 * (date + duration + optional note) against the current task and shows the
 * running total for the task.
 *
 * The "log time" form and delete button both require `tasks.write`
 * (`viewer.can_write_tasks`), matching the routes' own gate. Delete is
 * further restricted to own entries, or any entry with
 * `time_logging.manage_all`, via `canManageTimeLog`; the backend re-checks
 * both on every write, so it's the actual authorization boundary, not this
 * component's rendering.
 */
export default function TimeLogsSection(props: TimeLogsSectionProps) {
  return (
    <PluginQueryClientProvider>
      <TimeLogsSectionInner {...props} />
    </PluginQueryClientProvider>
  );
}

function TimeLogsSectionInner({ projectId, taskId }: TimeLogsSectionProps) {
  const api = useMemo(
    () =>
      new PluginApiClient({
        baseUrl: `${window.location.origin}/api/v1`,
        projectId,
        fetch: (url, init) =>
          window.fetch(url, { ...init, credentials: "include" }),
      }),
    [projectId],
  );

  const qc = useQueryClient();
  const queryKey = ["plugin", PLUGIN_ID, "time-logs", projectId, taskId];
  const viewer = useTimeLogViewer(api, projectId);

  const [date, setDate] = useState(todayDateString());
  const [duration, setDuration] = useState("");
  const [note, setNote] = useState("");
  const [formError, setFormError] = useState<string | null>(null);
  const [deleteError, setDeleteError] = useState<string | null>(null);

  // ── Query ──────────────────────────────────────────────────────────────────

  const { data: logs = [], isLoading } = useQuery<TimeLog[]>({
    queryKey,
    queryFn: () =>
      api.pluginGet<TimeLog[]>(PLUGIN_ID, `/projects/${projectId}/tasks/${taskId}/time-logs`),
  });

  const totalMinutes = logs.reduce((sum, l) => sum + l.minutes_spent, 0);

  // ── Mutations ──────────────────────────────────────────────────────────────

  const invalidate = () => qc.invalidateQueries({ queryKey });

  const createLog = useMutation({
    mutationFn: (payload: { spent_date: string; minutes_spent: number; note: string }) =>
      api.pluginPost<TimeLog>(
        PLUGIN_ID,
        `/projects/${projectId}/tasks/${taskId}/time-logs`,
        payload,
      ),
    onSuccess: () => {
      invalidate();
      setDuration("");
      setNote("");
    },
  });

  const deleteLog = useMutation({
    mutationFn: (logId: string) =>
      api.pluginDelete(PLUGIN_ID, `/projects/${projectId}/tasks/${taskId}/time-logs/${logId}`),
    onSuccess: () => {
      invalidate();
      setDeleteError(null);
    },
    onError: (err: unknown) => {
      setDeleteError(err instanceof Error ? err.message : "Failed to delete time log.");
    },
  });

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    setFormError(null);
    const minutes = parseDurationToMinutes(duration);
    if (!minutes || minutes <= 0) {
      setFormError("Enter a duration like \"1h30m\", \"45m\", or \"90\" (minutes).");
      return;
    }
    if (!date) {
      setFormError("Pick a date.");
      return;
    }
    createLog.mutate({ spent_date: date, minutes_spent: minutes, note });
  };

  // ── Render ─────────────────────────────────────────────────────────────────

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between">
        <h3 className="text-xs font-semibold uppercase tracking-[0.08em] text-muted-foreground/70 flex items-center gap-2">
          <span>Time logged</span>
          <div className="flex-1 h-px bg-linear-to-r from-border/40 to-transparent" />
        </h3>
        <span className="text-xs font-semibold text-muted-foreground/70">
          {formatMinutes(totalMinutes)}
        </span>
      </div>

      {!!viewer?.can_write_tasks && (
        <form
          onSubmit={handleSubmit}
          className="flex flex-wrap items-center gap-2 rounded-xl border border-border/25 bg-card/50 p-3"
        >
          <input
            type="date"
            value={date}
            onChange={(e) => setDate(e.target.value)}
            className="rounded-lg bg-muted/40 px-2.5 py-1.5 text-xs"
          />
          <input
            type="text"
            placeholder="1h30m"
            value={duration}
            onChange={(e) => setDuration(e.target.value)}
            className="w-20 rounded-lg bg-muted/40 px-2.5 py-1.5 text-xs"
          />
          <input
            type="text"
            placeholder="Note (optional)"
            value={note}
            onChange={(e) => setNote(e.target.value)}
            className="min-w-40 flex-1 rounded-lg bg-muted/40 px-2.5 py-1.5 text-xs"
          />
          <button
            type="submit"
            disabled={createLog.isPending}
            className="flex items-center gap-1.5 rounded-lg bg-muted/40 text-muted-foreground/80 hover:bg-muted/60 hover:text-foreground px-2.5 py-1.5 text-xs font-semibold transition-all duration-150"
          >
            <Plus className="size-3" />
            Log time
          </button>
          {formError && (
            <p className="w-full text-xs text-destructive">{formError}</p>
          )}
        </form>
      )}

      {isLoading ? null : logs.length > 0 ? (
        <div className="space-y-1.5">
          {logs.map((l) => (
            <div
              key={l.id}
              className="flex items-center gap-3 rounded-lg border border-border/20 bg-card/40 px-3 py-2"
            >
              <Clock className="size-3.5 shrink-0 opacity-60" />
              <span className="text-xs font-semibold">{formatMinutes(l.minutes_spent)}</span>
              <span className="text-xs text-muted-foreground/70">{l.spent_date}</span>
              {l.note && (
                <span className="flex-1 truncate text-xs text-muted-foreground/60">{l.note}</span>
              )}
              {canManageTimeLog(viewer, l.member_id) && (
                <button
                  type="button"
                  onClick={() => deleteLog.mutate(l.id)}
                  className="ml-auto text-muted-foreground/50 hover:text-destructive"
                  aria-label="Delete time log"
                >
                  <Trash2 className="size-3.5" />
                </button>
              )}
            </div>
          ))}
        </div>
      ) : (
        <div className="flex items-center gap-3 px-1 py-3 text-muted-foreground/45">
          <Clock className="size-4 opacity-70" />
          <p className="text-sm italic">No time logged yet</p>
        </div>
      )}
      {deleteError && <p className="text-xs text-destructive">{deleteError}</p>}
    </div>
  );
}
