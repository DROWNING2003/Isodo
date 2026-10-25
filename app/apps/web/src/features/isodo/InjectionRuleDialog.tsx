import { useState } from "react";
import { Button } from "../../components/ui/button";
import {
  Dialog,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogPanel,
  DialogPopup,
  DialogTitle,
} from "../../components/ui/dialog";
import { Input } from "../../components/ui/input";
import {
  Select,
  SelectItem,
  SelectPopup,
  SelectTrigger,
  SelectValue,
} from "../../components/ui/select";
import type { InjectionRuleType } from "../../api/public.generated";
import {
  ruleTarget,
  ruleTypeLabel,
  type LocalInjectionRule,
  type LocalInjectionSpec,
} from "./injectionRules";

export interface InjectionEditorValues {
  name: string;
  type: InjectionRuleType;
  baseUrl: string;
  headerName: string;
  secret: string;
  ifHeaders: Record<string, string>;
  ifQueries: Record<string, string>;
}

const TYPES: InjectionRuleType[] = ["http", "openai", "anthropic", "gemini", "qiniu", "github"];

function initialValues(input?: LocalInjectionRule): InjectionEditorValues {
  return {
    name: input?.name ?? "",
    type: input?.spec.type ?? "http",
    baseUrl: input?.spec.base_url ?? "",
    headerName: input?.spec.header_name ?? "Authorization",
    secret: "",
    ifHeaders: input?.spec.if_headers ?? {},
    ifQueries: input?.spec.if_queries ?? {},
  };
}

function firstEntry(map: Record<string, string>): [string, string] {
  return Object.entries(map)[0] ?? ["", ""];
}

