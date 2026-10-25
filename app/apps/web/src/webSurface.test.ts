import { describe, expect, it } from "vite-plus/test";
import { resolveWebSurface } from "./webSurface";
describe("Isodo web surface", () => {
  it.each([undefined, "sandbox", "preview", ""])("stays sandbox-only for %j", (value) => {
    expect(resolveWebSurface(value)).toBe("sandbox");
  });

  it("lets the desktop client opt into the full surface", () => {
    expect(resolveWebSurface("full")).toBe("full");
    expect(resolveWebSurface(" full ")).toBe("full");
  });
});
