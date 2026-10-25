import { expect, it, vi } from "vite-plus/test";

const api = vi.hoisted(() => ({
  listTemplates: vi.fn(),
  getTemplateTag: vi.fn(),
  getTemplateBuild: vi.fn(),
}));
vi.mock("../../api/public.generated", () => api);
import { loadCloudAppTemplates } from "./cloudAppTemplates";

it("includes a system T3 template beyond the first page and pins its Ready default build", async () => {
  api.listTemplates
    .mockReset()
    .mockResolvedValueOnce({
      items: [{ id: "empty", name: "draft", scope: "personal" }],
      total: 2,
    })
    .mockResolvedValueOnce({
      items: [{ id: "t3", name: "t3-base", scope: "system" }],
      total: 2,
    });
  api.getTemplateTag.mockImplementation(async (id: string) => {
    if (id === "empty") throw Object.assign(new Error("unpublished"), { status: 404 });
    return { build_id: "build-ready" };
  });
  api.getTemplateBuild.mockResolvedValue({ status: "ready" });
  await expect(loadCloudAppTemplates()).resolves.toEqual([
    { id: "t3", label: "t3-base", ready: true },
  ]);
  expect(api.listTemplates).toHaveBeenNthCalledWith(1, {
    scope: "available",
    limit: 100,
    offset: 0,
  });
  expect(api.listTemplates).toHaveBeenNthCalledWith(2, {
    scope: "available",
    limit: 100,
    offset: 1,
  });
});

it("does not offer a template whose default build is not Ready", async () => {
  api.listTemplates
    .mockReset()
    .mockResolvedValue({ items: [{ id: "pending", name: "python", scope: "personal" }], total: 1 });
  api.getTemplateTag.mockResolvedValue({ build_id: "building" });
  api.getTemplateBuild.mockResolvedValue({ status: "building" });
  await expect(loadCloudAppTemplates()).resolves.toEqual([]);
});

it("offers Ready personal templates and prefers the published base", async () => {
  api.listTemplates.mockReset().mockResolvedValue({
    items: [
      { id: "python", name: "python", scope: "personal" },
      { id: "base", name: "base", scope: "system" },
    ],
    total: 2,
  });
  api.getTemplateTag.mockResolvedValue({ build_id: "ready" });
  api.getTemplateBuild.mockResolvedValue({ status: "ready" });
  await expect(loadCloudAppTemplates()).resolves.toEqual([
    { id: "base", label: "base", ready: true },
    { id: "python", label: "python", ready: true },
  ]);
});

it("surfaces catalog failures instead of inventing a usable fallback", async () => {
  api.listTemplates.mockReset().mockRejectedValue(new Error("catalog unavailable"));
  await expect(loadCloudAppTemplates()).rejects.toThrow("catalog unavailable");
});
