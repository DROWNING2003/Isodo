import * as NodeTest from "node:test";
import * as NodeAssert from "node:assert/strict";
import { createMockCloudService } from "./mock.ts";

NodeTest.test("approval controls artifacts and paused sandboxes cannot run tasks", () => {
  const service = createMockCloudService();
  const task = service.createTask({
    sandboxId: "studio",
    prompt: "整理项目",
    agent: "Codex",
    mode: "supervised",
  });
  NodeAssert.equal(task.status, "approval");
  NodeAssert.equal(service.getSnapshot().sandboxes[0]?.files.length, 2);
  service.resolveTask(task.id, true);
  NodeAssert.equal(service.getSnapshot().tasks[0]?.status, "completed");
  NodeAssert.equal(service.getSnapshot().sandboxes[0]?.files.length, 3);
  NodeAssert.throws(() => service.resolveTask(task.id, true));
  service.setStatus("studio", "paused");
  NodeAssert.throws(() =>
    service.createTask({ sandboxId: "studio", prompt: "x", agent: "Codex", mode: "auto" }),
  );
});

NodeTest.test("snapshots preserve files and resources but restore with new authorization", () => {
  const service = createMockCloudService();
  const snapshot = service.takeSnapshot("studio");
  NodeAssert.equal("connectionIds" in snapshot, false);
  const restored = service.restoreSnapshot(snapshot.id);
  NodeAssert.notEqual(restored.id, "studio");
  NodeAssert.deepEqual(restored.connectionIds, []);
  NodeAssert.equal(restored.cpu, 4);
  service.createTask({ sandboxId: restored.id, prompt: "write", agent: "Codex", mode: "auto" });
  NodeAssert.equal(service.getSnapshot().sandboxes.find((v) => v.id === "studio")?.files.length, 2);
  NodeAssert.equal(snapshot.files.length, 2);
});

NodeTest.test("revoking one sandbox never revokes another sandbox", () => {
  const service = createMockCloudService();
  service.setConnection("assistant", "github", true);
  service.setConnection("studio", "github", false);
  NodeAssert.equal(
    service
      .getSnapshot()
      .sandboxes.find((v) => v.id === "assistant")
      ?.connectionIds.includes("github"),
    true,
  );
  NodeAssert.equal(
    service
      .getSnapshot()
      .sandboxes.find((v) => v.id === "studio")
      ?.connectionIds.includes("github"),
    false,
  );
});

NodeTest.test("API keys reveal a demo secret once and only retain safe metadata", () => {
  const service = createMockCloudService();
  const created = service.createApiKey({
    name: "CI deployment",
    scopes: ["sandboxes:read", "sandboxes:write"],
    expiresInDays: 30,
  });

  NodeAssert.match(created.secret, /^iso_demo_/);
  NodeAssert.equal(created.apiKey.name, "CI deployment");
  NodeAssert.deepEqual(created.apiKey.scopes, ["sandboxes:read", "sandboxes:write"]);
  NodeAssert.ok(created.apiKey.prefix.startsWith("iso_demo_"));
  NodeAssert.ok(created.apiKey.expiresAt);
  NodeAssert.equal("secret" in created.apiKey, false);
  NodeAssert.equal(JSON.stringify(service.getSnapshot()).includes(created.secret), false);
  NodeAssert.equal(service.getSnapshot().apiKeys[0]?.id, created.apiKey.id);
});

NodeTest.test("API key revocation is visible and unknown keys are rejected", () => {
  const service = createMockCloudService();
  const { apiKey } = service.createApiKey({ name: "temporary", scopes: ["sandboxes:read"] });
  service.revokeApiKey(apiKey.id);

  NodeAssert.ok(service.getSnapshot().apiKeys[0]?.revokedAt);
  NodeAssert.throws(() => service.revokeApiKey("missing-key"), /密钥不存在/);
  NodeAssert.throws(() => service.createApiKey({ name: "  ", scopes: ["sandboxes:read"] }), /名称/);
});
