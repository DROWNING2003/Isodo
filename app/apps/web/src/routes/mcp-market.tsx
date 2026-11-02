import { createFileRoute } from "@tanstack/react-router";
import { MCPMarketPage } from "../features/isodo/MCPMarketPage";
export const Route = createFileRoute("/mcp-market")({ component: MCPMarketPage });
