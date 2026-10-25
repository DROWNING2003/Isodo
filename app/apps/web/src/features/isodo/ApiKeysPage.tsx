import { useEffect, useState, type FormEvent } from "react";
import {
  CheckIcon,
  ChevronDownIcon,
  CopyIcon,
  EllipsisIcon,
  KeyRoundIcon,
  PlusIcon,
} from "lucide-react";
import { apiKeys, type ApiKey, type ApiKeyScope } from "./auth";
import { Button } from "../../components/ui/button";
import { Input } from "../../components/ui/input";
import {
  Dialog,
  DialogDescription,
  DialogHeader,
  DialogPanel,
  DialogPopup,
  DialogTitle,
} from "../../components/ui/dialog";
import { CloudPage } from "./CloudPages";

const SCOPE_LABELS: Record<ApiKeyScope, string> = {
  "sandboxes:read": "沙箱读取",
  "sandboxes:write": "沙箱管理",
  "templates:read": "模板读取",
  "templates:write": "模板管理",
};

function formatDate(value: string | null) {
  if (!value) return "永不过期";
  return new Intl.DateTimeFormat("zh-CN", {
    year: "numeric",
    month: "short",
    day: "numeric",
  }).format(new Date(value));
}

function formatKeyPrefix(value: string) {
  return `${value.replace(/…+$/, "").slice(0, 10)}…`;
}

function ApiKeyRow({
  apiKey,
  onRevoke,
}: {
  apiKey: ApiKey;
  onRevoke: (id: string) => Promise<void>;
}) {
  const [confirming, setConfirming] = useState(false);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  return (
    <tr className="border-t border-border align-middle">
      <td className="px-5 py-4">
        <div className="font-medium">{apiKey.name}</div>
        <code className="mt-1 block text-xs text-muted-foreground">
          {formatKeyPrefix(apiKey.prefix)}
        </code>
      </td>
      <td className="whitespace-nowrap px-5 py-4 text-sm text-muted-foreground">
        {formatDate(apiKey.created_at)}
      </td>
      <td className="whitespace-nowrap px-5 py-4 text-sm text-muted-foreground">
        {formatDate(apiKey.expires_at)}
      </td>
      <td className="px-5 py-4">
        <div className="flex flex-wrap gap-1.5">
          {apiKey.scopes.map((scope) => (
            <span
              key={scope}
              className="rounded-md border border-border bg-muted/40 px-2 py-1 text-xs"
            >
              {SCOPE_LABELS[scope]}
            </span>
          ))}
        </div>
      </td>
      <td className="px-5 py-4 text-right">
        <Button
          variant="ghost"
          size="icon"
          aria-label={`撤销 ${apiKey.name}`}
          onClick={() => setConfirming(true)}
        >
          <EllipsisIcon className="size-4" />
        </Button>
        <Dialog open={confirming} onOpenChange={setConfirming}>
          <DialogPopup className="max-w-sm" bottomStickOnMobile={false}>
            <DialogHeader>
              <DialogTitle>撤销密钥？</DialogTitle>
              <DialogDescription>“{apiKey.name}”将立即失效，此操作无法恢复。</DialogDescription>
            </DialogHeader>
            <DialogPanel>
              {error && (
                <p role="alert" className="mb-3 text-sm text-destructive">
                  {error}
                </p>
              )}
              <div className="flex justify-end gap-2">
                <Button variant="outline" onClick={() => setConfirming(false)}>
                  取消
                </Button>
                <Button
                  variant="destructive"
                  disabled={pending}
                  onClick={async () => {
                    setPending(true);
                    setError(null);
                    try {
                      await onRevoke(apiKey.id);
                      setConfirming(false);
                    } catch (cause) {
                      setError(cause instanceof Error ? cause.message : "撤销失败");
                    } finally {
                      setPending(false);
                    }
                  }}
                >
                  {pending ? "正在撤销…" : "撤销密钥"}
                </Button>
              </div>
            </DialogPanel>
          </DialogPopup>
        </Dialog>
        {error && (
          <p role="alert" className="mt-2 text-xs text-destructive">
            {error}
          </p>
        )}
      </td>
    </tr>
  );
}

