import { useEffect, useRef, useState } from "react";
import { createCustomMCP, type MCPCatalogEntry } from "../../api/public.generated";
import { getAuthGeneration } from "./auth";
import { Button } from "../../components/ui/button";
import { Input } from "../../components/ui/input";
import {
  Dialog,
  DialogPopup,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogPanel,
} from "../../components/ui/dialog";

export function CustomMCPDialog({
  onClose,
  onCreated,
}: {
  onClose: () => void;
  onCreated: (item: MCPCatalogEntry) => Promise<void>;
}) {
  const [name, setName] = useState("");
  const [endpoint, setEndpoint] = useState("");
  const [auth, setAuth] = useState<"none" | "bearer">("none");
  const [token, setToken] = useState("");
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const controller = useRef<AbortController | null>(null);
  useEffect(() => () => controller.current?.abort(), []);
  const close = () => {
    controller.current?.abort();
    setToken("");
    onClose();
  };
  const submit = async () => {
    if (pending) return;
    setPending(true);
    setError(null);
    const generation = getAuthGeneration();
    const abort = new AbortController();
    controller.current = abort;
    try {
      const url = new URL(endpoint.trim());
      if (
        url.protocol !== "https:" ||
        url.username ||
        url.password ||
        url.search ||
        url.hash ||
        (url.port && url.port !== "443")
      )
        throw new Error("请填写公网 HTTPS 地址（443 端口），认证信息单独填写。");
      const result = await createCustomMCP(
        {
          name: name.trim(),
          endpoint: url.toString(),
          auth_kind: auth,
          ...(auth === "bearer" ? { token } : {}),
        },
        { signal: abort.signal },
      );
      if (abort.signal.aborted) return;
      if (getAuthGeneration() !== generation)
        throw new Error("账号已切换，请重新加载确认连接状态。");
      setToken("");
      await onCreated(result);
      onClose();
    } catch (cause) {
      if (!abort.signal.aborted)
        setError(cause instanceof Error ? cause.message : "连接验证失败，请检查服务地址及认证。");
    } finally {
      controller.current = null;
      setPending(false);
    }
  };
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) close();
      }}
    >
      <DialogPopup className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>添加自定义 MCP</DialogTitle>
          <DialogDescription>连接你信任的远程工具服务。</DialogDescription>
        </DialogHeader>
        <DialogPanel>
          <form
            className="grid gap-5"
            onSubmit={(event) => {
              event.preventDefault();
              void submit();
            }}
          >
            <label className="grid gap-2 text-sm">
              名称
              <Input
                value={name}
                onChange={(event) => setName(event.target.value)}
                required
                maxLength={80}
                disabled={pending}
                placeholder="例如：团队文档"
              />
            </label>
            <label className="grid gap-2 text-sm">
              服务器地址
              <Input
                type="url"
                value={endpoint}
                onChange={(event) => setEndpoint(event.target.value)}
                required
                maxLength={2048}
                disabled={pending}
                placeholder="https://example.com/mcp"
              />
            </label>
            <label className="grid gap-2 text-sm">
              认证方式
              <select
                className="h-9 rounded-lg border border-border bg-background px-3"
                value={auth}
                disabled={pending}
                onChange={(event) => {
                  setAuth(event.target.value === "bearer" ? "bearer" : "none");
                  setToken("");
                }}
              >
                <option value="none">无需认证</option>
                <option value="bearer">Bearer Token</option>
              </select>
            </label>
            {auth === "bearer" ? (
              <label className="grid gap-2 text-sm">
                Token
                <Input
                  type="password"
                  value={token}
                  onChange={(event) => setToken(event.target.value)}
                  required
                  maxLength={4096}
                  disabled={pending}
                  autoComplete="new-password"
                  spellCheck={false}
                />
              </label>
            ) : null}
            <p className="text-xs leading-relaxed text-muted-foreground">
              支持公网 HTTPS Streamable HTTP。Token
              加密保存在服务器，不写入沙箱。添加时验证连接与工具发现，之后仍需在沙箱“安全连接”中启用；工具权限由服务方提供。
            </p>
            {error ? (
              <p role="alert" className="text-sm text-destructive">
                {error}
              </p>
            ) : null}
            {pending ? (
              <p role="status" className="text-sm text-muted-foreground">
                正在验证连接和工具…
              </p>
            ) : null}
            <div className="flex justify-end gap-2">
              <Button type="button" variant="outline" onClick={close}>
                取消
              </Button>
              <Button
                type="submit"
                disabled={
                  pending || !name.trim() || !endpoint.trim() || (auth === "bearer" && !token)
                }
              >
                {pending ? "正在连接…" : "添加并验证"}
              </Button>
            </div>
          </form>
        </DialogPanel>
      </DialogPopup>
    </Dialog>
  );
}
