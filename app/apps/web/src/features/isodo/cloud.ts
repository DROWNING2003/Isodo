import { remoteSandboxesOnly } from "./sandboxCapabilities";
import { useSyncExternalStore } from "react";
import { createMockCloudService } from "@t3tools/sandbox-client/cloud";

// Replace this adapter with the cloud control plane; UI and remote T3 protocol stay separate.
export const cloud = createMockCloudService();
export const useCloudState = () => {
  const snapshot = useSyncExternalStore(cloud.subscribe, cloud.getSnapshot);
  return remoteSandboxesOnly() ? { ...snapshot, sandboxes: [] } : snapshot;
};
