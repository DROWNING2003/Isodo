import { createFileRoute } from "@tanstack/react-router";
import { CloudPage } from "../features/isodo/CloudPages";
import { CreateSandboxDialog } from "../features/isodo/CreateSandboxDialog";
export const Route = createFileRoute("/sandboxes/new")({
  component: function NewSandboxRoute() {
    return (
      <CloudPage title="创建沙箱">
        <CreateSandboxDialog />
      </CloudPage>
    );
  },
});
