import { createFileRoute } from "@tanstack/react-router";
import { ChatIndexRouteView } from "./_chat.index";

// Successful sandbox pairing enters here; / remains the app launch page.
export const Route = createFileRoute("/_chat/workspace")({
  component: ChatIndexRouteView,
});
