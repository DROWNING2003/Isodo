import type { SandboxInfo } from "@t3tools/shared/sandbox";
import { sandboxUrl } from "@t3tools/shared/sandbox";
import {
  ArrowUpRightIcon,
  ExternalLinkIcon,
  LoaderCircleIcon,
  PauseIcon,
  PlayIcon,
  RefreshCwIcon,
  Trash2Icon,
} from "lucide-react";
import { useEffect, useState } from "react";

import { cn } from "../../lib/utils";
import { Button } from "../ui/button";
import { Card, CardFooter, CardHeader, CardPanel, CardTitle } from "../ui/card";

interface Props {
  readonly sandbox: SandboxInfo;
  readonly pendingAction: SandboxAction | null;
  readonly pendingLabel?: string;
  readonly onConnect: () => void;
  readonly onPause: () => void;
  readonly onResume: () => void;
  readonly onRefresh: () => void;
  readonly onDelete: () => void;
}

export type SandboxAction = "connect" | "pause" | "resume" | "refresh" | "delete";

export function SandboxCard({
  sandbox,
  pendingAction,
  pendingLabel,
  onConnect,
  onPause,
  onResume,
  onRefresh,
  onDelete,
}: Props) {
  const [now, setNow] = useState(Date.now());

  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 10_000);
    return () => clearInterval(id);
  }, []);

  const msLeft = new Date(sandbox.endAt).getTime() - now;
  const minutesLeft = Math.max(0, Math.floor(msLeft / 60_000));
  const startedAt = new Date(sandbox.startedAt).getTime();
  const endAt = new Date(sandbox.endAt).getTime();
  const totalMs = Math.max(1, endAt - startedAt);
  const timeProgress = Math.min(100, Math.max(0, (msLeft / totalMs) * 100));
  const isUrgent = msLeft > 0 && minutesLeft < 30;
  const isRunning = sandbox.state === "running";
  const displayName = sandbox.alias || `沙箱 ${sandbox.sandboxID.slice(0, 8)}`;
  const displayUrl = sandboxUrl(sandbox.sandboxID, sandbox.domain);

  return (
    <Card className="overflow-hidden rounded-xl border-border/70 bg-card shadow-none transition-[border-color,background-color] duration-150 ease-out hover:border-border">
      <CardHeader className="gap-0 p-4 sm:p-5">
        <div className="flex min-w-0 items-start justify-between gap-4">
          <div className="flex min-w-0 items-start gap-3">
            <span
              className={cn(
                "mt-1.5 size-2 shrink-0 rounded-full",
                isRunning ? "bg-emerald-500" : "bg-amber-500",
              )}
              aria-hidden="true"
            />
            <div className="min-w-0">
              <CardTitle className="truncate text-base">{displayName}</CardTitle>
              <p className="mt-1 truncate font-mono text-[11px] text-muted-foreground/70">
                {sandbox.sandboxID}
              </p>
            </div>
          </div>
          <span
            className={cn(
              "shrink-0 text-xs font-medium",
              isRunning
                ? "text-emerald-700 dark:text-emerald-400"
                : "text-amber-700 dark:text-amber-400",
            )}
          >
            {isRunning ? "运行中" : "已暂停"}
          </span>
        </div>
        <div className="mt-4 flex flex-wrap items-end justify-between gap-4">
          <div>
            <p className="text-xs text-muted-foreground">剩余时间</p>
            <p
              className={cn(
                "mt-1 text-sm font-medium",
                isUrgent && "text-amber-600 dark:text-amber-400",
              )}
            >
              {msLeft > 0 ? `${minutesLeft} 分钟` : "已过期"}
            </p>
          </div>
          <div className="min-w-[8rem] flex-1 sm:max-w-[14rem]">
            <div className="mb-1.5 flex items-center justify-between text-[11px] text-muted-foreground/70">
              <span>生命周期</span>
              <span>{isUrgent ? "即将到期" : "自动暂停"}</span>
            </div>
            <div
              className="h-1 overflow-hidden rounded-full bg-muted"
              role="progressbar"
              aria-label="沙箱剩余生命周期"
              aria-valuemin={0}
              aria-valuemax={100}
              aria-valuenow={Math.round(timeProgress)}
            >
              <div
                className={cn(
                  "h-full rounded-full transition-[width,background-color] duration-300 ease-out",
                  isUrgent ? "bg-amber-500" : "bg-primary",
                )}
                style={{ width: `${timeProgress}%` }}
              />
            </div>
          </div>
        </div>
      </CardHeader>
      <CardPanel className="border-t border-border/50 bg-muted/10 px-4 py-3.5 sm:px-5">
        <div className="grid gap-3 text-xs sm:grid-cols-2 sm:gap-5">
          <div className="min-w-0">
            <p className="mb-1 text-[11px] text-muted-foreground">模板</p>
            <p className="truncate font-mono text-muted-foreground">{sandbox.templateID}</p>
          </div>
          <div className="min-w-0">
            <p className="mb-1 text-[11px] text-muted-foreground">访问地址</p>
            <a
              className="flex min-w-0 items-center gap-1.5 text-muted-foreground transition-colors duration-150 ease-out hover:text-foreground"
              href={displayUrl ?? undefined}
              target="_blank"
              rel="noreferrer"
            >
              <ExternalLinkIcon className="size-3 shrink-0 opacity-60" />
              <span className="truncate font-mono">{sandbox.domain}</span>
            </a>
          </div>
        </div>
      </CardPanel>
      <CardFooter className="flex flex-wrap items-center justify-between gap-3 border-t border-border/50 px-4 py-3 sm:px-5">
        <p className="text-[11px] text-muted-foreground/70">
          {sandbox.cpuCount} vCPU · {sandbox.memoryMB} MB · {sandbox.diskSizeMB} MB
        </p>
        <div className="flex flex-wrap items-center justify-end gap-1.5">
          {isRunning ? (
            <>
              <Button
                size="sm"
                variant="outline"
                onClick={onConnect}
                disabled={pendingAction !== null}
                className="border-primary/20 bg-primary/8 text-primary shadow-none hover:border-primary/30 hover:bg-primary/14 [transition:transform_160ms_cubic-bezier(0.23,1,0.32,1)] active:scale-[0.97]"
              >
                {pendingAction === "connect" ? (
                  <LoaderCircleIcon className="size-3.5 animate-spin" />
                ) : (
                  <ArrowUpRightIcon className="size-3.5" />
                )}
                {pendingAction === "connect" ? (pendingLabel ?? "连接中") : "打开工作区"}
              </Button>
              <Button
                size="icon-sm"
                variant="ghost"
                onClick={onPause}
                disabled={pendingAction !== null}
                aria-label="暂停"
                title="暂停"
                className="text-muted-foreground hover:bg-muted/60 hover:text-foreground"
              >
                {pendingAction === "pause" ? (
                  <LoaderCircleIcon className="size-3.5 animate-spin" />
                ) : (
                  <PauseIcon className="size-3.5" />
                )}
              </Button>
            </>
          ) : (
            <Button
              size="sm"
              variant="outline"
              onClick={onResume}
              disabled={pendingAction !== null}
              className="border-primary/20 bg-primary/8 text-primary shadow-none hover:border-primary/30 hover:bg-primary/14 [transition:transform_160ms_cubic-bezier(0.23,1,0.32,1)] active:scale-[0.97]"
            >
              {pendingAction === "resume" ? (
                <LoaderCircleIcon className="size-3.5 animate-spin" />
              ) : (
                <PlayIcon className="size-3.5" />
              )}
              {pendingAction === "resume" ? "恢复中" : "恢复"}
            </Button>
          )}
          <Button
            size="icon-sm"
            variant="ghost"
            onClick={onRefresh}
            disabled={pendingAction !== null}
            aria-label="刷新有效期"
            title="刷新有效期"
            className="text-muted-foreground hover:bg-muted/60 hover:text-foreground"
          >
            <RefreshCwIcon className="size-3.5" />
          </Button>
          <Button
            size="icon-sm"
            variant="ghost"
            onClick={onDelete}
            disabled={pendingAction !== null}
            aria-label="删除"
            title="删除"
            className="text-muted-foreground hover:bg-destructive/8 hover:text-destructive"
          >
            {pendingAction === "delete" ? (
              <LoaderCircleIcon className="size-3.5 animate-spin" />
            ) : (
              <Trash2Icon className="size-3.5" />
            )}
          </Button>
        </div>
      </CardFooter>
    </Card>
  );
}
