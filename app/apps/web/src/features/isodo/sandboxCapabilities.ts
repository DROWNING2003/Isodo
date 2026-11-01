/** Hosted Web builds have no local runtime, including cached desktop state. */
export function remoteSandboxesOnly(): boolean {
  return import.meta.env.VITE_ISODO_REMOTE_ONLY === "true";
}

export function localSandboxAvailable(): boolean {
  return !remoteSandboxesOnly() && typeof window !== "undefined" && Boolean(window.isodoDesktop);
}
