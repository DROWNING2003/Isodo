import { useEnvironments } from "../state/environments";
import { ConnectionSettingsPanel } from "../features/isodo/ConnectionSettingsPanel";
import {
  AppearanceSettingsPanel,
  GeneralSettingsPanel,
} from "../components/settings/SettingsPanels";
import { KeybindingsSettingsPanel } from "../components/settings/KeybindingsSettings";
import { SettingsScopeProvider } from "../components/settings/SettingsScopeContext";
import type { SettingsScopeSearch } from "../components/settings/settingsScope";
import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { Card, CardHeader, CardPanel, CardTitle } from "../components/ui/card";
import { Button } from "../components/ui/button";
import { CloudPage } from "../features/isodo/CloudPages";
import { useState } from "react";
import { signOut, useAuth } from "../features/isodo/auth";

export const Route = createFileRoute("/developer/settings")({
  validateSearch: (search): { section?: string } => ({
    section: ["账号", "连接", "通用", "外观", "快捷键"].includes(String(search.section))
      ? String(search.section)
      : "账号",
  }),
  component: DeveloperSettingsPage,
});

function DeveloperSettingsPage() {
  const { user: account } = useAuth();
  const [error, setError] = useState<string | null>(null);
  const navigate = useNavigate();
  const { section } = Route.useSearch();
  const tab = section ?? "账号";
  const setTab = (section: string) => {
    void navigate({ to: "/developer/settings", search: { section } });
  };
  const { environments } = useEnvironments();
  const [scope, setScope] = useState<SettingsScopeSearch>({});

  return (
    <CloudPage title="设置">
      <nav aria-label="设置分类" className="flex flex-wrap gap-2">
        {["账号", "连接", "通用", "外观", "快捷键"].map((item) => (
          <Button
            key={item}
            variant={tab === item ? "default" : "outline"}
            onClick={() => setTab(item)}
            aria-pressed={tab === item}
          >
            {item}
          </Button>
        ))}
      </nav>
      {(tab === "通用" || tab === "快捷键") && (
        <label className="flex items-center gap-3 text-sm">
          设置范围
          <select
            aria-label="设置范围"
            className="rounded-md border px-3 py-2"
            value={scope.machine ?? ""}
            onChange={(event) =>
              setScope(event.target.value ? { machine: event.target.value } : {})
            }
          >
            <option value="">全部环境</option>
            {environments.map((environment) => (
              <option key={environment.environmentId} value={environment.environmentId}>
                {environment.label}
              </option>
            ))}
          </select>
        </label>
      )}
      <SettingsScopeProvider search={scope} onChange={setScope}>
        {tab === "连接" && <ConnectionSettingsPanel />}
        {tab === "通用" && <GeneralSettingsPanel />}
        {tab === "外观" && <AppearanceSettingsPanel />}
        {tab === "快捷键" && <KeybindingsSettingsPanel />}
      </SettingsScopeProvider>
      {tab === "账号" && (
        <>
          <section className="space-y-2">
            <h1 className="text-2xl font-semibold tracking-tight">账号设置</h1>
            <p className="text-sm text-muted-foreground">你的账号、语言与工作空间偏好。</p>
          </section>
          <Card className="max-w-2xl shadow-none">
            <CardHeader>
              <CardTitle className="text-base">账号</CardTitle>
            </CardHeader>
            <CardPanel className="flex items-center justify-between gap-4 text-sm">
              <div className="flex min-w-0 items-center gap-3">
                <span className="inline-flex size-10 shrink-0 items-center justify-center rounded-full border border-border bg-muted font-medium">
                  {account?.name.slice(0, 1).toUpperCase() ?? "?"}
                </span>
                <div className="min-w-0">
                  <p className="truncate font-medium">{account?.name ?? "未登录"}</p>
                  <p className="mt-1 text-muted-foreground">
                    {account ? `${account.provider}` : "个人账号"}
                  </p>
                </div>
              </div>
              <Button
                size="sm"
                variant={account ? "outline" : "default"}
                onClick={() => {
                  if (account)
                    void signOut()
                      .then(() => navigate({ to: "/sandboxes/login" }))
                      .catch(() => setError("退出登录失败，请重试"));
                  else void navigate({ to: "/sandboxes/login" });
                }}
              >
                {account ? "退出登录" : "前往登录"}
              </Button>
            </CardPanel>
          </Card>
        </>
      )}
      {error && (
        <p role="alert" className="text-sm text-destructive">
          {error}
        </p>
      )}
    </CloudPage>
  );
}
