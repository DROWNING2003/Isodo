import type { InjectionById } from "@t3tools/shared/sandbox";
import {
  AlertCircleIcon,
  CheckCircle2Icon,
  KeyRoundIcon,
  LoaderCircleIcon,
  PlusIcon,
  Trash2Icon,
} from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import { createSandboxApi } from "@t3tools/sandbox-client";
import { useSandboxCredentials } from "../../hooks/useSandboxCredentials";
import {
  createSandboxInjectionDrafts,
  getConfiguredSandboxInjections,
  serializeSandboxInjectionDrafts,
  type EditableSandboxInjectionType,
  type SandboxInjectionDraft,
  validateSandboxInjectionDrafts,
} from "../../lib/sandboxInjections";
import { Button } from "../ui/button";
import {
  Dialog,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogPanel,
  DialogPopup,
  DialogTitle,
} from "../ui/dialog";
import { Input } from "../ui/input";
import { Select, SelectItem, SelectPopup, SelectTrigger, SelectValue } from "../ui/select";

interface Props {
  readonly sandboxID: string;
}

type SaveState = "idle" | "loading" | "saving" | "error";

const TYPE_LABELS: Readonly<Record<EditableSandboxInjectionType, string>> = {
  http: "HTTP",
  openai: "OpenAI",
  anthropic: "Anthropic",
  gemini: "Gemini",
  qiniu: "Qiniu",
  github: "GitHub",
};

function newDraft(id: number): SandboxInjectionDraft {
  return {
    id,
    type: "http",
    baseUrl: "",
    secret: "",
    header: "Authorization",
    wasMasked: false,
  };
}

