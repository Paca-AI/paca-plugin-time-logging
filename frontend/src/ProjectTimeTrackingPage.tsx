import { PluginApiClient, PluginQueryClientProvider } from "@paca-ai/plugin-sdk-react";
import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Check,
  ChevronLeft,
  ChevronRight,
  Clock,
  Pencil,
  Trash2,
  X,
} from "lucide-react";
import { useMemo, useState } from "react";
import { PLUGIN_ID } from "./constants";
import {
  canManageTimeLog,
  type DatePreset,
  formatDate,
  formatMinutes,
  isoDaysAgo,
  isoFirstOfMonth,
  PAGE_SIZES,
  parseDurationToMinutes,
  presetLabel,
  todayDateString,
  useTimeLogViewer,
} from "./shared";
import type { TimeLog, TimeLogPage } from "./types";

// ── Props ─────────────────────────────────────────────────────────────────────

interface ProjectTimeTrackingPageProps {
  projectId: string;
}

/**
 * ProjectTimeTrackingPage — the `project.page` entry component exposed by the
 * time-logging plugin. This is the routed, full-page "total logged time of
 * this project" view, reached via a dedicated sidebar nav item. Shows every
 * logged time entry across every task in the project, filterable by member
 * and date range with inline edit/delete for individual entries. Filtering,
 * pagination, and the total-minutes sum are all computed by the backend
 * (GET /projects/:projectId/time-logs) — this component only renders
 * whatever page the API returns.
 */
export default function ProjectTimeTrackingPage(
  props: ProjectTimeTrackingPageProps,
) {
  return (
    <PluginQueryClientProvider>
      <ProjectTimeTrackingPageInner projectId={props.projectId} />
    </PluginQueryClientProvider>
  );
}

