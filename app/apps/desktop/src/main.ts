import { SandboxFileUploads, type FileRequestInit } from "./fileUploads.js";
import { validateSandboxRequest } from "./sandboxRequest.ts";
import { createManagedQuit, pauseManagedSandboxes } from "./shutdown.js";
import { app, BrowserWindow, dialog, ipcMain, shell } from "electron";
import * as NodeChildProcess from "node:child_process";
import * as NodeFSSync from "node:fs";
import * as NodeFS from "node:fs/promises";
import * as NodeOS from "node:os";
import * as NodePath from "node:path";

function resolveNodeBinary() {
  if (process.env.ISODO_NODE_BINARY) return process.env.ISODO_NODE_BINARY;
  const packaged = NodePath.join(process.resourcesPath, "isodo-node");
  const development = NodePath.resolve(__dirname, "../../../../isodo-node");
  return app.isPackaged ? packaged : development;
}
function goArch() {
  return process.arch === "x64" ? "amd64" : process.arch;
}

/** Development checkouts resolve from the repo root; packaged builds from resources. */
function resolveSidecarAsset(relativePath: string, packagedName: string) {
  const candidate = app.isPackaged
    ? NodePath.join(process.resourcesPath, packagedName)
    : NodePath.resolve(__dirname, "../../../../", relativePath);
  return NodeFSSync.existsSync(candidate) ? candidate : undefined;
}

// The menu bar and Dock read the bundle name; keep the runtime identity aligned
// with the product for logs, menus, and user-data paths.
app.setName("isodo");

function resolveIcon() {
  return app.isPackaged
    ? NodePath.join(process.resourcesPath, "icon.png")
    : NodePath.join(__dirname, "../resources/icon.png");
}
let mainWindow: BrowserWindow | null = null;
let nodeProcess: NodeChildProcess.ChildProcess | null = null;
let localRuntime: { address: string; token: string } | null = null;

async function startLocalNode() {
  const binary = resolveNodeBinary();
  const infoPath = NodePath.join(NodeOS.tmpdir(), `isodo-node-${process.pid}.json`);
  const guestAgent = resolveSidecarAsset(
    `.tools/isodo-guest-linux-${goArch()}`,
    "isodo-guest-linux",
  );
  const t3Bundle = resolveSidecarAsset(
    "app/artifacts/sandbox/t3-server-dist.bundle",
    "t3-server-dist.bundle",
  );
  const child = NodeChildProcess.spawn(binary, [], {
    cwd: NodePath.dirname(binary),
    stdio: ["ignore", "pipe", "pipe"],
    env: {
      ...process.env,
      ISODO_LOCAL_ONLY: "true",
      ISODO_NODE_BACKEND: "lima",
      ISODO_NODE_ID: process.env.ISODO_NODE_ID ?? `desktop-${process.pid}`,
      // Without these the runtime cannot provision a Guest Agent or start T3.
      ...(guestAgent ? { ISODO_GUEST_AGENT_BINARY: guestAgent } : {}),
      ...(t3Bundle ? { ISODO_T3_SERVER_BUNDLE: t3Bundle } : {}),
      ISODO_LOCAL_API_INFO_FILE: infoPath,
      // Keep the desktop runtime's journal separate from a CLI-started Node so
      // the two never fight over the same sandbox records.
      ISODO_NODE_STATE_DIR: NodePath.join(app.getPath("userData"), "node"),
      ISODO_LOCAL_SANDBOXES_FILE: NodePath.join(app.getPath("userData"), "node", "sandboxes.json"),
    },
  });
  nodeProcess = child;
  // oxlint-disable-next-line t3code/no-global-console -- Electron main bootstrap forwards sidecar diagnostics.
  child.stdout?.on("data", (chunk: Buffer) =>
    console.info(`[isodo-node] ${chunk.toString().trim()}`),
  );
  // oxlint-disable-next-line t3code/no-global-console -- Electron main bootstrap forwards sidecar diagnostics.
  child.stderr?.on("data", (chunk: Buffer) =>
    console.error(`[isodo-node] ${chunk.toString().trim()}`),
  );
  // oxlint-disable-next-line t3code/no-global-date -- Electron bootstrap readiness deadline.
  const deadline = Date.now() + 15_000;
  // oxlint-disable-next-line t3code/no-global-date -- Electron bootstrap readiness deadline.
  while (Date.now() < deadline) {
    try {
      const content = await NodeFS.readFile(infoPath, "utf8");
      localRuntime = JSON.parse(content) as { address: string; token: string };
      return;
    } catch {
      // oxlint-disable-next-line t3code/no-global-timers -- Electron bootstrap polling.
      await new Promise((resolve) => setTimeout(resolve, 50));
    }
  }
  child.kill();
  throw new Error("isodo-node local API did not become ready");
}

async function stopLocalNode() {
  if (!nodeProcess) return;
  nodeProcess.kill();
  nodeProcess = null;
  localRuntime = null;
}

