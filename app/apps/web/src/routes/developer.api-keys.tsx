import { createFileRoute } from "@tanstack/react-router";
import { ApiKeysPage } from "../features/isodo/ApiKeysPage";

export const Route = createFileRoute("/developer/api-keys")({ component: ApiKeysPage });
