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
  useTimeLogViewerAll,
} from "./shared";
import type {
  ProjectTotal,
  TimeLog,
  TimeLogAllPage,
  TimeLogPage,
  TimeLogWithContext,
  UserTotalAll,
} from "./types";

// ── Scope ─────────────────────────────────────────────────────────────────────

/**
 * TimeTrackingScope selects which time-tracking page this instance renders:
 * "project" for one project's logged time (filterable by member), or "admin"
 * for the cross-project view (filterable by project + user). See
 * TimeTrackingPage's doc comment for why one component covers both.
 */
export type TimeTrackingScope =
  | { kind: "project"; projectId: string }
  | { kind: "admin" };

interface TimeTrackingPageProps {
  scope: TimeTrackingScope;
}

// ── Normalized row shape ─────────────────────────────────────────────────────

interface DisplayRow {
  id: string;
  taskId: string;
  memberId: string;
  memberLabel: string;
  taskLabel: string;
  projectLabel?: string;
  spentDate: string;
  minutesSpent: number;
  note: string;
}

/**
 * TimeTrackingPage — the shared list/table view behind both the
 * `project.page` and `admin.page` entry components exposed by the
 * time-logging plugin (see the thin wrappers in ProjectTimeTrackingPage.tsx
 * and AdminTimeTrackingPage.tsx). One component covers both scopes because
 * they differ only in data source, filter dimensions, and the edit/delete
 * authorization rule — the table, pagination, filter-bar shell, and
 * inline-edit UX are otherwise identical.
 *
 * Project scope: lists one project's entries (GET .../time-logs), filterable
 * by member + date range; edit/delete gated per-row by canManageTimeLog (own
 * entry, or the project/global time_logging.manage_all permission), against
 * the project-scoped PATCH/DELETE .../tasks/:taskId/time-logs/:logId routes.
 *
 * Admin scope: lists every project's entries (GET /time-logs/all), filterable
 * by project + user + date range; edit/delete gated uniformly — not per-row —
 * by whether the caller holds the global time_logging.manage_all permission
 * (useTimeLogViewerAll), against the dedicated global-scope PATCH/DELETE
 * /time-logs/all/:logId routes. There's no per-row ownership check at this
 * scope: the caller may not even be a member of a given entry's project, so
 * the global grant is the entire authorization boundary, enforced by the
 * host route gate.
 */
export default function TimeTrackingPage({ scope }: TimeTrackingPageProps) {
  return (
    <PluginQueryClientProvider>
      <TimeTrackingPageInner scope={scope} />
    </PluginQueryClientProvider>
  );
}

