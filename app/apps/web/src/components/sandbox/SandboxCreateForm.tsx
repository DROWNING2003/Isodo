import {
  BOUNDLY_SANDBOX_METADATA,
  type SandboxCreateInput,
  type SandboxInjection,
} from "@t3tools/shared/sandbox";
import { LoaderIcon } from "lucide-react";
import { useState } from "react";

import { useSandboxCredentials } from "../../hooks/useSandboxCredentials";
import { useSandboxApi } from "../../hooks/useSandboxApi";
import { getAdditionalInjections } from "../../lib/sandboxCredentialStore";
import {
  formatSandboxConnectionError,
  sandboxConnectionStageLabel,
  type SandboxConnectionStage,
} from "../../lib/sandboxConnection";
import { setActiveEnvironmentId } from "../../state/entities";
import { Button } from "../ui/button";
import { Card, CardHeader, CardTitle, CardDescription, CardPanel, CardFooter } from "../ui/card";
import { Input } from "../ui/input";

interface Props {
  readonly onSuccess: () => void;
  readonly onCancel: () => void;
}

export function SandboxCreateForm({ onSuccess, onCancel }: Props) {
  const creds = useSandboxCredentials();
  const { api, connect } = useSandboxApi(creds.credentials);
  const [timeoutHours, setTimeoutHours] = useState("3");
  const [githubRepo, setGithubRepo] = useState("");
  const [githubToken, setGithubToken] = useState("");
  const [mountPath, setMountPath] = useState("");
  const [name, setName] = useState("");
  const [createdSandboxID, setCreatedSandboxID] = useState<string | null>(null);
  const [status, setStatus] = useState<"idle" | "creating" | "error">("idle");
  const [connectionStage, setConnectionStage] = useState<SandboxConnectionStage | null>(null);
  const [errorMessage, setErrorMessage] = useState<string | null>(null);

  const handleCreate = async () => {
    if (!creds.hasRequired) return;
    setStatus("creating");
    setConnectionStage(null);
    setErrorMessage(null);
    try {
      const hours = Math.max(1, parseInt(timeoutHours, 10) || 3);
      let sandboxID = createdSandboxID;
      if (sandboxID === null) {
        const injections: SandboxInjection[] = [];
        if (creds.credentials.openaiApiKey) {
          injections.push({
            type: "openai",
            api_key: creds.credentials.openaiApiKey,
            ...(creds.credentials.openaiBaseUrl
              ? { base_url: creds.credentials.openaiBaseUrl }
              : {}),
          } as SandboxInjection);
        }
        for (const inj of getAdditionalInjections()) injections.push(inj);
        const input: SandboxCreateInput = {
          templateID: creds.credentials.templateID,
          timeout: hours * 3600,
          autoPause: true,
          network: { allowPublicTraffic: true },
          metadata: {
            ...BOUNDLY_SANDBOX_METADATA,
            ...(name.trim() ? { name: name.trim() } : {}),
          },
          ...(injections.length > 0 ? { injections } : {}),
        };
        if (githubRepo.trim()) {
          input.resources = [
            {
              type: "github_repository",
              url: `https://github.com/${githubRepo.trim()}`,
              mount_path: mountPath.trim() || "/home/user/project",
              authorization_token: githubToken.trim() || "",
            },
          ];
        }
        const sandbox = await api.create(input);
        sandboxID = sandbox.sandboxID;
        setCreatedSandboxID(sandboxID);
      }
      const connected = await api.connect(sandboxID, 3600);
      const environmentId = await connect(connected, setConnectionStage);
      setActiveEnvironmentId(environmentId);
      onSuccess();
    } catch (error) {
      setStatus("error");
      setErrorMessage(formatSandboxConnectionError(error));
    }
  };

  if (!creds.isLoaded) {
    return (
      <div className="flex items-center justify-center py-12">
        <LoaderIcon className="size-5 animate-spin text-muted-foreground" />
      </div>
    );
  }

  if (!creds.hasRequired) {
    return (
      <div className="mx-auto flex max-w-md flex-col gap-4 p-6 pt-10 text-center">
        <p className="text-sm text-muted-foreground">请先在设置中配置沙箱凭证</p>
      </div>
    );
  }

  return (
    <div className="mx-auto flex w-full max-w-lg flex-col gap-4 p-6 pt-10">
      <Card>
        <CardHeader>
          <CardTitle>创建沙箱</CardTitle>
          <CardDescription>输入超时时间和可选的 GitHub 仓库</CardDescription>
        </CardHeader>
        <CardPanel>
          <div className="flex flex-col gap-4">
            <div>
              <label className="mb-1.5 block text-sm font-medium">名称</label>
              <Input
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="可选，方便识别沙箱"
              />
            </div>
            <div>
              <label className="mb-1.5 block text-sm font-medium">超时时间（小时）</label>
              <Input
                type="number"
                min={1}
                max={24}
                value={timeoutHours}
                onChange={(e) => setTimeoutHours(e.target.value)}
              />
            </div>
            <div>
              <label className="mb-1.5 block text-sm font-medium">GitHub 仓库（可选）</label>
              <Input
                value={githubRepo}
                onChange={(e) => setGithubRepo(e.target.value)}
                placeholder="owner/repo"
              />
              <p className="mt-1 text-xs text-muted-foreground">
                自动克隆到沙箱中的 /home/user/project
              </p>
            </div>
            {githubRepo.trim() && (
              <>
                <div>
                  <label className="mb-1.5 block text-sm font-medium">挂载路径</label>
                  <Input
                    value={mountPath}
                    onChange={(e) => setMountPath(e.target.value)}
                    placeholder="/home/user/project"
                  />
                </div>
                <div>
                  <label className="mb-1.5 block text-sm font-medium">GitHub Token</label>
                  <Input
                    type="password"
                    value={githubToken}
                    onChange={(e) => setGithubToken(e.target.value)}
                    placeholder="ghp_..."
                  />
                </div>
              </>
            )}
          </div>
        </CardPanel>
        <CardFooter>
          {status === "creating" ? (
            <div className="flex items-center gap-2 text-sm text-muted-foreground" role="status">
              <LoaderIcon className="size-4 animate-spin" />
              {createdSandboxID
                ? connectionStage
                  ? sandboxConnectionStageLabel(connectionStage)
                  : "连接沙箱..."
                : "正在创建并启动沙箱..."}
            </div>
          ) : status === "error" ? (
            <p className="max-w-[24rem] break-words text-sm text-destructive" role="alert">
              {errorMessage ?? (createdSandboxID ? "连接失败，可重试连接" : "创建失败，请重试")}
            </p>
          ) : null}
          <div className="ml-auto flex items-center gap-2">
            <Button variant="outline" size="sm" onClick={onCancel} disabled={status === "creating"}>
              取消
            </Button>
            <Button
              size="sm"
              onClick={handleCreate}
              disabled={status === "creating" || !creds.hasRequired}
            >
              {createdSandboxID ? "重试连接" : "创建"}
            </Button>
          </div>
        </CardFooter>
      </Card>
    </div>
  );
}
