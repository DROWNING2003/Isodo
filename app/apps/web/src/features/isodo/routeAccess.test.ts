import { beforeEach, expect, it, vi } from "vite-plus/test";
const fixture = vi.hoisted(() => ({ status: "loading", refresh: vi.fn() }));
vi.mock("./auth", () => ({
  getAuthState: () => ({ status: fixture.status }),
  refreshAuth: fixture.refresh,
}));
import { isodoRouteRedirect } from "./routeAccess";
beforeEach(() => {
  fixture.status = "loading";
  fixture.refresh.mockReset();
});
it("awaits account verification before allowing a restored task route", async () => {
  fixture.refresh.mockImplementation(async () => {
    fixture.status = "authenticated";
  });
  expect(await isodoRouteRedirect("/draft/saved-draft")).toBeNull();
  expect(fixture.refresh).toHaveBeenCalledOnce();
});
it("sends unauthenticated and expired sessions to the login URL", async () => {
  for (const status of ["unauthenticated", "error"]) {
    fixture.status = status;
    expect(await isodoRouteRedirect("/draft/saved-draft")).toBe("/sandboxes/login");
    expect(await isodoRouteRedirect("/developer/settings")).toBe("/sandboxes/login");
  }
});
it("lands authenticated app launches on the sandbox list without opening cached drafts", async () => {
  fixture.status = "authenticated";
  expect(await isodoRouteRedirect("/")).toBe("/sandboxes");
});
it("leaves login and pairing callbacks accessible without a session", async () => {
  for (const path of ["/sandboxes/login", "/pair", "/connect", "/connect/callback"])
    expect(await isodoRouteRedirect(path)).toBeNull();
  expect(fixture.refresh).not.toHaveBeenCalled();
});

it("allows the explicit workbench landing after pairing", async () => {
  fixture.status = "authenticated";
  expect(await isodoRouteRedirect("/workspace")).toBeNull();
});
