import { createFileRoute, redirect } from "@tanstack/react-router";

export const Route = createFileRoute("/settings/api-keys")({
  beforeLoad: () => {
    throw redirect({ to: "/developer/api-keys" });
  },
});