function ProjectTimeTrackingPageInner({ projectId }: { projectId: string }) {
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
  const viewer = useTimeLogViewer(api, projectId);

  // ── Filters ────────────────────────────────────────────────────────────────

  const [memberFilter, setMemberFilterState] = useState("");
  const [dateFrom, setDateFromState] = useState("");
  const [dateTo, setDateToState] = useState("");
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState<number>(PAGE_SIZES[0]);

  const setMemberFilter = (id: string) => {
    setMemberFilterState(id);
    setPage(1);
  };
  const setDateFrom = (v: string) => {
    setDateFromState(v);
    setPage(1);
  };
  const setDateTo = (v: string) => {
    setDateToState(v);
    setPage(1);
  };
  const applyPreset = (preset: DatePreset) => {
    if (preset === "all") {
      setDateFrom("");
      setDateTo("");
      return;
    }
    if (preset === "today") {
      const t = todayDateString();
      setDateFrom(t);
      setDateTo(t);
      return;
    }
    if (preset === "week") {
      setDateFrom(isoDaysAgo(6));
      setDateTo(todayDateString());
      return;
    }
    setDateFrom(isoFirstOfMonth());
    setDateTo(todayDateString());
  };
  const clearFilters = () => {
    setMemberFilterState("");
    setDateFromState("");
    setDateToState("");
    setPage(1);
  };
  const hasActiveFilters = memberFilter !== "" || dateFrom !== "" || dateTo !== "";

  // ── Queries ────────────────────────────────────────────────────────────────

  const logsPath = useMemo(() => {
    const params = new URLSearchParams();
    if (memberFilter) params.set("member_id", memberFilter);
    if (dateFrom) params.set("date_from", dateFrom);
    if (dateTo) params.set("date_to", dateTo);
    params.set("page", String(page));
    params.set("page_size", String(pageSize));
    return `/projects/${projectId}/time-logs?${params.toString()}`;
  }, [projectId, memberFilter, dateFrom, dateTo, page, pageSize]);

  const { data: logPage, isLoading } = useQuery<TimeLogPage>({
    queryKey: ["plugin", PLUGIN_ID, "time-logs", projectId, memberFilter, dateFrom, dateTo, page, pageSize],
    queryFn: () => api.pluginGet<TimeLogPage>(PLUGIN_ID, logsPath),
    placeholderData: keepPreviousData,
  });

  const logs = logPage?.logs ?? [];
  const total = logPage?.total ?? 0;
  const totalMinutes = logPage?.total_minutes ?? 0;
  const currentPage = logPage?.page ?? page;
  const currentPageSize = logPage?.page_size ?? pageSize;
  const totalPages = Math.max(1, Math.ceil(total / currentPageSize));
  const pageStart = total === 0 ? 0 : (currentPage - 1) * currentPageSize;

  const { data: members = [] } = useQuery({
    queryKey: ["plugin", PLUGIN_ID, "core", "members", projectId],
    queryFn: () => api.listMembers(),
  });

  const { data: tasks = [] } = useQuery({
    queryKey: ["plugin", PLUGIN_ID, "core", "tasks", projectId],
    queryFn: () => api.listTasks(),
  });

  const memberName = (memberId: string) =>
    members.find((m) => m.id === memberId)?.full_name ?? memberId;
  const taskTitle = (taskId: string) =>
    tasks.find((t) => t.id === taskId)?.title ?? taskId;

  // ── Pagination controls ────────────────────────────────────────────────────

  const goPrev = () => setPage(Math.max(1, currentPage - 1));
  const goNext = () => setPage(Math.min(totalPages, currentPage + 1));
  const handlePageSizeChange = (n: number) => {
    setPageSize(n);
    setPage(1);
  };

  // ── Inline edit / delete ──────────────────────────────────────────────────

  const invalidateLogs = () => {
    qc.invalidateQueries({ queryKey: ["plugin", PLUGIN_ID, "time-logs", projectId] });
    qc.invalidateQueries({ queryKey: ["plugin", PLUGIN_ID, "time-logs-summary", projectId] });
  };

  const [editingId, setEditingId] = useState<string | null>(null);
  const [editDate, setEditDate] = useState("");
  const [editDuration, setEditDuration] = useState("");
  const [editNote, setEditNote] = useState("");
  const [editError, setEditError] = useState<string | null>(null);

  const updateLog = useMutation({
    mutationFn: (vars: {
      taskId: string;
      logId: string;
      spent_date: string;
      minutes_spent: number;
      note: string;
    }) =>
      api.pluginPatch<TimeLog>(
        PLUGIN_ID,
        `/projects/${projectId}/tasks/${vars.taskId}/time-logs/${vars.logId}`,
        {
          spent_date: vars.spent_date,
          minutes_spent: vars.minutes_spent,
          note: vars.note,
        },
      ),
    onSuccess: () => {
      invalidateLogs();
      setEditingId(null);
      setEditError(null);
    },
    onError: (err: unknown) => {
      setEditError(err instanceof Error ? err.message : "Failed to update time log.");
    },
  });

  const [deleteError, setDeleteError] = useState<string | null>(null);

  const deleteLog = useMutation({
    mutationFn: (vars: { taskId: string; logId: string }) =>
      api.pluginDelete(PLUGIN_ID, `/projects/${projectId}/tasks/${vars.taskId}/time-logs/${vars.logId}`),
    onSuccess: () => {
      invalidateLogs();
      setDeleteError(null);
    },
    onError: (err: unknown) => {
      setDeleteError(err instanceof Error ? err.message : "Failed to delete time log.");
    },
  });

  const startEdit = (log: TimeLog) => {
    setEditingId(log.id);
    setEditDate(log.spent_date.split("T")[0]);
    setEditDuration(formatMinutes(log.minutes_spent));
    setEditNote(log.note);
    setEditError(null);
  };
  const cancelEdit = () => {
    setEditingId(null);
    setEditError(null);
  };
  const saveEdit = (log: TimeLog) => {
    const minutes = parseDurationToMinutes(editDuration);
    if (!minutes || minutes <= 0) {
      setEditError('Enter a duration like "1h30m", "45m", or "90" (minutes).');
      return;
    }
    if (!editDate) {
      setEditError("Pick a date.");
      return;
    }
    updateLog.mutate({
      taskId: log.task_id,
      logId: log.id,
      spent_date: editDate,
      minutes_spent: minutes,
      note: editNote,
    });
  };

  // ── Render ─────────────────────────────────────────────────────────────────

  return (
    <div className="flex flex-col gap-6 p-6 max-w-5xl w-full mx-auto">
      <div>
        <div className="flex items-center gap-2">
          <Clock className="size-5 text-primary" />
          <h1 className="text-xl font-semibold">Time Tracking</h1>
        </div>
        <p className="mt-1 text-sm text-muted-foreground">
          Track, filter, and manage logged time for this project.
        </p>
      </div>

      <div className="space-y-3">
        <h3 className="text-xs font-semibold uppercase tracking-[0.08em] text-muted-foreground/70 flex items-center gap-2">
          <span>All logged time entries</span>
          <div className="flex-1 h-px bg-linear-to-r from-border/40 to-transparent" />
        </h3>

        {(hasActiveFilters || total > 0) && (
          <div className="flex flex-wrap items-end gap-3 rounded-xl border border-border/25 bg-card/30 p-3">
            <div className="flex flex-col gap-1">
              <label
                htmlFor="time-tracking-member-filter"
                className="text-xs font-semibold uppercase tracking-wide text-muted-foreground/60"
              >
                Member
              </label>
              <select
                id="time-tracking-member-filter"
                value={memberFilter}
                onChange={(e) => setMemberFilter(e.target.value)}
                className="min-w-[9rem] rounded-lg bg-muted/40 px-2.5 py-1.5 text-xs"
              >
                <option value="">All members</option>
                {members.map((m) => (
                  <option key={m.id} value={m.id}>
                    {m.full_name}
                  </option>
                ))}
              </select>
            </div>

            <div className="flex flex-col gap-1">
              <label
                htmlFor="time-tracking-date-from"
                className="text-xs font-semibold uppercase tracking-wide text-muted-foreground/60"
              >
                From
              </label>
              <input
                id="time-tracking-date-from"
                type="date"
                value={dateFrom}
                onChange={(e) => setDateFrom(e.target.value)}
                className="rounded-lg bg-muted/40 px-2.5 py-1.5 text-xs"
              />
            </div>
            <div className="flex flex-col gap-1">
              <label
                htmlFor="time-tracking-date-to"
                className="text-xs font-semibold uppercase tracking-wide text-muted-foreground/60"
              >
                To
              </label>
              <input
                id="time-tracking-date-to"
                type="date"
                value={dateTo}
                onChange={(e) => setDateTo(e.target.value)}
                className="rounded-lg bg-muted/40 px-2.5 py-1.5 text-xs"
              />
            </div>

            <div className="flex items-center gap-0.5">
              {(["today", "week", "month", "all"] as const).map((preset) => (
                <button
                  key={preset}
                  type="button"
                  onClick={() => applyPreset(preset)}
                  className="rounded-lg px-2 py-1.5 text-xs font-medium text-muted-foreground/70 transition-colors hover:bg-muted/40 hover:text-foreground"
                >
                  {presetLabel(preset)}
                </button>
              ))}
            </div>

            {hasActiveFilters && (
              <button
                type="button"
                onClick={clearFilters}
                className="flex items-center gap-1 rounded-lg px-2 py-1.5 text-xs font-medium text-muted-foreground/60 transition-colors hover:text-destructive"
              >
                <X className="size-3" />
                Clear filters
              </button>
            )}

            <div className="ml-auto text-xs text-muted-foreground/60">
              {total} {total === 1 ? "entry" : "entries"} · {formatMinutes(totalMinutes)}
            </div>
          </div>
        )}

        {isLoading ? (
          <TableSkeleton />
        ) : !hasActiveFilters && total === 0 ? (
          <EmptyRow text="No time logs recorded for this project yet" />
        ) : total === 0 ? (
          <div className="flex flex-col items-center gap-2 px-1 py-8 text-center text-muted-foreground/50">
            <Clock className="size-5 opacity-60" />
            <p className="text-sm italic">No entries match your filters.</p>
            <button
              type="button"
              onClick={clearFilters}
              className="text-xs font-semibold text-primary hover:underline"
            >
              Clear filters
            </button>
          </div>
        ) : (
          <>
            <div className="overflow-hidden rounded-lg border border-border/20">
              <div className="overflow-x-auto">
                <table className="w-full text-xs">
                  <thead>
                    <tr className="bg-card/40 text-muted-foreground/70">
                      <th className="px-3 py-2 text-left font-semibold">Date</th>
                      <th className="px-3 py-2 text-left font-semibold">Member</th>
                      <th className="px-3 py-2 text-left font-semibold">Task</th>
                      <th className="px-3 py-2 text-left font-semibold">Note</th>
                      <th className="px-3 py-2 text-right font-semibold">Duration</th>
                      <th className="w-16 px-3 py-2 text-right font-semibold">
                        <span className="sr-only">Actions</span>
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {logs.map((log) => {
                      const isDeleting =
                        deleteLog.isPending && deleteLog.variables?.logId === log.id;

                      if (editingId === log.id) {
                        return (
                          <tr key={log.id} className="border-t border-border/10 bg-muted/20">
                            <td className="px-3 py-2">
                              <input
                                type="date"
                                value={editDate}
                                onChange={(e) => setEditDate(e.target.value)}
                                className="w-full rounded-md bg-muted/40 px-2 py-1 text-xs"
                              />
                            </td>
                            <td className="px-3 py-2 truncate text-muted-foreground/70">
                              {memberName(log.member_id)}
                            </td>
                            <td className="max-w-48 truncate px-3 py-2 text-muted-foreground/70">
                              {taskTitle(log.task_id)}
                            </td>
                            <td className="px-3 py-2">
                              <input
                                type="text"
                                value={editNote}
                                onChange={(e) => setEditNote(e.target.value)}
                                onKeyDown={(e) => {
                                  if (e.key === "Enter") saveEdit(log);
                                  if (e.key === "Escape") cancelEdit();
                                }}
                                placeholder="Note"
                                className="w-full rounded-md bg-muted/40 px-2 py-1 text-xs"
                              />
                            </td>
                            <td className="px-3 py-2 text-right">
                              <input
                                type="text"
                                value={editDuration}
                                onChange={(e) => setEditDuration(e.target.value)}
                                onKeyDown={(e) => {
                                  if (e.key === "Enter") saveEdit(log);
                                  if (e.key === "Escape") cancelEdit();
                                }}
                                placeholder="1h30m"
                                className="w-20 rounded-md bg-muted/40 px-2 py-1 text-right text-xs"
                              />
                            </td>
                            <td className="px-3 py-2">
                              <div className="flex items-center justify-end gap-1">
                                <button
                                  type="button"
                                  onClick={() => saveEdit(log)}
                                  disabled={updateLog.isPending}
                                  aria-label="Save"
                                  className="text-muted-foreground/60 hover:text-primary disabled:opacity-40"
                                >
                                  <Check className="size-3.5" />
                                </button>
                                <button
                                  type="button"
                                  onClick={cancelEdit}
                                  aria-label="Cancel"
                                  className="text-muted-foreground/60 hover:text-destructive"
                                >
                                  <X className="size-3.5" />
                                </button>
                              </div>
                            </td>
                          </tr>
                        );
                      }

                      return (
                        <tr key={log.id} className="border-t border-border/10">
                          <td className="px-3 py-2 whitespace-nowrap">{formatDate(log.spent_date)}</td>
                          <td className="px-3 py-2 truncate">{memberName(log.member_id)}</td>
                          <td className="max-w-48 truncate px-3 py-2">{taskTitle(log.task_id)}</td>
                          <td
                            className="max-w-64 truncate px-3 py-2 text-muted-foreground/70"
                            title={log.note || undefined}
                          >
                            {log.note || "—"}
                          </td>
                          <td className="px-3 py-2 text-right font-semibold whitespace-nowrap">
                            {formatMinutes(log.minutes_spent)}
                          </td>
                          <td className="px-3 py-2">
                            {canManageTimeLog(viewer, log.member_id) && (
                              <div className="flex items-center justify-end gap-1.5">
                                <button
                                  type="button"
                                  onClick={() => startEdit(log)}
                                  aria-label="Edit time log"
                                  className="text-muted-foreground/50 hover:text-foreground"
                                >
                                  <Pencil className="size-3.5" />
                                </button>
                                <button
                                  type="button"
                                  onClick={() => deleteLog.mutate({ taskId: log.task_id, logId: log.id })}
                                  disabled={isDeleting}
                                  aria-label="Delete time log"
                                  className="text-muted-foreground/50 hover:text-destructive disabled:cursor-not-allowed disabled:opacity-40"
                                >
                                  <Trash2 className="size-3.5" />
                                </button>
                              </div>
                            )}
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>
            </div>

            {editError && <p className="text-xs text-destructive">{editError}</p>}
            {deleteError && <p className="text-xs text-destructive">{deleteError}</p>}

            <div className="flex flex-wrap items-center justify-between gap-3 text-xs text-muted-foreground/70">
              <div className="flex items-center gap-2">
                <span>Rows per page</span>
                <select
                  value={pageSize}
                  onChange={(e) => handlePageSizeChange(Number(e.target.value))}
                  className="rounded-lg bg-muted/40 px-2 py-1 text-xs"
                >
                  {PAGE_SIZES.map((n) => (
                    <option key={n} value={n}>
                      {n}
                    </option>
                  ))}
                </select>
              </div>
              <div className="flex items-center gap-3">
                <span>
                  {pageStart + 1}–{Math.min(pageStart + currentPageSize, total)} of {total}
                </span>
                <div className="flex items-center gap-1">
                  <button
                    type="button"
                    onClick={goPrev}
                    disabled={currentPage <= 1}
                    aria-label="Previous page"
                    className="rounded-md p-1 hover:bg-muted/40 disabled:opacity-30 disabled:hover:bg-transparent"
                  >
                    <ChevronLeft className="size-3.5" />
                  </button>
                  <span className="min-w-[3.5rem] text-center">
                    Page {currentPage} / {totalPages}
                  </span>
                  <button
                    type="button"
                    onClick={goNext}
                    disabled={currentPage >= totalPages}
                    aria-label="Next page"
                    className="rounded-md p-1 hover:bg-muted/40 disabled:opacity-30 disabled:hover:bg-transparent"
                  >
                    <ChevronRight className="size-3.5" />
                  </button>
                </div>
              </div>
            </div>
          </>
        )}
      </div>
    </div>
  );
}

// ── Small presentational helpers ─────────────────────────────────────────────

function EmptyRow({ text }: { text: string }) {
  return (
    <div className="flex items-center gap-3 px-1 py-3 text-muted-foreground/45">
      <Clock className="size-4 opacity-70" />
      <p className="text-sm italic">{text}</p>
    </div>
  );
}

function TableSkeleton() {
  return (
    <div className="overflow-hidden rounded-lg border border-border/20">
      <div className="animate-pulse divide-y divide-border/10">
        {Array.from({ length: 5 }).map((_, i) => (
          // biome-ignore lint/suspicious/noArrayIndexKey: static placeholder rows
          <div key={i} className="h-9 bg-card/20" />
        ))}
      </div>
    </div>
  );
}
