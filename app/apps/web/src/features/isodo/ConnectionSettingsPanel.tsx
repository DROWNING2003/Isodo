import { useLocalSandboxes } from "./sandboxQueries";
import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { useEnvironments } from "../../state/environments";
import { useAtomCommand } from "../../state/use-atom-command";
import { environmentCatalog } from "../../connection/catalog";
import { Button } from "../../components/ui/button";
import { Card, CardPanel } from "../../components/ui/card";
import {
  useClientSettings,
  useClientSettingsHydrated,
  useUpdateClientSettings,
} from "../../hooks/useSettings";
import { sandboxIdForEnvironment } from "./sandboxEnvironmentMap";
import { useSandboxConnections } from "./useSandboxConnections";

/** Connection controls use the same registry as tasks; opening settings has no side effects. */
export function ConnectionSettingsPanel() {
  const { environments: savedEnvironments } = useEnvironments();
  const local = useLocalSandboxes();
  const environments = savedEnvironments.map((environment) => ({
    ...environment,
    label:
      local.data?.find(
        (sandbox) => sandbox.id === sandboxIdForEnvironment(environment.environmentId),
      )?.name || environment.label,
  }));
  const connections = useSandboxConnections();
  const remove = useAtomCommand(environmentCatalog.remove, { reportFailure: false });
  const retry = useAtomCommand(environmentCatalog.retryNow, { reportFailure: false });
  const settings = useClientSettings();
  const hydrated = useClientSettingsHydrated();
  const update = useUpdateClientSettings();
  const [error, setError] = useState<string | null>(null);
  const [pending, setPending] = useState<string | null>(null);
  return (
    <section className="space-y-6">
      <div>
        <h2 className="text-lg font-semibold">沙箱连接</h2>
        <p className="mt-1 text-sm text-muted-foreground">
          暂停并断开后保留沙箱磁盘、任务记录和文件。可以从沙箱列表重新启动。
        </p>
      </div>
      {!environments.length && <p className="text-sm text-muted-foreground">还没有保存的连接。</p>}
      {environments.map((environment) => {
        const id = environment.environmentId;
        const sandboxId = sandboxIdForEnvironment(id);
        const phase = environment.connection.phase;
        const status =
          phase === "connected" ? "已连接" : phase === "available" ? "已断开" : "正在连接或重试";
        return (
          <Card key={id} className="shadow-none">
            <CardPanel className="space-y-3 pt-4">
              <div className="flex flex-wrap items-center justify-between gap-3">
                <div className="min-w-0">
                  <p className="break-all font-medium">{environment.label}</p>
                  <p className="text-sm text-muted-foreground">{status}</p>
                </div>
                <div className="flex gap-2">
                  {sandboxId ? (
                    <>
                      {connections.canDisconnectSandbox(sandboxId) && (
                        <Button
                          variant="outline"
                          disabled={connections.disconnecting !== null}
                          onClick={() => void connections.pauseAndDisconnect(sandboxId)}
                        >
                          暂停并断开
                        </Button>
                      )}
                      <Button
                        variant="outline"
                        render={<Link to="/sandboxes/$sandboxId" params={{ sandboxId }} />}
                      >
                        {phase === "connected" ? "进入沙箱" : "重新连接"}
                      </Button>
                    </>
                  ) : (
                    <Button
                      variant="outline"
                      disabled={pending === id}
                      onClick={async () => {
                        setPending(id);
                        const result = await retry(id);
                        setPending(null);
                        if (result._tag !== "Success") setError("连接失败，请检查服务是否运行。");
                      }}
                    >
                      重试
                    </Button>
                  )}
                  {environment.entry.target._tag !== "PrimaryConnectionTarget" && (
                    <Button
                      variant="ghost"
                      disabled={pending === id}
                      onClick={async () => {
                        if (
                          !window.confirm(
                            "移除保存的连接？此操作会清除该连接在本机的配对和缓存，包括未发送的草稿；沙箱和文件仍保留。",
                          )
                        )
                          return;
                        setPending(id);
                        const result = await remove(id);
                        setPending(null);
                        if (result._tag !== "Success") setError("移除连接失败，请重试。");
                      }}
                    >
                      移除
                    </Button>
                  )}
                </div>
              </div>
            </CardPanel>
          </Card>
        );
      })}
      {(error || connections.error) && (
        <p role="alert" className="text-sm text-destructive">
          {error || connections.error}
        </p>
      )}
      <div className="space-y-3 border-t pt-5">
        <h2 className="text-lg font-semibold">任务分配</h2>
        <label className="flex items-center gap-2 text-sm">
          <input
            type="checkbox"
            checked={settings.loadBalancingEnabled}
            disabled={!hydrated}
            onChange={(event) => void update({ loadBalancingEnabled: event.target.checked })}
          />
          为共享项目的新任务自动选择运行环境
        </label>
        {environments.map((environment) => (
          <label
            key={environment.environmentId}
            className="flex flex-wrap items-center justify-between gap-3 text-sm"
          >
            <span>{environment.label}</span>
            <select
              aria-label={`${environment.label} 分配偏好`}
              className="rounded-md border px-3 py-2"
              disabled={!hydrated || !settings.loadBalancingEnabled}
              value={settings.loadBalancingWeights[environment.environmentId] ?? 50}
              onChange={(event) =>
                void update({
                  loadBalancingWeights: {
                    ...settings.loadBalancingWeights,
                    [environment.environmentId]: Number(event.target.value),
                  },
                })
              }
            >
              <option value={100}>优先使用</option>
              <option value={50}>正常</option>
              <option value={25}>较少使用</option>
              <option value={0}>仅手动选择</option>
            </select>
          </label>
        ))}
      </div>
    </section>
  );
}
