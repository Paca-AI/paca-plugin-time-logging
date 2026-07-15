/**
 * shared.tsx — small formatting and viewer-permission helpers used across the
 * time-logging frontend components.
 */

import type { PluginApiClient } from "@paca-ai/plugin-sdk-react";
import { useQuery } from "@tanstack/react-query";
import { PLUGIN_ID } from "./constants";
import type { TimeLogViewer } from "./types";

/** Formats a minute count as "1h 30m" / "45m" / "2h". */
export function formatMinutes(minutes: number): string {
  if (minutes <= 0) return "0m";
  const h = Math.floor(minutes / 60);
  const m = minutes % 60;
  if (h === 0) return `${m}m`;
  if (m === 0) return `${h}h`;
  return `${h}h ${m}m`;
}

/** Converts a duration string like "1h30m", "90m", or "1.5h" into minutes. */
export function parseDurationToMinutes(input: string): number | null {
  const trimmed = input.trim().toLowerCase();
  if (trimmed === "") return null;

  // "1h30m", "1h", "30m"
  const hm = trimmed.match(/^(?:(\d+(?:\.\d+)?)h)?\s*(?:(\d+)m)?$/);
  if (hm && (hm[1] || hm[2])) {
    const hours = hm[1] ? parseFloat(hm[1]) : 0;
    const mins = hm[2] ? parseInt(hm[2], 10) : 0;
    return Math.round(hours * 60 + mins);
  }

  // Plain number: interpret as minutes.
  const asNumber = Number(trimmed);
  if (!Number.isNaN(asNumber) && asNumber > 0) {
    return Math.round(asNumber);
  }

  return null;
}

/** Returns today's date as YYYY-MM-DD in local time. */
export function todayDateString(): string {
  const now = new Date();
  const y = now.getFullYear();
  const m = String(now.getMonth() + 1).padStart(2, "0");
  const d = String(now.getDate()).padStart(2, "0");
  return `${y}-${m}-${d}`;
}

/**
 * Formats a date string as a readable local date, e.g. "Jul 14, 2026".
 * Accepts a plain "YYYY-MM-DD" value or a full timestamp with a "T..." time
 * component (defensive: the date-only portion is used either way).
 */
export function formatDate(spentDate: string): string {
  const [y, m, d] = spentDate.split("T")[0].split("-").map(Number);
  if (!y || !m || !d) return spentDate;
  return new Date(y, m - 1, d).toLocaleDateString(undefined, {
    year: "numeric",
    month: "short",
    day: "numeric",
  });
}

/** Returns the date N days before today as YYYY-MM-DD in local time. */
export function isoDaysAgo(n: number): string {
  const d = new Date();
  d.setDate(d.getDate() - n);
  const y = d.getFullYear();
  const m = String(d.getMonth() + 1).padStart(2, "0");
  const day = String(d.getDate()).padStart(2, "0");
  return `${y}-${m}-${day}`;
}

/** Returns the first day of the current month as YYYY-MM-DD in local time. */
export function isoFirstOfMonth(): string {
  const d = new Date();
  const y = d.getFullYear();
  const m = String(d.getMonth() + 1).padStart(2, "0");
  return `${y}-${m}-01`;
}

/** Quick date-range filter presets shared by the time-tracking list pages. */
export type DatePreset = "today" | "week" | "month" | "all";

export function presetLabel(preset: DatePreset): string {
  switch (preset) {
    case "today":
      return "Today";
    case "week":
      return "7 days";
    case "month":
      return "This month";
    case "all":
      return "All time";
  }
}

/** Row-count options for the "rows per page" control on paginated list pages. */
export const PAGE_SIZES = [10, 25, 50] as const;

/**
 * Fetches the current caller's project_members id and whether they hold
 * time_logging.manage_all for this project. List views use this to decide
 * whether to offer edit/delete controls for an entry — own entries are
 * always editable; other members' entries require manage_all. This is a UX
 * affordance only: the backend re-checks ownership/manage_all on every
 * write, so it remains the actual authorization boundary.
 */
export function useTimeLogViewer(
  api: PluginApiClient,
  projectId: string,
): TimeLogViewer | undefined {
  const { data } = useQuery<TimeLogViewer>({
    queryKey: ["plugin", PLUGIN_ID, "viewer", projectId],
    queryFn: () =>
      api.pluginGet<TimeLogViewer>(PLUGIN_ID, `/projects/${projectId}/time-logs/me`),
    staleTime: 5 * 60 * 1000,
  });
  return data;
}

/** Whether the viewer may edit/delete a time log entry owned by memberId. */
export function canManageTimeLog(
  viewer: TimeLogViewer | undefined,
  memberId: string,
): boolean {
  if (!viewer) return false;
  return viewer.can_manage_all || viewer.member_id === memberId;
}
