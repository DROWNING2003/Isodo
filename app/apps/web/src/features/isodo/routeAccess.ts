import { getAuthState, refreshAuth } from "./auth";

export async function isodoRouteRedirect(
  path: string,
): Promise<"/sandboxes/login" | "/sandboxes" | null> {
  if (
    path === "/sandboxes/login" ||
    path === "/pair" ||
    path === "/connect" ||
    path.startsWith("/connect/")
  )
    return null;
  if (getAuthState().status === "loading") await refreshAuth();
  if (getAuthState().status !== "authenticated") return "/sandboxes/login";
  return path === "/" ? "/sandboxes" : null;
}