function TimeTrackingPageInner({ scope }: TimeTrackingPageProps) {
  const isProject = scope.kind === "project";
  const projectId = scope.kind === "project" ? scope.projectId : undefined;

  const api = useMemo(
    () =>
      new PluginApiClient({
        baseUrl: `${window.location.origin}/api/v1`,
        projectId: projectId ?? "",
        fetch: (url, init) =>
          window.fetch(url, { ...init, credentials: "include" }),
      }),
    [projectId],
  );
  const qc = useQueryClient();

  const projectViewer = useTimeLogViewer(api, projectId ?? "", isProject);
  const adminViewer = useTimeLogViewerAll(api, !isProject);

  // ── Filters ────────────────────────────────────────────────────────────────

  const [memberFilter, setMemberFilterState] = useState("");
  const [projectFilter, setProjectFilterState] = useState("");
  const [userFilter, setUserFilterState] = useState("");
  const [dateFrom, setDateFromState] = useState("");
  const [dateTo, setDateToState] = useState("");
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState<number>(PAGE_SIZES[0]);

  const setMemberFilter = (id: string) => {
    setMemberFilterState(id);
    setPage(1);
  };
  const setProjectFilter = (id: string) => {
    setProjectFilterState(id);
    setPage(1);
  };
  const setUserFilter = (id: string) => {
    setUserFilterState(id);
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
    setProjectFilterState("");
    setUserFilterState("");
    setDateFromState("");
    setDateToState("");
    setPage(1);
  };
  const hasActiveFilters = isProject
    ? memberFilter !== "" || dateFrom !== "" || dateTo !== ""
    : projectFilter !== "" || userFilter !== "" || dateFrom !== "" || dateTo !== "";

  // ── Admin-only filter-dropdown totals ─────────────────────────────────────
  //
  // These double as the option lists for the Project/User filters below —
  // there is no global "list all projects"/"list all members" call available
  // to plugins (project_members are project-scoped). Each is computed against
  // the *other* active filters and refetches whenever those filters change so
  // the totals shown always match the current scope.

  const summaryAllPath = useMemo(() => {
    const params = new URLSearchParams();
    if (userFilter) params.set("user_id", userFilter);
    if (dateFrom) params.set("date_from", dateFrom);
    if (dateTo) params.set("date_to", dateTo);
    const qs = params.toString();
    return `/time-logs/summary-all${qs ? `?${qs}` : ""}`;
  }, [userFilter, dateFrom, dateTo]);

  const { data: totals = [] } = useQuery<ProjectTotal[]>({
    queryKey: ["plugin", PLUGIN_ID, "time-logs-summary-all", userFilter, dateFrom, dateTo],
    queryFn: () => api.pluginGet<ProjectTotal[]>(PLUGIN_ID, summaryAllPath),
    placeholderData: keepPreviousData,
    enabled: !isProject,
  });

  const usersAllPath = useMemo(() => {
    const params = new URLSearchParams();
    if (projectFilter) params.set("project_id", projectFilter);
    if (dateFrom) params.set("date_from", dateFrom);
    if (dateTo) params.set("date_to", dateTo);
    const qs = params.toString();
    return `/time-logs/users-all${qs ? `?${qs}` : ""}`;
  }, [projectFilter, dateFrom, dateTo]);

  const { data: userTotals = [] } = useQuery<UserTotalAll[]>({
    queryKey: ["plugin", PLUGIN_ID, "time-logs-users-all", projectFilter, dateFrom, dateTo],
    queryFn: () => api.pluginGet<UserTotalAll[]>(PLUGIN_ID, usersAllPath),
    placeholderData: keepPreviousData,
    enabled: !isProject,
  });

  // ── Project-only member/task name lookups ─────────────────────────────────
  //
  // Admin-scope rows already carry member_name/task_title/project_name
  // embedded by the backend (see TimeLogWithContext) — there's no single
  // project to resolve these against generically the way project scope does.

  const { data: members = [] } = useQuery({
    queryKey: ["plugin", PLUGIN_ID, "core", "members", projectId ?? ""],
    queryFn: () => api.listMembers(),
    enabled: isProject,
  });
  const { data: tasks = [] } = useQuery({
    queryKey: ["plugin", PLUGIN_ID, "core", "tasks", projectId ?? ""],
    queryFn: () => api.listTasks(),
    enabled: isProject,
  });
  const memberName = (memberId: string) =>
    members.find((m) => m.id === memberId)?.full_name ?? memberId;
  const taskTitle = (taskId: string) =>
    tasks.find((t) => t.id === taskId)?.title ?? taskId;

  // ── Main list query ────────────────────────────────────────────────────────

  const logsPath = useMemo(() => {
    const params = new URLSearchParams();
    if (isProject) {
      if (memberFilter) params.set("member_id", memberFilter);
    } else {
      if (projectFilter) params.set("project_id", projectFilter);
      if (userFilter) params.set("user_id", userFilter);
    }
    if (dateFrom) params.set("date_from", dateFrom);
    if (dateTo) params.set("date_to", dateTo);
    params.set("page", String(page));
    params.set("page_size", String(pageSize));
    return isProject
      ? `/projects/${projectId}/time-logs?${params.toString()}`
      : `/time-logs/all?${params.toString()}`;
  }, [isProject, projectId, memberFilter, projectFilter, userFilter, dateFrom, dateTo, page, pageSize]);

  const { data: logPage, isLoading } = useQuery<TimeLogPage | TimeLogAllPage>({
    queryKey: [
      "plugin",
      PLUGIN_ID,
      "time-logs-list",
      isProject ? projectId : "all",
      memberFilter,
      projectFilter,
      userFilter,
      dateFrom,
      dateTo,
      page,
      pageSize,
    ],
    queryFn: () => api.pluginGet(PLUGIN_ID, logsPath),
    placeholderData: keepPreviousData,
  });

  const rows: DisplayRow[] = useMemo(() => {
    const logs = logPage?.logs ?? [];
    if (isProject) {
      return (logs as TimeLog[]).map((l) => ({
        id: l.id,
        taskId: l.task_id,
        memberId: l.member_id,
        memberLabel: memberName(l.member_id),
        taskLabel: taskTitle(l.task_id),
        spentDate: l.spent_date,
        minutesSpent: l.minutes_spent,
        note: l.note,
      }));
    }
    return (logs as TimeLogWithContext[]).map((l) => ({
      id: l.id,
      taskId: l.task_id,
      memberId: l.member_id,
      memberLabel: l.member_name || l.member_id,
      taskLabel: l.task_title || l.task_id,
      projectLabel: l.project_name,
      spentDate: l.spent_date,
      minutesSpent: l.minutes_spent,
      note: l.note,
    }));
  }, [logPage, isProject, members, tasks]);

  const total = logPage?.total ?? 0;
  const totalMinutes = logPage?.total_minutes ?? 0;
  const currentPage = logPage?.page ?? page;
  const currentPageSize = logPage?.page_size ?? pageSize;
  const totalPages = Math.max(1, Math.ceil(total / currentPageSize));
  const pageStart = total === 0 ? 0 : (currentPage - 1) * currentPageSize;

  // ── Pagination controls ────────────────────────────────────────────────────

  const goPrev = () => setPage(Math.max(1, currentPage - 1));
  const goNext = () => setPage(Math.min(totalPages, currentPage + 1));
  const handlePageSizeChange = (n: number) => {
    setPageSize(n);
    setPage(1);
  };

  // ── Inline edit / delete ──────────────────────────────────────────────────

  const invalidateLogs = () => {
    qc.invalidateQueries({ queryKey: ["plugin", PLUGIN_ID, "time-logs-list", isProject ? projectId : "all"] });
    if (isProject) {
      qc.invalidateQueries({ queryKey: ["plugin", PLUGIN_ID, "time-logs-summary", projectId] });
    } else {
      qc.invalidateQueries({ queryKey: ["plugin", PLUGIN_ID, "time-logs-summary-all"] });
      qc.invalidateQueries({ queryKey: ["plugin", PLUGIN_ID, "time-logs-users-all"] });
    }
  };

  const [editingId, setEditingId] = useState<string | null>(null);
  const [editDate, setEditDate] = useState("");
  const [editDuration, setEditDuration] = useState("");
  const [editNote, setEditNote] = useState("");
  const [editError, setEditError] = useState<string | null>(null);

  const mutationPath = (kind: "patch" | "delete", taskId: string, logId: string) =>
    isProject
      ? `/projects/${projectId}/tasks/${taskId}/time-logs/${logId}`
      : `/time-logs/all/${logId}`;

  const updateLog = useMutation({
    mutationFn: (vars: {
      taskId: string;
      logId: string;
      spent_date: string;
      minutes_spent: number;
      note: string;
    }) =>
      api.pluginPatch<TimeLog>(PLUGIN_ID, mutationPath("patch", vars.taskId, vars.logId), {
        spent_date: vars.spent_date,
        minutes_spent: vars.minutes_spent,
        note: vars.note,
      }),
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
      api.pluginDelete(PLUGIN_ID, mutationPath("delete", vars.taskId, vars.logId)),
    onSuccess: () => {
      invalidateLogs();
      setDeleteError(null);
    },
    onError: (err: unknown) => {
      setDeleteError(err instanceof Error ? err.message : "Failed to delete time log.");
    },
  });

  const canManageRow = (row: DisplayRow): boolean =>
    isProject ? canManageTimeLog(projectViewer, row.memberId) : !!adminViewer?.can_manage_all;

  const startEdit = (row: DisplayRow) => {
    setEditingId(row.id);
    setEditDate(row.spentDate.split("T")[0]);
    setEditDuration(formatMinutes(row.minutesSpent));
    setEditNote(row.note);
    setEditError(null);
  };
  const cancelEdit = () => {
    setEditingId(null);
    setEditError(null);
  };
  const saveEdit = (row: DisplayRow) => {
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
      taskId: row.taskId,
      logId: row.id,
      spent_date: editDate,
      minutes_spent: minutes,
      note: editNote,
    });
  };

  // ── Copy ───────────────────────────────────────────────────────────────────

  const title = isProject ? "Time Tracking" : "Time Tracking — All Projects";
  const description = isProject
    ? "Track, filter, and manage logged time for this project."
    : "Browse and filter logged time across every project.";
  const emptyText = isProject
    ? "No time logs recorded for this project yet"
    : "No time logged in any project yet";

  // ── Render ─────────────────────────────────────────────────────────────────

  return (
    <div className="flex flex-col gap-6 p-6 max-w-5xl w-full mx-auto">
      <div>
        <div className="flex items-center gap-2">
          <Clock className="size-5 text-primary" />
          <h1 className="text-xl font-semibold">{title}</h1>
        </div>
        <p className="mt-1 text-sm text-muted-foreground">{description}</p>
      </div>

      <div className="space-y-3">
        <h3 className="text-xs font-semibold uppercase tracking-[0.08em] text-muted-foreground/70 flex items-center gap-2">
          <span>All logged time entries</span>
          <div className="flex-1 h-px bg-linear-to-r from-border/40 to-transparent" />
        </h3>

        {(hasActiveFilters || total > 0) && (
          <div className="flex flex-wrap items-end gap-3 rounded-xl border border-border/25 bg-card/30 p-3">
            {isProject ? (
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
                  className="min-w-36 rounded-lg bg-muted/40 px-2.5 py-1.5 text-xs"
                >
                  <option value="">All members</option>
                  {members.map((m) => (
                    <option key={m.id} value={m.id}>
                      {m.full_name}
                    </option>
                  ))}
                </select>
              </div>
            ) : (
              <>
                <div className="flex flex-col gap-1">
                  <label
                    htmlFor="time-tracking-project-filter"
                    className="text-xs font-semibold uppercase tracking-wide text-muted-foreground/60"
                  >
                    Project
                  </label>
                  <select
                    id="time-tracking-project-filter"
                    value={projectFilter}
                    onChange={(e) => setProjectFilter(e.target.value)}
                    className="min-w-36 rounded-lg bg-muted/40 px-2.5 py-1.5 text-xs"
                  >
                    <option value="">All projects</option>
                    {totals.map((t) => (
                      <option key={t.project_id} value={t.project_id}>
                        {t.project_name}
                      </option>
                    ))}
                  </select>
                </div>

                <div className="flex flex-col gap-1">
                  <label
                    htmlFor="time-tracking-user-filter"
                    className="text-xs font-semibold uppercase tracking-wide text-muted-foreground/60"
                  >
                    User
                  </label>
                  <select
                    id="time-tracking-user-filter"
                    value={userFilter}
                    onChange={(e) => setUserFilter(e.target.value)}
                    className="min-w-36 rounded-lg bg-muted/40 px-2.5 py-1.5 text-xs"
                  >
                    <option value="">All users</option>
                    {userTotals.map((u) => (
                      <option key={u.user_id} value={u.user_id}>
                        {u.user_name || u.user_id}
                      </option>
                    ))}
                  </select>
                </div>
              </>
            )}

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
          <EmptyRow text={emptyText} />
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
                      {!isProject && (
                        <th className="px-3 py-2 text-left font-semibold">Project</th>
                      )}
                      <th className="px-3 py-2 text-left font-semibold">
                        {isProject ? "Member" : "User"}
                      </th>
                      <th className="px-3 py-2 text-left font-semibold">Task</th>
                      <th className="px-3 py-2 text-left font-semibold">Note</th>
                      <th className="px-3 py-2 text-right font-semibold">Duration</th>
                      <th className="w-16 px-3 py-2 text-right font-semibold">
                        <span className="sr-only">Actions</span>
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {rows.map((row) => {
                      const isDeleting =
                        deleteLog.isPending && deleteLog.variables?.logId === row.id;

                      if (editingId === row.id) {
                        return (
                          <tr key={row.id} className="border-t border-border/10 bg-muted/20">
                            <td className="px-3 py-2">
                              <input
                                type="date"
                                value={editDate}
                                onChange={(e) => setEditDate(e.target.value)}
                                className="w-full rounded-md bg-muted/40 px-2 py-1 text-xs"
                              />
                            </td>
                            {!isProject && (
                              <td className="max-w-40 truncate px-3 py-2 text-muted-foreground/70">
                                {row.projectLabel}
                              </td>
                            )}
                            <td className="px-3 py-2 truncate text-muted-foreground/70">
                              {row.memberLabel}
                            </td>
                            <td className="max-w-48 truncate px-3 py-2 text-muted-foreground/70">
                              {row.taskLabel}
                            </td>
                            <td className="px-3 py-2">
                              <input
                                type="text"
                                value={editNote}
                                onChange={(e) => setEditNote(e.target.value)}
                                onKeyDown={(e) => {
                                  if (e.key === "Enter") saveEdit(row);
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
                                  if (e.key === "Enter") saveEdit(row);
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
                                  onClick={() => saveEdit(row)}
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
                        <tr key={row.id} className="border-t border-border/10">
                          <td className="px-3 py-2 whitespace-nowrap">{formatDate(row.spentDate)}</td>
                          {!isProject && (
                            <td className="max-w-40 truncate px-3 py-2">{row.projectLabel}</td>
                          )}
                          <td className="px-3 py-2 truncate">{row.memberLabel}</td>
                          <td className="max-w-48 truncate px-3 py-2">{row.taskLabel}</td>
                          <td
                            className="max-w-64 truncate px-3 py-2 text-muted-foreground/70"
                            title={row.note || undefined}
                          >
                            {row.note || "—"}
                          </td>
                          <td className="px-3 py-2 text-right font-semibold whitespace-nowrap">
                            {formatMinutes(row.minutesSpent)}
                          </td>
                          <td className="px-3 py-2">
                            {canManageRow(row) && (
                              <div className="flex items-center justify-end gap-1.5">
                                <button
                                  type="button"
                                  onClick={() => startEdit(row)}
                                  aria-label="Edit time log"
                                  className="text-muted-foreground/50 hover:text-foreground"
                                >
                                  <Pencil className="size-3.5" />
                                </button>
                                <button
                                  type="button"
                                  onClick={() => deleteLog.mutate({ taskId: row.taskId, logId: row.id })}
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
                  <span className="min-w-14 text-center">
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