export function SandboxInjectionsControl({ sandboxID }: Props) {
  const { credentials, hasRequired, isLoaded } = useSandboxCredentials();
  const [open, setOpen] = useState(false);
  const [entries, setEntries] = useState<SandboxInjectionDraft[]>([]);
  const [fixed, setFixed] = useState<InjectionById[]>([]);
  const [runtimeCount, setRuntimeCount] = useState(0);
  const [saveState, setSaveState] = useState<SaveState>("idle");
  const [errorMessage, setErrorMessage] = useState<string | null>(null);
  const nextId = useRef(0);
  const configuredInjections = useMemo(
    () => getConfiguredSandboxInjections(credentials),
    [credentials],
  );
  const api = useMemo(
    () =>
      createSandboxApi({
        apiKey: credentials.e2bApiKey,
        apiUrl: credentials.e2bApiUrl,
      }),
    [credentials.e2bApiKey, credentials.e2bApiUrl],
  );

  const load = useCallback(async () => {
    setSaveState("loading");
    setErrorMessage(null);
    try {
      const runtimeInjections = await api.getInjections(sandboxID);
      const result = createSandboxInjectionDrafts(runtimeInjections, configuredInjections);
      setEntries([...result.drafts]);
      setFixed([...result.fixed]);
      setRuntimeCount(runtimeInjections.length);
      nextId.current = result.drafts.reduce((max, entry) => Math.max(max, entry.id + 1), 0);
      setSaveState("idle");
    } catch (error) {
      setSaveState("error");
      setErrorMessage(error instanceof Error ? error.message : "读取运行时注入失败。");
    }
  }, [api, configuredInjections, sandboxID]);

  useEffect(() => {
    if (open) void load();
  }, [load, open]);

  const updateEntry = useCallback(
    (id: number, field: keyof SandboxInjectionDraft, value: string) => {
      setSaveState("idle");
      setErrorMessage(null);
      setEntries((current) =>
        current.map((entry) => (entry.id === id ? { ...entry, [field]: value } : entry)),
      );
    },
    [],
  );

  const updateType = useCallback((id: number, type: EditableSandboxInjectionType) => {
    setSaveState("idle");
    setErrorMessage(null);
    setEntries((current) =>
      current.map((entry) =>
        entry.id === id
          ? {
              ...entry,
              type,
              baseUrl: type === "http" ? entry.baseUrl : "",
              secret: "",
              header: type === "http" ? entry.header || "Authorization" : "",
              wasMasked: false,
            }
          : entry,
      ),
    );
  }, []);

  const save = useCallback(async () => {
    const validationError = validateSandboxInjectionDrafts(entries);
    if (validationError) {
      setSaveState("error");
      setErrorMessage(validationError);
      return;
    }

    setSaveState("saving");
    setErrorMessage(null);
    try {
      const injections = serializeSandboxInjectionDrafts(entries, fixed);
      await api.updateInjections(sandboxID, injections);
      setRuntimeCount(injections.length);
      setOpen(false);
      setSaveState("idle");
    } catch (error) {
      setSaveState("error");
      setErrorMessage(error instanceof Error ? error.message : "更新运行时注入失败。");
    }
  }, [api, entries, fixed, sandboxID]);

  return (
    <>
      <Button
        size="icon-sm"
        variant="ghost"
        aria-label="管理沙箱注入"
        title={hasRequired ? "管理沙箱注入" : "请先配置 Sandbox API Key"}
        disabled={!isLoaded || !hasRequired}
        onClick={() => setOpen(true)}
        className="text-muted-foreground hover:text-foreground"
      >
        <KeyRoundIcon className="size-3.5" />
      </Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogPopup className="max-w-2xl overflow-hidden">
          <DialogHeader>
            <DialogTitle>沙箱运行时注入</DialogTitle>
            <DialogDescription>
              修改会立即应用到当前沙箱的新 HTTPS 请求。保存会完整替换运行时注入规则。
            </DialogDescription>
          </DialogHeader>
          <DialogPanel className="space-y-4">
            <div className="flex items-center justify-between gap-3 rounded-lg border border-border/60 bg-muted/20 px-3 py-2.5 text-xs">
              <span className="text-muted-foreground">Sandbox ID</span>
              <span className="truncate font-mono text-foreground">{sandboxID}</span>
            </div>
            {saveState === "loading" ? (
              <div
                className="flex items-center gap-2 py-8 text-sm text-muted-foreground"
                role="status"
              >
                <LoaderCircleIcon className="size-4 animate-spin" />
                读取运行时注入...
              </div>
            ) : (
              <>
                <div className="flex items-center justify-between gap-3">
                  <div>
                    <p className="text-sm font-semibold">Provider 注入</p>
                    <p className="mt-0.5 text-xs text-muted-foreground">
                      当前运行中 {runtimeCount} 条；敏感值不会从服务端回显。
                    </p>
                  </div>
                  <Button
                    size="sm"
                    variant="ghost"
                    onClick={() => {
                      setEntries((current) => [...current, newDraft(nextId.current++)]);
                      setSaveState("idle");
                      setErrorMessage(null);
                    }}
                  >
                    <PlusIcon className="size-3.5" />
                    添加
                  </Button>
                </div>
                {entries.length === 0 ? (
                  <div className="border-l-2 border-border/70 px-3 py-3 text-sm text-muted-foreground">
                    暂无可编辑注入；点击“添加”创建一条规则。
                  </div>
                ) : (
                  <div className="space-y-3">
                    {entries.map((entry, index) => (
                      <div key={entry.id} className="rounded-lg border border-border/60 p-3">
                        <div className="flex items-center justify-between gap-3">
                          <p className="text-xs font-medium">注入 {index + 1}</p>
                          <Button
                            size="icon-xs"
                            variant="ghost"
                            aria-label={`删除注入 ${index + 1}`}
                            title="删除注入"
                            onClick={() =>
                              setEntries((current) =>
                                current.filter((item) => item.id !== entry.id),
                              )
                            }
                            className="text-muted-foreground hover:text-destructive"
                          >
                            <Trash2Icon />
                          </Button>
                        </div>
                        <div className="mt-3 grid gap-3 sm:grid-cols-[8rem_minmax(0,1fr)]">
                          <Select
                            value={entry.type}
                            onValueChange={(value) => {
                              if (value)
                                updateType(entry.id, value as EditableSandboxInjectionType);
                            }}
                          >
                            <SelectTrigger size="sm" aria-label={`注入 ${index + 1} 类型`}>
                              <SelectValue>{TYPE_LABELS[entry.type]}</SelectValue>
                            </SelectTrigger>
                            <SelectPopup alignItemWithTrigger={false}>
                              {Object.entries(TYPE_LABELS).map(([value, label]) => (
                                <SelectItem key={value} value={value}>
                                  {label}
                                </SelectItem>
                              ))}
                            </SelectPopup>
                          </Select>
                          <Input
                            size="sm"
                            type="url"
                            value={entry.baseUrl}
                            onChange={(event) =>
                              updateEntry(entry.id, "baseUrl", event.target.value)
                            }
                            placeholder={
                              entry.type === "http"
                                ? "https://api.example.com"
                                : "默认 Base URL（可选）"
                            }
                            aria-label={`注入 ${index + 1} Base URL`}
                            spellCheck={false}
                          />
                        </div>
                        <div className="mt-3 grid gap-3 sm:grid-cols-[minmax(0,1fr)_minmax(0,1.4fr)]">
                          {entry.type === "http" ? (
                            <Input
                              size="sm"
                              value={entry.header}
                              onChange={(event) =>
                                updateEntry(entry.id, "header", event.target.value)
                              }
                              placeholder="Authorization"
                              aria-label={`注入 ${index + 1} 请求头`}
                            />
                          ) : (
                            <div className="flex items-center rounded-lg border border-dashed border-border/70 px-3 text-xs text-muted-foreground">
                              {entry.type === "github" ? "GitHub Token" : "API Key"}
                            </div>
                          )}
                          <Input
                            size="sm"
                            type="password"
                            value={entry.secret}
                            onChange={(event) =>
                              updateEntry(entry.id, "secret", event.target.value)
                            }
                            placeholder={entry.wasMasked ? "重新输入以保留此注入" : "输入密钥"}
                            aria-label={`注入 ${index + 1} 密钥`}
                            autoComplete="off"
                          />
                        </div>
                      </div>
                    ))}
                  </div>
                )}
              </>
            )}
            {errorMessage ? (
              <div className="flex items-start gap-2 text-xs text-destructive" role="alert">
                <AlertCircleIcon className="mt-0.5 size-3.5 shrink-0" />
                <span className="min-w-0 break-words">{errorMessage}</span>
              </div>
            ) : null}
          </DialogPanel>
          <DialogFooter>
            <Button variant="ghost" size="sm" onClick={() => setOpen(false)}>
              取消
            </Button>
            <Button
              size="sm"
              onClick={() => void save()}
              disabled={saveState === "loading" || saveState === "saving"}
            >
              {saveState === "saving" ? (
                <LoaderCircleIcon className="size-3.5 animate-spin" />
              ) : (
                <CheckCircle2Icon className="size-3.5" />
              )}
              {saveState === "saving" ? "更新中..." : "立即应用"}
            </Button>
          </DialogFooter>
        </DialogPopup>
      </Dialog>
    </>
  );
}
