import type {
  CloudSandbox,
  CloudSandboxService,
  CloudState,
  CloudTask,
  CloudSnapshot,
  CloudApiKey,
} from "./types.ts";

export function initialCloudState(): CloudState {
  return {
    sandboxes: [
      {
        id: "studio",
        name: "项目开发",
        template: "dev",
        status: "running",
        cpu: 4,
        memoryGB: 8,
        diskGB: 32,
        connectionIds: ["github", "anthropic"],
        files: [
          {
            path: "README.md",
            content: "# 项目开发\n\nAgent 在云端沙箱内运行，文件保存在沙箱中。",
          },
          {
            path: "src/app.tsx",
            content: "export function App() { return <main>Hello Isodo</main>; }",
          },
        ],
      },
      {
        id: "assistant",
        name: "日常助手",
        template: "daily",
        status: "running",
        cpu: 2,
        memoryGB: 4,
        diskGB: 20,
        connectionIds: ["notion"],
        files: [{ path: "notes/weekly.md", content: "# 本周阅读\n\n整理产品、设计和工程资料。" }],
      },
      {
        id: "lab",
        name: "实验环境",
        template: "blank",
        status: "paused",
        cpu: 2,
        memoryGB: 4,
        diskGB: 20,
        connectionIds: [],
        files: [],
      },
    ],
    tasks: [
      {
        id: "intro",
        sandboxId: "studio",
        title: "梳理项目架构并生成文档",
        agent: "Claude Code",
        mode: "supervised",
        status: "completed",
        output:
          "已检查示例项目。应用源码位于 `src/`，文档保存在 `docs/`。这是用于验证工作流的模拟产出。",
        artifact: null,
      },
    ],
    credentials: [
      { id: "github", service: "GitHub", label: "个人 GitHub" },
      { id: "anthropic", service: "Anthropic", label: "Claude 开发连接" },
      { id: "notion", service: "Notion MCP", label: "个人笔记" },
    ],
    snapshots: [],
    apiKeys: [
      {
        id: "demo-api-key",
        name: "开发环境",
        prefix: "iso_demo_ab12…",
        scopes: ["sandboxes:read", "sandboxes:write"],
        createdAt: new Date().toISOString(),
        expiresAt: new Date(Date.now() + 30 * 24 * 60 * 60 * 1000).toISOString(),
        lastUsedAt: null,
        revokedAt: null,
      },
    ],
  };
}

