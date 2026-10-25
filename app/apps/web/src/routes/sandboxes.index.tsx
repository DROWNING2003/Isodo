import { createFileRoute } from "@tanstack/react-router";
import { CloudOverview } from "../features/isodo/CloudPages";
export const Route = createFileRoute("/sandboxes/")({ component: CloudOverview });
