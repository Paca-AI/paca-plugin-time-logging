import { PluginApiClient, PluginQueryClientProvider } from "@paca-ai/plugin-sdk-react";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { ChevronLeft, ChevronRight, Clock, X } from "lucide-react";
import { useMemo, useState } from "react";
import { PLUGIN_ID } from "./constants";
import {
  type DatePreset,
  formatDate,
  formatMinutes,
  isoDaysAgo,
  isoFirstOfMonth,
  PAGE_SIZES,
  presetLabel,
  todayDateString,
} from "./shared";
import type { ProjectTotal, TimeLogAllPage, UserTotalAll } from "./types";

/**
 * AdminTimeTrackingPage — the `admin.page` entry component exposed by the
 * time-logging plugin. Cross-project administration view: shows every
 * logged time entry across every project in the instance, filterable by
 * project, user, and date range with backend-driven pagination and summing.
 * Filters by user (users.id) rather than project membership, since the same
 * person has a different project_members row per project — a user filter is
 * what lets one selection cover all of a person's entries instance-wide.
 * Reached via a dedicated nav item in the admin sidebar; gated by the same
 * `users.write` permission as the built-in admin pages.
 *
 * Read-only by design: unlike the project-scoped page, editing/deleting an
 * entry here would act on a project the admin may not be a member of, and
 * this plugin has no way to confirm from the frontend that the underlying
 * project-scoped write permission would actually be granted for that entry.
 */
export default function AdminTimeTrackingPage() {
  return (
    <PluginQueryClientProvider>
      <AdminTimeTrackingPageInner />
    </PluginQueryClientProvider>
  );
}

function AdminTimeTrackingPageInner() {
  // No projectId: this is a global/admin-scoped page.
  const api = useMemo(
    () =>
      new PluginApiClient({
        baseUrl: `${window.location.origin}/api/v1`,
        fetch: (url, init) =>
          window.fetch(url, { ...init, credentials: "include" }),
      }),
    [],
  );

  // ── Filters ────────────────────────────────────────────────────────────────

  const [projectFilter, setProjectFilterState] = useState("");
  const [userFilter, setUserFilterState] = useState("");
  const [dateFrom, setDateFromState] = useState("");
  const [dateTo, setDateToState] = useState("");
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState<number>(PAGE_SIZES[0]);

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
    setProjectFilterState("");
    setUserFilterState("");
    setDateFromState("");
    setDateToState("");
    setPage(1);
  };
  const hasActiveFilters =
    projectFilter !== "" || userFilter !== "" || dateFrom !== "" || dateTo !== "";

  // ── Filter dropdown totals ─────────────────────────────────────────────────
  //
  // These double as the option lists for the Project/User filters below —
  // there is no global "list all projects"/"list all members" call available
  // to plugins (project_members are project-scoped). Each is computed against
  // the *other* active filters (never its own dimension, or picking a project
  // would collapse the User list to just that project and vice versa), and
  // refetches whenever those filters change so the totals shown always match
  // the current scope.

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
  });

  // ── Query ──────────────────────────────────────────────────────────────────

  const logsPath = useMemo(() => {
    const params = new URLSearchParams();
    if (projectFilter) params.set("project_id", projectFilter);
    if (userFilter) params.set("user_id", userFilter);
    if (dateFrom) params.set("date_from", dateFrom);
    if (dateTo) params.set("date_to", dateTo);
    params.set("page", String(page));
    params.set("page_size", String(pageSize));
    return `/time-logs/all?${params.toString()}`;
  }, [projectFilter, userFilter, dateFrom, dateTo, page, pageSize]);

  const { data: logPage, isLoading } = useQuery<TimeLogAllPage>({
    queryKey: [
      "plugin",
      PLUGIN_ID,
      "time-logs-all",
      projectFilter,
      userFilter,
      dateFrom,
      dateTo,
      page,
      pageSize,
    ],
    queryFn: () => api.pluginGet<TimeLogAllPage>(PLUGIN_ID, logsPath),
    placeholderData: keepPreviousData,
  });

  const logs = logPage?.logs ?? [];
  const total = logPage?.total ?? 0;
  const totalMinutes = logPage?.total_minutes ?? 0;
  const currentPage = logPage?.page ?? page;
  const currentPageSize = logPage?.page_size ?? pageSize;
  const totalPages = Math.max(1, Math.ceil(total / currentPageSize));
  const pageStart = total === 0 ? 0 : (currentPage - 1) * currentPageSize;

  const goPrev = () => setPage(Math.max(1, currentPage - 1));
  const goNext = () => setPage(Math.min(totalPages, currentPage + 1));
  const handlePageSizeChange = (n: number) => {
    setPageSize(n);
    setPage(1);
  };

  // ── Render ─────────────────────────────────────────────────────────────────

  return (
    <div className="flex flex-col gap-6 p-6 max-w-5xl w-full mx-auto">
      <div>
        <div className="flex items-center gap-2">
          <Clock className="size-5 text-primary" />
          <h1 className="text-xl font-semibold">Time Tracking — All Projects</h1>
        </div>
        <p className="mt-1 text-sm text-muted-foreground">
          Browse and filter logged time across every project.
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
                htmlFor="time-tracking-project-filter"
                className="text-xs font-semibold uppercase tracking-wide text-muted-foreground/60"
              >
                Project
              </label>
              <select
                id="time-tracking-project-filter"
                value={projectFilter}
                onChange={(e) => setProjectFilter(e.target.value)}
                className="min-w-[9rem] rounded-lg bg-muted/40 px-2.5 py-1.5 text-xs"
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
                className="min-w-[9rem] rounded-lg bg-muted/40 px-2.5 py-1.5 text-xs"
              >
                <option value="">All users</option>
                {userTotals.map((u) => (
                  <option key={u.user_id} value={u.user_id}>
                    {u.user_name || u.user_id}
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
          <EmptyRow text="No time logged in any project yet" />
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
                      <th className="px-3 py-2 text-left font-semibold">Project</th>
                      <th className="px-3 py-2 text-left font-semibold">User</th>
                      <th className="px-3 py-2 text-left font-semibold">Task</th>
                      <th className="px-3 py-2 text-left font-semibold">Note</th>
                      <th className="px-3 py-2 text-right font-semibold">Duration</th>
                    </tr>
                  </thead>
                  <tbody>
                    {logs.map((log) => (
                      <tr key={log.id} className="border-t border-border/10">
                        <td className="px-3 py-2 whitespace-nowrap">{formatDate(log.spent_date)}</td>
                        <td className="max-w-40 truncate px-3 py-2">{log.project_name}</td>
                        <td className="px-3 py-2 truncate">{log.member_name || log.member_id}</td>
                        <td className="max-w-48 truncate px-3 py-2">{log.task_title || log.task_id}</td>
                        <td
                          className="max-w-64 truncate px-3 py-2 text-muted-foreground/70"
                          title={log.note || undefined}
                        >
                          {log.note || "—"}
                        </td>
                        <td className="px-3 py-2 text-right font-semibold whitespace-nowrap">
                          {formatMinutes(log.minutes_spent)}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </div>

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