export function createMockCloudService(
  initial: CloudState = initialCloudState(),
): CloudSandboxService {
  let state = initial;
  let sequence = 0;
  const listeners = new Set<() => void>();
  const id = () => `mock-${++sequence}-${Math.random().toString(36).slice(2, 9)}`;
  const publish = (next: CloudState) => {
    state = next;
    for (const listener of listeners) listener();
  };
  const sandbox = (sandboxId: string) => {
    const value = state.sandboxes.find((s) => s.id === sandboxId);
    if (!value) throw new Error("沙箱不存在");
    return value;
  };
  const apiKey = (apiKeyId: string) => {
    const value = state.apiKeys.find((key) => key.id === apiKeyId);
    if (!value) throw new Error("密钥不存在");
    return value;
  };
  function complete(task: CloudTask): CloudTask {
    const file = {
      path: `outputs/${task.id}.md`,
      content: `# ${task.title}\n\n这是 ${task.agent} 在云端沙箱中的模拟产出。\n\n后续接入真实服务后，这里将显示沙箱内 T3 Runtime 的执行结果。`,
    };
    return {
      ...task,
      status: "completed",
      output: "已完成示例任务，产出已保存在当前沙箱。",
      artifact: file,
    };
  }
  function saveTask(task: CloudTask) {
    const s = sandbox(task.sandboxId);
    publish({
      ...state,
      tasks: [task, ...state.tasks.filter((t) => t.id !== task.id)],
      sandboxes: state.sandboxes.map((v) =>
        v.id === s.id && task.artifact ? { ...v, files: [...v.files, task.artifact] } : v,
      ),
    });
  }
  return {
    mode: "mock",
    getSnapshot: () => state,
    subscribe: (listener) => {
      listeners.add(listener);
      return () => {
        listeners.delete(listener);
      };
    },
    createSandbox: ({ name, template }) => {
      const value: CloudSandbox = {
        id: id(),
        name: name.trim() || "新沙箱",
        template,
        status: "running",
        cpu: 2,
        memoryGB: 4,
        diskGB: 20,
        connectionIds: [],
        files: [],
      };
      publish({ ...state, sandboxes: [...state.sandboxes, value] });
      return value;
    },
    setStatus: (sandboxId, status) => {
      sandbox(sandboxId);
      publish({
        ...state,
        sandboxes: state.sandboxes.map((s) => (s.id === sandboxId ? { ...s, status } : s)),
      });
    },
    createTask: (input) => {
      if (sandbox(input.sandboxId).status !== "running") throw new Error("请先唤醒沙箱");
      if (!input.prompt.trim()) throw new Error("请输入任务描述");
      const task: CloudTask = {
        id: id(),
        sandboxId: input.sandboxId,
        title: input.prompt.trim(),
        agent: input.agent,
        mode: input.mode,
        status: "approval",
        output: "已读取沙箱工作目录。Agent 请求写入任务产出，是否允许本次操作？",
        artifact: null,
      };
      const result = input.mode === "supervised" ? task : complete(task);
      saveTask(result);
      return result;
    },
    resolveTask: (taskId, allow) => {
      const task = state.tasks.find((t) => t.id === taskId);
      if (!task || task.status !== "approval") throw new Error("任务不处于待确认状态");
      if (sandbox(task.sandboxId).status !== "running") throw new Error("请先唤醒沙箱");
      saveTask(
        allow
          ? complete(task)
          : { ...task, status: "cancelled", output: "已拒绝本次操作，沙箱文件未修改。" },
      );
    },
    setConnection: (sandboxId, credentialId, granted) => {
      const s = sandbox(sandboxId);
      if (!state.credentials.some((c) => c.id === credentialId)) throw new Error("连接不存在");
      const ids = s.connectionIds.filter((v) => v !== credentialId);
      publish({
        ...state,
        sandboxes: state.sandboxes.map((v) =>
          v.id === s.id ? { ...v, connectionIds: granted ? [...ids, credentialId] : ids } : v,
        ),
      });
    },
    takeSnapshot: (sandboxId) => {
      const s = sandbox(sandboxId);
      const snapshot: CloudSnapshot = {
        id: id(),
        name: `${s.name} · 快照`,
        sourceSandboxId: s.id,
        template: s.template,
        cpu: s.cpu,
        memoryGB: s.memoryGB,
        diskGB: s.diskGB,
        files: s.files.map((file) => ({ ...file })),
      };
      publish({ ...state, snapshots: [...state.snapshots, snapshot] });
      return snapshot;
    },
    restoreSnapshot: (snapshotId) => {
      const snapshot = state.snapshots.find((v) => v.id === snapshotId);
      if (!snapshot) throw new Error("快照不存在");
      const value: CloudSandbox = {
        id: id(),
        name: `${snapshot.name} · 副本`,
        template: snapshot.template,
        cpu: snapshot.cpu,
        memoryGB: snapshot.memoryGB,
        diskGB: snapshot.diskGB,
        files: snapshot.files.map((file) => ({ ...file })),
        status: "running",
        connectionIds: [],
      };
      publish({ ...state, sandboxes: [...state.sandboxes, value] });
      return value;
    },
    createApiKey: ({ name, scopes, expiresInDays = 30 }) => {
      const normalizedName = name.trim();
      if (!normalizedName) throw new Error("请输入密钥名称");
      if (scopes.length === 0) throw new Error("至少选择一个访问权限");
      if (scopes.some((scope) => scope !== "sandboxes:read" && scope !== "sandboxes:write")) {
        throw new Error("访问权限无效");
      }
      if (expiresInDays !== null && (!Number.isInteger(expiresInDays) || expiresInDays < 1)) {
        throw new Error("有效期必须是正整数或永久有效");
      }
      const keyId = id();
      const entropy = Math.random().toString(36).slice(2, 18);
      const secret = `iso_demo_${entropy}_${keyId.replaceAll("-", "")}`;
      const now = new Date();
      const expiresAt =
        expiresInDays === null
          ? null
          : new Date(now.getTime() + expiresInDays * 24 * 60 * 60 * 1000).toISOString();
      const value: CloudApiKey = {
        id: keyId,
        name: normalizedName,
        prefix: `${secret.slice(0, 13)}…`,
        scopes: [...scopes],
        createdAt: now.toISOString(),
        expiresAt,
        lastUsedAt: null,
        revokedAt: null,
      };
      publish({ ...state, apiKeys: [value, ...state.apiKeys] });
      return { apiKey: value, secret };
    },
    revokeApiKey: (apiKeyId) => {
      const value = apiKey(apiKeyId);
      if (value.revokedAt) return;
      publish({
        ...state,
        apiKeys: state.apiKeys.map((key) =>
          key.id === apiKeyId ? { ...key, revokedAt: new Date().toISOString() } : key,
        ),
      });
    },
  };
}
