import { createFileRoute } from "@tanstack/react-router";
import { SandboxConnectionsPage } from "../features/isodo/SandboxConnectionsPage";

export const Route = createFileRoute("/sandbox-connections/$sandboxId")({
  component: function SandboxConnectionsRoute() {
    const { sandboxId } = Route.useParams();
    return <SandboxConnectionsPage sandboxId={sandboxId} />;
  },
});
