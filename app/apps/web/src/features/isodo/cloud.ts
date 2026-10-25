import { useSyncExternalStore } from "react";
import { createMockCloudService } from "@t3tools/sandbox-client/cloud";

// Replace this adapter with the cloud control plane; UI and remote T3 protocol stay separate.
export const cloud = createMockCloudService();
export const useCloudState = () => useSyncExternalStore(cloud.subscribe, cloud.getSnapshot);
