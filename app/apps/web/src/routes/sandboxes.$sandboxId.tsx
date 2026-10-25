import { createFileRoute } from "@tanstack/react-router";
import {
  CloudWorkspace,
  isCloudWorkspacePane,
  type CloudWorkspacePane,
} from "../features/isodo/CloudWorkspace";

export const Route = createFileRoute("/sandboxes/$sandboxId")({
  validateSearch: (search): { task?: string; pane?: CloudWorkspacePane } => {
    return {
      ...(typeof search.task === "string" ? { task: search.task } : {}),
      ...(isCloudWorkspacePane(search.pane) ? { pane: search.pane } : {}),
    };
  },
  component: SandboxWorkspace,
});
function SandboxWorkspace() {
  const { sandboxId } = Route.useParams();
  return <CloudWorkspace key={sandboxId} sandboxId={sandboxId} />;
}
