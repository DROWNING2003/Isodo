import { createFileRoute } from "@tanstack/react-router";
import { CloudCredentials } from "../features/isodo/CloudPages";
export const Route = createFileRoute("/settings/sandbox")({ component: CloudCredentials });
