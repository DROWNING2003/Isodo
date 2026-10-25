import { createFileRoute } from "@tanstack/react-router";
import { CloudLogin } from "../features/isodo/CloudPages";

export const Route = createFileRoute("/sandboxes/login")({ component: CloudLogin });
