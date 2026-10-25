import { contextBridge, ipcRenderer } from "electron";

contextBridge.exposeInMainWorld("isodoDesktop", {
  localRuntimeStatus: () => ipcRenderer.invoke("isodo:local-runtime-status"),
  createLocalSandbox: (input: { name: string }) =>
    ipcRenderer.invoke("isodo:local-sandbox-create", input),
  getLocalOperation: (operationID: string) =>
    ipcRenderer.invoke("isodo:local-operation", operationID),
  getLocalSandboxApp: (sandboxID: string) =>
    ipcRenderer.invoke("isodo:local-sandbox-app", sandboxID),
  listLocalSandboxes: () => ipcRenderer.invoke("isodo:local-sandboxes"),
  deleteLocalSandbox: (sandboxID: string) =>
    ipcRenderer.invoke("isodo:local-sandbox-delete", sandboxID),
  startLocalSandbox: (sandboxID: string) =>
    ipcRenderer.invoke("isodo:local-sandbox-start", sandboxID),
  stopLocalSandbox: (sandboxID: string) =>
    ipcRenderer.invoke("isodo:local-sandbox-stop", sandboxID),
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
  writeAgentConfig: (sandboxID: string, input: { path: string; content: string }) =>
    ipcRenderer.invoke("isodo:write-agent-config", sandboxID, input),
});