export function ApiKeysPage() {
  const [keys, setKeys] = useState<ApiKey[]>([]);
  const [loading, setLoading] = useState(true);
  const [creating, setCreating] = useState(false);
  const [showCreateForm, setShowCreateForm] = useState(false);
  const [name, setName] = useState("");
  const [expiresInDays, setExpiresInDays] = useState("30");
  const [createdSecret, setCreatedSecret] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let active = true;
    apiKeys
      .list()
      .then(({ items }) => {
        if (active) setKeys(items.filter((key) => !key.revoked_at));
      })
      .catch((cause) => {
        if (active) setError(cause instanceof Error ? cause.message : "加载失败");
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => {
      active = false;
    };
  }, []);

  const revokeKey = async (id: string) => {
    await apiKeys.revoke(id);
    setKeys((current) => current.filter((key) => key.id !== id));
  };

  const createKey = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (creating) return;
    setError(null);
    setCreating(true);
    try {
      const { api_key, ...created } = await apiKeys.create({
        name,
        scopes: ["sandboxes:read", "sandboxes:write", "templates:read", "templates:write"],
        expires_at:
          expiresInDays === "never"
            ? null
            : new Date(Date.now() + Number(expiresInDays) * 86400000).toISOString(),
      });
      setCreatedSecret(api_key);
      setKeys((current) => [created, ...current]);
      setCopied(false);
      setName("");
      setShowCreateForm(false);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "创建密钥失败");
    } finally {
      setCreating(false);
    }
  };

  const copySecret = async () => {
    if (!createdSecret) return;
    try {
      await navigator.clipboard.writeText(createdSecret);
      setCopied(true);
    } catch {
      setCopied(false);
      setError("复制失败，请选中密钥手动复制。");
    }
  };

  return (
    <CloudPage title="API 密钥">
      <section className="space-y-2">
        <h1 className="text-2xl font-semibold tracking-tight">API 密钥</h1>
        <p className="text-sm text-muted-foreground">创建和管理用于访问 Isodo API 的 API 密钥。</p>
      </section>

      <div className="flex flex-wrap items-center justify-between gap-3">
        <p className="text-sm text-muted-foreground">已创建 {keys.length} 个密钥</p>
        <div className="flex items-center gap-2">
          <Button
            size="sm"
            disabled={loading}
            onClick={() => {
              setShowCreateForm(true);
              setError(null);
            }}
          >
            <PlusIcon className="size-4" />
            创建新密钥
          </Button>
        </div>
      </div>

      {!showCreateForm && error && (
        <p role="alert" className="text-sm text-destructive">
          {error}
        </p>
      )}
      <Dialog
        open={Boolean(createdSecret)}
        onOpenChange={(open) => {
          if (!open) {
            setCreatedSecret(null);
            setCopied(false);
          }
        }}
      >
        <DialogPopup className="max-w-md" bottomStickOnMobile={false}>
          <DialogHeader>
            <DialogTitle>密钥已创建</DialogTitle>
            <DialogDescription>请复制并妥善保存，关闭后无法再次查看完整密钥。</DialogDescription>
          </DialogHeader>
          <DialogPanel>
            {error && (
              <p role="alert" className="mb-3 text-sm text-destructive">
                {error}
              </p>
            )}
            <code className="block break-all rounded-lg border border-border bg-muted/40 p-3 text-sm leading-6 select-all">
              {createdSecret}
            </code>
            <div className="mt-5 flex justify-end gap-2">
              <Button
                variant="outline"
                onClick={() => {
                  setCreatedSecret(null);
                  setCopied(false);
                }}
              >
                完成
              </Button>
              <Button onClick={() => void copySecret()}>
                {copied ? <CheckIcon className="size-4" /> : <CopyIcon className="size-4" />}
                {copied ? "已复制" : "复制密钥"}
              </Button>
            </div>
          </DialogPanel>
        </DialogPopup>
      </Dialog>

      <Dialog open={showCreateForm} onOpenChange={setShowCreateForm}>
        <DialogPopup className="max-w-md" bottomStickOnMobile={false}>
          <DialogHeader>
            <DialogTitle>创建 API 密钥</DialogTitle>
            <DialogDescription>为应用或环境命名，方便后续管理。</DialogDescription>
          </DialogHeader>
          <DialogPanel>
            <form className="grid gap-5" onSubmit={createKey}>
              <label className="space-y-2 text-sm md:col-span-2">
                <span className="font-medium">名称</span>
                <Input
                  autoFocus
                  maxLength={64}
                  placeholder="例如：生产环境 CI"
                  value={name}
                  onChange={(event) => setName(event.target.value)}
                  required
                />
              </label>
              <label className="space-y-2 text-sm">
                <span className="font-medium">到期时间</span>
                <span className="relative block">
                  <select
                    className="h-9 w-full appearance-none rounded-md border border-input bg-background px-3 pr-9 text-sm"
                    value={expiresInDays}
                    onChange={(event) => setExpiresInDays(event.target.value)}
                  >
                    <option value="30">30 天</option>
                    <option value="90">90 天</option>
                    <option value="365">1 年</option>
                    <option value="never">永不过期</option>
                  </select>
                  <ChevronDownIcon className="pointer-events-none absolute right-3 top-2.5 size-4 text-muted-foreground" />
                </span>
              </label>
              {error && (
                <p role="alert" className="text-sm text-destructive md:col-span-2">
                  {error}
                </p>
              )}
              <div className="mt-1 flex justify-end gap-2">
                <Button type="button" variant="ghost" onClick={() => setShowCreateForm(false)}>
                  取消
                </Button>
                <Button type="submit" disabled={creating}>
                  {creating ? "正在创建…" : "创建密钥"}
                </Button>
              </div>
            </form>
          </DialogPanel>
        </DialogPopup>
      </Dialog>

      <div className="isodo-api-key-table overflow-hidden rounded-xl border bg-card">
        <div className="max-h-[min(480px,55vh)] overflow-auto">
          <table className="w-full min-w-[760px] text-left text-sm">
            <thead className="sticky top-0 z-10 bg-muted text-muted-foreground">
              <tr>
                <th scope="col" className="px-5 py-4 font-medium">
                  名称
                </th>
                <th scope="col" className="px-5 py-4 font-medium">
                  创建时间
                </th>
                <th scope="col" className="px-5 py-4 font-medium">
                  到期时间
                </th>
                <th scope="col" className="px-5 py-4 font-medium">
                  访问权限
                </th>
                <th scope="col" className="px-5 py-4 text-right font-medium">
                  操作
                </th>
              </tr>
            </thead>
            <tbody>
              {keys.length > 0 ? (
                keys.map((apiKey) => (
                  <ApiKeyRow key={apiKey.id} apiKey={apiKey} onRevoke={revokeKey} />
                ))
              ) : (
                <tr>
                  <td colSpan={5} className="px-5 py-14 text-center">
                    <KeyRoundIcon className="mx-auto size-6 text-muted-foreground" />
                    <p className="mt-3 font-medium">
                      {loading ? "正在加载密钥…" : "还没有 API 密钥"}
                    </p>
                    <p className="mt-1 text-sm text-muted-foreground">
                      创建密钥后即可在这里查看和管理。
                    </p>
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>

      <div className="flex flex-wrap items-center justify-between gap-3 text-sm text-muted-foreground">
        <label className="flex items-center gap-2">
          每页行数
          <select
            className="h-9 rounded-md border border-input bg-background px-3"
            defaultValue="10"
          >
            <option value="10">10</option>
          </select>
        </label>
        <span>正在查看（{keys.length ? `1 – ${keys.length}` : "0 – 0"}）</span>
      </div>
    </CloudPage>
  );
}
