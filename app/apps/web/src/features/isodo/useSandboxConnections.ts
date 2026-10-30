import { pauseSandbox } from "./pauseSandbox";
import { invalidateSandboxLists } from "./sandboxQueries";
import { useRef, useState } from "react";
import { setSandboxEnvironmentConnected } from "./sandboxConnectionCommands";
import { setActiveEnvironmentId, useActiveEnvironmentId } from "../../state/entities";
import { useEnvironments } from "../../state/environments";
import { useAtomCommand } from "../../state/use-atom-command";
import { environmentCatalog } from "../../connection/catalog";
import {
  environmentIdForSandbox,
  environmentIdsForSandbox,
  forgetSandboxEnvironment,
  isSandboxEnvironmentDisconnected,
  setSandboxEnvironmentDisconnected,
  useSandboxConnectionVersion,
} from "./sandboxEnvironmentMap";

/** Connection actions are independent of the route showing a sandbox. */
export function useSandboxConnections() {
  useSandboxConnectionVersion();
  const { environments } = useEnvironments();
  const activeId = useActiveEnvironmentId();
  const setConnected = useAtomCommand(setSandboxEnvironmentConnected, { reportFailure: false });
  const removeEnvironment = useAtomCommand(environmentCatalog.remove, { reportFailure: false });
  const [disconnecting, setDisconnecting] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const pending = useRef(false);
  const registeredIds = environments.map((entry) => entry.environmentId);

  const disconnect = async (sandboxId: string): Promise<boolean> => {
    if (pending.current) return false;
    pending.current = true;
    setDisconnecting(sandboxId);
    setError(null);
    try {
      for (const environmentId of environmentIdsForSandbox(sandboxId, registeredIds)) {
        const wasDisconnected = isSandboxEnvironmentDisconnected(environmentId);
        setSandboxEnvironmentDisconnected(environmentId, true);
        try {
          const result = await setConnected({ environmentId, connected: false });
          if (result._tag !== "Success") throw new Error("断开连接失败，请重试");
        } catch (reason) {
          setSandboxEnvironmentDisconnected(environmentId, wasDisconnected);
          throw reason;
        }
        if (environmentId === activeId) setActiveEnvironmentId(null);
      }
      return true;
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "断开连接失败，请重试");
      return false;
    } finally {
      pending.current = false;
      setDisconnecting(null);
    }
  };

  const pauseAndDisconnect = async (sandboxId: string): Promise<boolean> => {
    if (pending.current) return false;
    pending.current = true;
    setDisconnecting(sandboxId);
    setError(null);
    try {
      await pauseSandbox(sandboxId);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "暂停沙箱失败，请重试。");
      pending.current = false;
      setDisconnecting(null);
      return false;
    }
    pending.current = false;
    for (const environmentId of environmentIdsForSandbox(sandboxId, registeredIds))
      setSandboxEnvironmentDisconnected(environmentId, true);
    const disconnected = await disconnect(sandboxId);
    invalidateSandboxLists();
    return disconnected;
  };

  const removeForSandbox = async (sandboxId: string): Promise<boolean> => {
    if (pending.current) {
      setError("连接正在处理中，请稍后在设置中移除。");
      return false;
    }
    pending.current = true;
    setDisconnecting(sandboxId);
    setError(null);
    let allRemoved = true;
    try {
      for (const environmentId of environmentIdsForSandbox(sandboxId, registeredIds)) {
        const result = await removeEnvironment(environmentId);
        if (result._tag !== "Success") {
          allRemoved = false;
          continue;
        }
        forgetSandboxEnvironment(environmentId);
        if (environmentId === activeId) setActiveEnvironmentId(null);
      }
      if (!allRemoved) setError("沙箱已删除，但部分本机连接未能移除，请在设置中重试。");
      return allRemoved;
    } finally {
      pending.current = false;
      setDisconnecting(null);
    }
  };

  return {
    environmentForSandbox: (sandboxId: string) =>
      environmentIdForSandbox(
        sandboxId,
        environments
          .filter(
            (entry) =>
              entry.connection.phase === "connected" &&
              !isSandboxEnvironmentDisconnected(entry.environmentId),
          )
          .map((entry) => entry.environmentId),
      ),
    canDisconnectSandbox: (sandboxId: string) =>
      environmentIdsForSandbox(
        sandboxId,
        environments
          .filter(
            (entry) =>
              entry.connection.phase !== "available" &&
              !isSandboxEnvironmentDisconnected(entry.environmentId),
          )
          .map((entry) => entry.environmentId),
      ).length > 0,
    disconnect,
    pauseAndDisconnect,
    removeForSandbox,
    disconnecting,
    error,
  };
}
