import { contextBridge, ipcRenderer } from "electron";

const trustedDesktopOrigin =
  location.protocol === "file:" ||
  ["http://127.0.0.1:5734", "http://app.isodo.localhost:5734"].includes(location.origin);
if (trustedDesktopOrigin) contextBridge.exposeInMainWorld("isodoDesktop", {
  sandboxRequest: (path: string, method: string, body?: string, idempotencyKey?: string) =>
    ipcRenderer.invoke("isodo:sandbox-request", path, method, body, idempotencyKey),
  localRuntimeStatus: () => ipcRenderer.invoke("isodo:local-runtime-status"),
  listLocalTemplates: () => ipcRenderer.invoke("isodo:local-templates"),
  buildLocalTemplate: (templateID: string) =>
    ipcRenderer.invoke("isodo:local-template-build", templateID),
  getSandboxInjections: (sandboxID: string) =>
    ipcRenderer.invoke("isodo:sandbox-injections", sandboxID),
  setSandboxInjections: (sandboxID: string, rules: readonly unknown[]) =>
    ipcRenderer.invoke("isodo:set-sandbox-injections", sandboxID, rules),
  getInjectionSummary: () => ipcRenderer.invoke("isodo:injection-summary"),
  removePersistentInjection: (ruleID: string) =>
    ipcRenderer.invoke("isodo:remove-persistent-injection", ruleID),
  beginFileUpload: (input: {
    sandboxId: string;
    path: string;
    size: number;
    overwrite?: boolean;
  }) => ipcRenderer.invoke("isodo:file-upload-begin", input),
  appendFileUpload: (id: string, offset: number, data: Uint8Array) =>
    ipcRenderer.invoke("isodo:file-upload-chunk", id, offset, data),
  finishFileUpload: (id: string) => ipcRenderer.invoke("isodo:file-upload-finish", id),
  abortFileUpload: (id: string) => ipcRenderer.invoke("isodo:file-upload-abort", id),
});
