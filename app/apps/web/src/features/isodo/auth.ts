import { useSyncExternalStore } from "react";
import {
  createAPIKey,
  listAPIKeys,
  revokeAPIKey,
  getSession,
  logout,
  getLoginUrl,
} from "../../api/admin.generated";
import { configureSessionTransport } from "../../api/transport";
import type { Account } from "../../api/admin.generated";
export type { Account } from "../../api/admin.generated";

interface AuthState {
  status: "loading" | "authenticated" | "unauthenticated" | "error";
  user: Account | null;
  csrfToken: string | null;
}
let state: AuthState = { status: "loading", user: null, csrfToken: null };
const listeners = new Set<() => void>();
const publish = (next: AuthState) => {
  state = next;
  listeners.forEach((listener) => listener());
};
let pending: Promise<void> | null = null;
let generation = 0;
export const getAuthGeneration = () => generation;
export const getAuthState = () => state;
export function refreshAuth(): Promise<void> {
  if (pending) return pending;
  const startedGeneration = generation;
  pending = (async () => {
    try {
      const result = await getSession();
      if (startedGeneration !== generation) return;
      publish({ status: "authenticated", user: result.user, csrfToken: result.csrf_token });
    } catch {
      if (startedGeneration !== generation) return;
      publish({ status: "error", user: null, csrfToken: null });
    } finally {
      pending = null;
    }
  })();
  return pending;
}
export const useAuth = () =>
  useSyncExternalStore(
    (listener) => {
      listeners.add(listener);
      return () => {
        listeners.delete(listener);
      };
    },
    () => state,
  );
export function signIn() {
  window.location.assign(getLoginUrl());
}
export async function signOut() {
  await logout();
  generation++;
  publish({ status: "unauthenticated", user: null, csrfToken: null });
}
export { isodoFetch as apiRequest } from "../../api/transport";
export type { APIKey as ApiKey, APIKeyScope as ApiKeyScope } from "../../api/admin.generated";
export const apiKeys = { list: listAPIKeys, create: createAPIKey, revoke: revokeAPIKey };

configureSessionTransport(
  () => state.csrfToken,
  () => {
    generation++;
    publish({ status: "unauthenticated", user: null, csrfToken: null });
  },
);
