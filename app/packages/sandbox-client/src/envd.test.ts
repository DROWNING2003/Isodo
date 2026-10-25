import { describe, expect, it } from "vite-plus/test";

import { envdFileUrl } from "./envd";

describe("envd URLs", () => {
  it("uses the sandbox domain returned by the API", () => {
    expect(
      envdFileUrl("sandbox-1", "sandbox.example.com", "/home/user/.t3/mobile-pairing.json"),
    ).toBe(
      "https://49983-sandbox-1.sandbox.example.com/files?path=%2Fhome%2Fuser%2F.t3%2Fmobile-pairing.json&username=user",
    );
  });
});