async function localRequest(path: string, init?: FileRequestInit) {
  if (!localRuntime) throw new Error("Local runtime is unavailable");
  // oxlint-disable-next-line t3code/no-global-fetch -- Electron main calls its loopback sidecar.
  const response = await fetch(`${localRuntime.address}${path}`, {
    ...init,
    headers: { Authorization: `Bearer ${localRuntime.token}`, ...(init?.headers ?? {}) },
  });
  // 204 and empty bodies are valid (delete returns no content).
  const text = await response.text();
  const body = (text ? JSON.parse(text) : null) as unknown;
  if (!response.ok) {
    const detail = typeof body === "object" && body !== null && "error" in body ? body.error : body;
    const message =
      typeof detail === "object" && detail !== null && "message" in detail
        ? String(detail.message)
        : response.statusText;
    throw new Error(message);
  }
  return body;
}

ipcMain.handle(
  "isodo:sandbox-request",
  async (_event, path: unknown, method: unknown, body: unknown, idempotencyKey: unknown) => {
    const request = validateSandboxRequest(path, method, body, idempotencyKey);
    return localRequest(request.path, {
      method: request.method,
      headers: {
        ...(request.body === undefined ? {} : { "Content-Type": "application/json" }),
        ...(request.idempotencyKey ? { "Idempotency-Key": request.idempotencyKey } : {}),
      },
      ...(request.body === undefined ? {} : { body: request.body }),
    });
  },
);

ipcMain.handle("isodo:local-runtime-status", async () => {
  if (!localRuntime) return { available: false };
  const health = (await localRequest("/health")) as { node?: unknown };
  return { available: true, node: health.node };
});
ipcMain.handle("isodo:local-templates", async () => localRequest("/v1/templates"));
ipcMain.handle("isodo:local-template-build", async (_event, templateID: unknown) => {
  if (typeof templateID !== "string" || !/^[a-z0-9][a-z0-9._-]{0,63}$/.test(templateID)) {
    throw new Error("Invalid template ID");
  }
  return localRequest(`/v1/templates/${encodeURIComponent(templateID)}/build`, { method: "POST" });
});

function sandboxIDArgument(value: unknown): string {
  if (typeof value !== "string" || !/^[a-zA-Z0-9_-]{1,128}$/.test(value)) {
    throw new Error("Invalid sandbox ID");
  }
  return value;
}

