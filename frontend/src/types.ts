// Domain types for the com.paca.time-logging plugin frontend.
// These mirror the JSON produced by the backend plugin handlers.

export interface TimeLog {
  id: string;
  task_id: string;
  member_id: string;
  spent_date: string; // YYYY-MM-DD
  minutes_spent: number;
  note: string;
  created_at: string;
  updated_at: string;
}

export interface ProjectTotal {
  project_id: string;
  project_name: string;
  minutes_spent: number;
}

/** Cross-project user total, returned by GET /time-logs/users-all. */
export interface UserTotalAll {
  user_id: string;
  user_name: string;
  minutes_spent: number;
}

/** Time log entry annotated with project + member display names, returned
 * by the cross-project admin list endpoint. */
export interface TimeLogWithContext {
  id: string;
  project_id: string;
  project_name: string;
  task_id: string;
  task_title: string;
  member_id: string;
  member_name: string;
  spent_date: string;
  minutes_spent: number;
  note: string;
  created_at: string;
}

/** Caller identity + custom-permission state within the current project,
 * returned by GET /projects/:projectId/time-logs/me. Used to decide whether
 * to offer edit/delete controls for an entry that belongs to another member. */
export interface TimeLogViewer {
  member_id: string;
  can_manage_all: boolean;
}

interface PageMeta {
  total: number;
  total_minutes: number;
  page: number;
  page_size: number;
}

/** Response envelope for GET /projects/:projectId/time-logs. */
export interface TimeLogPage extends PageMeta {
  logs: TimeLog[];
}

/** Response envelope for GET /time-logs/all (admin, cross-project). */
export interface TimeLogAllPage extends PageMeta {
  logs: TimeLogWithContext[];
}

