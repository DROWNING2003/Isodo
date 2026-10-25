export type WebSurface = "full" | "sandbox";

export function resolveWebSurface(value: string | undefined): WebSurface {
  // "sandbox" is the surface served from inside a sandbox and is the default.
  // The desktop client opts into "full" so a paired sandbox lands on the real
  // workbench instead of being bounced back to the sandbox list.
  return value?.trim() === "full" ? "full" : "sandbox";
}

export const webSurface = resolveWebSurface(import.meta.env.VITE_BOUNDLY_WEB_SURFACE);
export const isSandboxOnlyWeb = webSurface === "sandbox";
