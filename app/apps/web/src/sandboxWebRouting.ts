import { isSandboxUrl } from "@t3tools/shared/sandbox";
import type { WebSurface } from "./webSurface";

export function shouldRedirectSandboxWebChatIndex(input: {
  readonly catalogReady: boolean;
  readonly sandboxCount: number;
}): boolean {
  return input.catalogReady && input.sandboxCount === 0;
}

export function resolveSandboxWebRestrictedPathRedirect(pathname: string): string | null {
  if (
    pathname === "/welcome" ||
    pathname.startsWith("/draft/") ||
    pathname.startsWith("/projects/")
  )
    return "/sandboxes";
  if (pathname === "/pair" || pathname === "/connect" || pathname.startsWith("/connect/")) {
    return "/sandboxes";
  }
  if (pathname === "/settings" || pathname.startsWith("/settings/")) {
    return pathname === "/settings/sandbox" || pathname === "/settings/api-keys"
      ? null
      : "/sandboxes";
  }
  return null;
}

export function shouldRedirectSandboxWebEnvironment(input: {
  readonly catalogReady: boolean;
  readonly displayUrl: string | null;
}): boolean {
  return input.catalogReady && (input.displayUrl === null || !isSandboxUrl(input.displayUrl));
}

export function resolveSandboxWebNoProjectsAction(
  surface: WebSurface,
): "add-project" | "sandboxes" {
  return surface === "sandbox" ? "sandboxes" : "add-project";
}