export function InjectionRuleDialog({
  open,
  onOpenChange,
  mode,
  initial,
  fixedType,
  onSubmit,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  mode: "persistent" | "one-off";
  initial?: LocalInjectionRule | undefined;
  fixedType?: InjectionRuleType | undefined;
  onSubmit: (values: InjectionEditorValues) => Promise<void>;
}) {
  const editing = Boolean(initial);
  const [values, setValues] = useState<InjectionEditorValues>(() => ({
    ...initialValues(initial),
    ...(fixedType ? { type: fixedType } : {}),
  }));
  const [headerCondition, setHeaderCondition] = useState<[string, string]>(() =>
    firstEntry(initial?.spec.if_headers ?? {}),
  );
  const [queryCondition, setQueryCondition] = useState<[string, string]>(() =>
    firstEntry(initial?.spec.if_queries ?? {}),
  );
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const update = <K extends keyof InjectionEditorValues>(key: K, value: InjectionEditorValues[K]) =>
    setValues((current) => ({ ...current, [key]: value }));

  const submit = async () => {
    setError(null);
    const name = values.name.trim() || (mode === "one-off" ? "临时安全连接" : "");
    if (!name) {
      setError("请填写连接名称。");
      return;
    }
    if ((values.type === "http" || values.type === "mcp") && !values.baseUrl.trim()) {
      setError("请填写目标地址。");
      return;
    }
    if (!editing && !values.secret.trim()) {
      setError("请填写密钥。");
      return;
    }
    const ifHeaders = headerCondition[0].trim()
      ? { [headerCondition[0].trim()]: headerCondition[1] }
      : {};
    const ifQueries = queryCondition[0].trim()
      ? { [queryCondition[0].trim()]: queryCondition[1] }
      : {};
    setSubmitting(true);
    try {
      await onSubmit({ ...values, name, ifHeaders, ifQueries });
      onOpenChange(false);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "保存失败，请重试。");
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={(next) => !submitting && onOpenChange(next)}>
      <DialogPopup className="max-w-lg">
        <DialogHeader>
          <DialogTitle>
            {values.type === "mcp"
              ? editing
                ? "编辑 MCP 凭证"
                : "添加 MCP 凭证"
              : editing
                ? "编辑凭证"
                : mode === "persistent"
                  ? "添加 HTTP 凭证"
                  : "添加临时凭证"}
          </DialogTitle>
          <DialogDescription>
            {values.type === "mcp"
              ? "用于远程 Streamable HTTP MCP。支持 API Key / Header；OAuth 暂未接入。"
              : mode === "persistent"
                ? "填写请求地址和认证信息，然后在沙箱中选择使用。"
                : "此凭证仅供当前沙箱使用。"}
          </DialogDescription>
        </DialogHeader>
        <DialogPanel>
          <form
            className="grid gap-4"
            onSubmit={(event) => {
              event.preventDefault();
              void submit();
            }}
          >
            <div className="grid gap-1.5">
              <label className="text-sm font-medium" htmlFor="injection-name">
                名称
              </label>
              <Input
                id="injection-name"
                value={values.name}
                onChange={(event) => update("name", event.target.value)}
                maxLength={64}
                placeholder={mode === "persistent" ? "例如：个人 GitHub" : "例如：临时 GitHub 访问"}
                autoFocus
              />
            </div>

            <div className="grid gap-1.5">
              <label className="text-sm font-medium" htmlFor="injection-type">
                服务类型
              </label>
              <Select
                value={values.type}
                onValueChange={(next) => update("type", String(next) as InjectionRuleType)}
                disabled={editing || Boolean(fixedType)}
              >
                <SelectTrigger id="injection-type" className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectPopup align="start">
                  {(fixedType === "mcp" ? ["mcp" as const] : TYPES).map((type) => (
                    <SelectItem key={type} value={type}>
                      {ruleTypeLabel(type)}
                    </SelectItem>
                  ))}
                </SelectPopup>
              </Select>
              <p className="text-xs text-muted-foreground">
                匹配{" "}
                {ruleTarget({
                  spec: {
                    type: values.type === "mcp" ? "http" : values.type,
                    base_url: values.baseUrl,
                  },
                })}
              </p>
            </div>

            <div className="grid gap-1.5">
              <label className="text-sm font-medium" htmlFor="injection-base-url">
                {values.type === "mcp" ? "MCP URL" : "目标地址"}
                {values.type === "http" || values.type === "mcp" ? "" : "（可选）"}
              </label>
              <Input
                id="injection-base-url"
                value={values.baseUrl}
                onChange={(event) => update("baseUrl", event.target.value)}
                placeholder={
                  values.type === "github"
                    ? "https://api.github.com"
                    : values.type === "http" || values.type === "mcp"
                      ? "https://api.example.com"
                      : "留空使用服务默认地址"
                }
                autoComplete="off"
                spellCheck={false}
              />
            </div>

            {values.type === "http" || values.type === "mcp" ? (
              <div className="grid grid-cols-[minmax(0,1fr)_minmax(0,1.6fr)] gap-3">
                <div className="grid gap-1.5">
                  <label className="text-sm font-medium" htmlFor="injection-header">
                    Header
                  </label>
                  <Input
                    id="injection-header"
                    value={values.headerName}
                    onChange={(event) => update("headerName", event.target.value)}
                    placeholder="Authorization"
                  />
                </div>
                <div className="grid gap-1.5">
                  <label className="text-sm font-medium" htmlFor="injection-secret">
                    密钥
                  </label>
                  <Input
                    id="injection-secret"
                    type="password"
                    value={values.secret}
                    onChange={(event) => update("secret", event.target.value)}
                    placeholder={
                      editing
                        ? "留空表示不修改"
                        : values.type === "mcp"
                          ? "API Key（Authorization 自动加 Bearer）"
                          : "Bearer …"
                    }
                    autoComplete="new-password"
                  />
                </div>
              </div>
            ) : (
              <div className="grid gap-1.5">
                <label className="text-sm font-medium" htmlFor="injection-secret">
                  密钥 / Token
                </label>
                <Input
                  id="injection-secret"
                  type="password"
                  value={values.secret}
                  onChange={(event) => update("secret", event.target.value)}
                  placeholder={editing ? "留空表示不修改" : "粘贴服务密钥"}
                  autoComplete="new-password"
                />
              </div>
            )}

            <div className="grid gap-2 rounded-lg border border-border p-3">
              <p className="text-xs font-medium text-muted-foreground">可选匹配条件</p>
              <div className="grid grid-cols-[minmax(0,1fr)_minmax(0,1.6fr)] gap-2">
                <Input
                  aria-label="Header 条件名"
                  value={headerCondition[0]}
                  onChange={(event) => setHeaderCondition([event.target.value, headerCondition[1]])}
                  placeholder="Header 名"
                />
                <Input
                  aria-label="Header 条件值"
                  value={headerCondition[1]}
                  onChange={(event) => setHeaderCondition([headerCondition[0], event.target.value])}
                  placeholder="必须等于"
                />
              </div>
              <div className="grid grid-cols-[minmax(0,1fr)_minmax(0,1.6fr)] gap-2">
                <Input
                  aria-label="Query 条件名"
                  value={queryCondition[0]}
                  onChange={(event) => setQueryCondition([event.target.value, queryCondition[1]])}
                  placeholder="Query 名"
                />
                <Input
                  aria-label="Query 条件值"
                  value={queryCondition[1]}
                  onChange={(event) => setQueryCondition([queryCondition[0], event.target.value])}
                  placeholder="必须等于"
                />
              </div>
              <p className="text-[11px] leading-4 text-muted-foreground">
                仅当请求已带有指定 Header 或 Query 时才注入。
              </p>
            </div>

            {error ? (
              <p role="alert" className="text-sm text-destructive">
                {error}
              </p>
            ) : null}
          </form>
        </DialogPanel>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={submitting}>
            取消
          </Button>
          <Button onClick={() => void submit()} disabled={submitting}>
            {submitting ? "正在保存…" : "保存连接"}
          </Button>
        </DialogFooter>
      </DialogPopup>
    </Dialog>
  );
}

export function editorValuesToSpec(values: InjectionEditorValues): LocalInjectionSpec {
  return {
    type: values.type === "mcp" ? "http" : values.type,
    ...(values.baseUrl.trim() ? { base_url: values.baseUrl.trim() } : {}),
    ...((values.type === "http" || values.type === "mcp") && values.headerName.trim()
      ? { header_name: values.headerName.trim() }
      : {}),
    ...(Object.keys(values.ifHeaders).length > 0 ? { if_headers: values.ifHeaders } : {}),
    ...(Object.keys(values.ifQueries).length > 0 ? { if_queries: values.ifQueries } : {}),
  };
}
