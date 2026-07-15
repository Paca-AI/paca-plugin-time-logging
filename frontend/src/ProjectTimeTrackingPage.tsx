import TimeTrackingPage from "./TimeTrackingPage";

interface ProjectTimeTrackingPageProps {
  projectId: string;
}

/**
 * ProjectTimeTrackingPage — the `project.page` entry component exposed by
 * the time-logging plugin, reached via a dedicated project sidebar nav item.
 * Thin wrapper: the actual list/table/filter/edit UI lives in the shared
 * TimeTrackingPage component, also used by AdminTimeTrackingPage.
 */
export default function ProjectTimeTrackingPage({
  projectId,
}: ProjectTimeTrackingPageProps) {
  return <TimeTrackingPage scope={{ kind: "project", projectId }} />;
}
