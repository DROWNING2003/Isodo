import { describe, expect, it } from "vite-plus/test";

import { DEFAULT_SANDBOX_UPLOAD_DIRECTORY, sandboxUploadPath } from "./sandboxUpload";

describe("sandbox upload paths", () => {
  it("uses the project directory by default and removes trailing slashes", () => {
    expect(sandboxUploadPath("", "README.md")).toBe(
      `${DEFAULT_SANDBOX_UPLOAD_DIRECTORY}/README.md`,
    );
    expect(sandboxUploadPath("/tmp/uploads///", "image.png")).toBe("/tmp/uploads/image.png");
    expect(sandboxUploadPath("/", "image.png")).toBe("/image.png");
  });

  it("requires absolute directories and plain file names", () => {
    expect(() => sandboxUploadPath("tmp/uploads", "README.md")).toThrow(
      "Target directory must be an absolute path.",
    );
    expect(() => sandboxUploadPath("/tmp/uploads", "../secret.txt")).toThrow(
      "File name must not contain a path separator.",
    );
  });
});