ipcMain.handle("isodo:sandbox-injections", async (_event, sandboxID: unknown) =>
  localRequest(`/v1/sandboxes/${encodeURIComponent(sandboxIDArgument(sandboxID))}/injections`),
);
ipcMain.handle(
  "isodo:set-sandbox-injections",
  async (_event, sandboxID: unknown, rules: unknown) => {
    const id = sandboxIDArgument(sandboxID);
    if (!Array.isArray(rules) || rules.length > 32) {
      throw new Error("Invalid secure-connection rules");
    }
    const body = JSON.stringify({ rules });
    if (body.length > 256 * 1024) throw new Error("Secure-connection rules are too large");
    return localRequest(`/v1/sandboxes/${encodeURIComponent(id)}/injections`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body,
    });
  },
);
ipcMain.handle("isodo:injection-summary", async () => localRequest("/injections/summary"));
ipcMain.handle("isodo:remove-persistent-injection", async (_event, ruleID: unknown) => {
  if (typeof ruleID !== "string" || !/^[a-zA-Z0-9_-]{1,128}$/.test(ruleID)) {
    throw new Error("Invalid injection rule ID");
  }
  return localRequest(`/injections/persistent/${encodeURIComponent(ruleID)}`, { method: "DELETE" });
});
const fileUploads = new SandboxFileUploads(localRequest);
const uploadOwners = new Set<number>();
ipcMain.handle("isodo:file-upload-begin", async (event, input: unknown) => {
  const owner = event.sender.id;
  if (!uploadOwners.has(owner)) {
    uploadOwners.add(owner);
    event.sender.once("destroyed", () => {
      uploadOwners.delete(owner);
      void fileUploads.closeOwner(owner);
    });
  }
  return fileUploads.begin(owner, input);
});
ipcMain.handle("isodo:file-upload-chunk", (event, id: unknown, offset: unknown, data: unknown) =>
  fileUploads.append(event.sender.id, id, offset, data),
);
ipcMain.handle("isodo:file-upload-finish", (event, id: unknown) =>
  fileUploads.finish(event.sender.id, id),
);
ipcMain.handle("isodo:file-upload-abort", (event, id: unknown) =>
  fileUploads.abort(event.sender.id, id),
);
async function createWindow() {
  const icon = resolveIcon();
  app.dock?.setIcon(icon);
  const window = new BrowserWindow({
    width: 1280,
    height: 860,
    minWidth: 720,
    minHeight: 520,
    title: "isodo",
    icon,
    show: false,
    // The app draws its own header: no title bar at all, traffic lights float
    // over the sidebar, and content runs to the top edge of the window.
    ...(process.platform === "darwin"
      ? { titleBarStyle: "hidden" as const, trafficLightPosition: { x: 16, y: 14 } }
      : {}),
    webPreferences: {
      preload: NodePath.join(__dirname, "preload.cjs"),
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: true,
    },
  });
  mainWindow = window;
  // The renderer sets document.title for the browser tab; on the desktop the
  // window has no title bar, so that update must never recreate one.
  window.on("page-title-updated", (event) => {
    event.preventDefault();
    window.setTitle("");
  });
  window.setTitle("");
  window.once("ready-to-show", () => window.show());
  window.on("closed", () => {
    mainWindow = null;
  });
  window.webContents.setWindowOpenHandler(({ url }) => {
    if (/^https?:\/\//.test(url)) void shell.openExternal(url);
    return { action: "deny" };
  });
  // A development window must always load the dev server: it owns the /api and
  // /auth proxy, so file:// would leave sign-in and every request dead.
  const devUrl =
    process.env.VITE_DEV_SERVER_URL ??
    (app.isPackaged ? undefined : (process.env.ISODO_WEB_URL ?? "http://127.0.0.1:5734"));
  // Sign-in leaves the app origin for the identity provider and then the
  // upstream provider (GitHub) before coming back to /auth/callback. Only that
  // flow may leave the app origin; every other cross-origin navigation stays
  // blocked. Without this the provider hop is dropped and the IdP page hangs on
  // its "Redirecting..." screen.
  let signingIn = false;
  window.webContents.on("will-navigate", (event, url) => {
    const target = new URL(url);
    if (target.origin === new URL(window.webContents.getURL()).origin) {
      // Only the app's own sign-in entry point arms provider navigation, and
      // landing back on the callback disarms it.
      if (target.pathname.startsWith("/auth/login")) signingIn = true;
      else if (target.pathname.startsWith("/auth/callback")) signingIn = false;
      return;
    }
    if (signingIn) return;
    event.preventDefault();
  });
  if (devUrl) {
    try {
      await window.loadURL(devUrl);
    } catch {
      await window.loadURL(
        "data:text/html;charset=utf-8," +
          encodeURIComponent(
            `<body style="font:14px -apple-system,sans-serif;padding:40px;line-height:1.7">
               <h1 style="font-size:18px">无法打开应用</h1>
               <p>桌面端需要本地 Web 服务提供 API 与登录代理，当前 ${devUrl} 没有响应。</p>
               <pre style="background:#f3f4f6;padding:12px;border-radius:8px">make web</pre>
               <p>启动后重新打开窗口即可。</p>
             </body>`,
          ),
      );
    }
  } else await window.loadFile(NodePath.join(__dirname, "../../web/dist/index.html"));
}

void app
  .whenReady()
  .then(startLocalNode)
  .then(createWindow)
  .catch((error: unknown) => {
    // oxlint-disable-next-line t3code/no-global-console -- Electron bootstrap failure is reported to its parent process.
    console.error(error);
    app.quit();
  });
const handleManagedQuit = createManagedQuit({
  pause: async () => {
    if (!nodeProcess) return;
    if (!localRuntime) throw new Error("本地运行时不可用，无法确认沙箱已暂停。");
    await pauseManagedSandboxes(localRequest);
  },
  quit: () => app.quit(),
  onFailure: async (error: unknown) => {
    const options = {
      type: "warning" as const,
      title: "沙箱尚未全部暂停",
      message: "自动暂停未完成，是否继续退出？",
      detail: `${error instanceof Error ? error.message : "本地运行时未响应。"}\n直接退出可能使部分沙箱继续运行。`,
      buttons: ["重试暂停", "取消退出", "直接退出"],
      defaultId: 0,
      cancelId: 1,
      noLink: true,
    };
    const result =
      mainWindow && !mainWindow.isDestroyed()
        ? await dialog.showMessageBox(mainWindow, options)
        : await dialog.showMessageBox(options);
    return result.response === 0 ? "retry" : result.response === 2 ? "force" : "cancel";
  },
});
app.on("before-quit", (event) => {
  void handleManagedQuit(event)?.catch((error: unknown) => {
    console.error("Isodo shutdown did not complete", error);
    dialog.showErrorBox("退出未完成", "请重试退出，或在沙箱列表中暂停本地沙箱后退出。");
  });
});
// Keep Node alive if a window cancels closing because it has unsaved work.
app.on("will-quit", () => {
  void stopLocalNode().catch((error: unknown) => {
    console.error("Isodo Node shutdown failed", error);
  });
});
app.on("activate", () => {
  if (!mainWindow) void createWindow();
});
app.on("window-all-closed", () => {
  // oxlint-disable-next-line t3code/no-global-process-runtime -- Standalone Electron bootstrap has no Effect host runtime.
  if (process.platform !== "darwin") app.quit();
});
