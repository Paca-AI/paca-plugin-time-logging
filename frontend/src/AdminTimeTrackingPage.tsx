import TimeTrackingPage from "./TimeTrackingPage";

/**
 * AdminTimeTrackingPage — the `admin.page` entry component exposed by the
 * time-logging plugin, reached via a dedicated nav item in the admin
 * sidebar; gated by the plugin's own `time_logging.view_all` global custom
 * permission (declared in plugin.json), not the built-in `users.write`.
 * Thin wrapper: the actual list/table/filter/edit UI lives in the shared
 * TimeTrackingPage component, also used by ProjectTimeTrackingPage.
 *
 * Edit/delete controls appear here only for callers who additionally hold
 * the global `time_logging.manage_all` permission (see useTimeLogViewerAll),
 * backed by the dedicated global-scope PATCH/DELETE /time-logs/all/:logId
 * routes — a global grant applies across every project regardless of the
 * caller's membership in any one of them, unlike the project-scoped page's
 * per-row ownership check.
 */
export default function AdminTimeTrackingPage() {
  return <TimeTrackingPage scope={{ kind: "admin" }} />;
}
