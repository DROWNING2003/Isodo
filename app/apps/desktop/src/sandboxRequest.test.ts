import { expect, it } from "vite-plus/test";
import { validateSandboxRequest } from "./sandboxRequest.ts";
it("allows canonical management routes and pagination", () => {
  expect(
    validateSandboxRequest("/v1/sandboxes?limit=10&offset=0", "GET", undefined).path,
  ).toContain("limit=10");
  expect(validateSandboxRequest("/v1/sandboxes/local-a/stop", "POST", undefined).method).toBe(
    "POST",
  );
});
it.each([
  "https://evil.invalid/v1/sandboxes",
  "//evil.invalid/v1/sandboxes",
  "/v1/sandboxes/a/../../health",
  "/v1/sandboxes/a/injections",
  "/v1/sandboxes?token=secret",
])("rejects renderer escape: %s", (path) => {
  expect(() => validateSandboxRequest(path, "GET", undefined)).toThrow();
});
it("rejects oversized bodies and unsupported verbs", () => {
  expect(() => validateSandboxRequest("/v1/sandboxes", "POST", "x".repeat(65537))).toThrow();
  expect(() => validateSandboxRequest("/v1/templates", "DELETE", undefined)).toThrow();
});
