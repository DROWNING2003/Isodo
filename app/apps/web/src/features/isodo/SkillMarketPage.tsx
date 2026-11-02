import { useEffect, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { PlusIcon, SparklesIcon, StarIcon, ArrowLeftIcon } from "lucide-react";
import { createSkill, favoriteSkill, unfavoriteSkill } from "../../api/public.generated";
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
import { CloudPage } from "./CloudPages";
import { getAuthGeneration } from "./auth";
import { skillKey, useSkillMarket, type useSandboxSkillSelection } from "./skillMarket";

export function SkillMarketPage() {
  const market = useSkillMarket();
  const client = useQueryClient();
  const [mine, setMine] = useState(false);
  const [search, setSearch] = useState("");
  const [selected, setSelected] = useState<string | null>(null);
  const [adding, setAdding] = useState(false);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [body, setBody] = useState("");
  const generation = getAuthGeneration();
  useEffect(() => {
    setSelected(null);
    setAdding(false);
    setName("");
    setDescription("");
    setBody("");
    setError(null);
  }, [generation]);
  const entries = market.data?.items ?? [];
  const detail = entries.find((entry) => entry.id === selected);
  const mutate = async (action: () => Promise<unknown>) => {
    if (getAuthGeneration() !== generation) return;
    setPending(true);
    setError(null);
    try {
      await action();
      if (getAuthGeneration() !== generation) return;
      await client.invalidateQueries({ queryKey: skillKey });
    } catch (cause) {
      if (getAuthGeneration() === generation)
        setError(cause instanceof Error ? cause.message : "操作失败");
    } finally {
      setPending(false);
    }
  };
  return (
    <CloudPage title="Skill 市场">
      <div className="mx-auto w-full max-w-5xl space-y-7 py-4">
        <header className="flex items-start justify-between gap-4">
          <div>
            <h1 className="text-2xl font-semibold">Skill</h1>
            <p className="mt-2 text-sm text-muted-foreground">
              收藏工作方法，在沙箱中选择，让 Agent 按需使用。
            </p>
          </div>
          <Button onClick={() => setAdding(true)}>
            <PlusIcon />
            制作 Skill
          </Button>
        </header>
        {error ? (
          <p role="alert" className="text-sm text-destructive">
            {error}
          </p>
        ) : null}
        {detail ? (
          <section className="space-y-5">
            <Button variant="ghost" onClick={() => setSelected(null)}>
              <ArrowLeftIcon />
              返回 Skill
            </Button>
            <div className="flex items-start justify-between gap-4">
              <div>
                <h2 className="text-xl font-medium">{detail.name}</h2>
                <p className="mt-2 text-muted-foreground">{detail.description}</p>
              </div>
              <Button
                variant="outline"
                disabled={pending}
                onClick={() =>
                  void mutate(() =>
                    detail.favorite ? unfavoriteSkill(detail.id) : favoriteSkill(detail.id),
                  )
                }
              >
                <StarIcon />
                {detail.favorite ? "取消收藏" : "收藏"}
              </Button>
            </div>
            <p className="text-xs text-muted-foreground">
              {detail.source === "personal" ? "个人 Skill · 仅当前账号可见" : "Isodo 市场"} ·
              指令文件 · v1
            </p>
            <pre className="whitespace-pre-wrap break-words rounded-xl border p-5 text-sm leading-7">
              {detail.body}
            </pre>
            <p className="text-sm text-muted-foreground">
              收藏不会自动启用。请在沙箱的安全连接中选择并保存；新建 Agent 任务后加载。
            </p>
          </section>
        ) : (
          <>
            <div className="flex flex-wrap items-center justify-between gap-4">
              <nav className="flex gap-2" aria-label="Skill 分类">
                <Button variant={mine ? "ghost" : "secondary"} onClick={() => setMine(false)}>
                  市场
                </Button>
                <Button variant={mine ? "secondary" : "ghost"} onClick={() => setMine(true)}>
                  我的 Skill
                </Button>
              </nav>
              <Input
                aria-label="搜索 Skill"
                placeholder="搜索 Skill"
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                className="max-w-sm"
              />
            </div>
            {market.isLoading ? (
              <p role="status">正在加载 Skill…</p>
            ) : market.isError ? (
              <div role="alert">
                Skill 市场加载失败。
                <Button variant="outline" onClick={() => void market.refetch()}>
                  重新加载
                </Button>
              </div>
            ) : (
              <div className="grid gap-x-10 gap-y-2 md:grid-cols-2">
                {entries
                  .filter(
                    (entry) =>
                      (mine
                        ? entry.favorite || entry.source === "personal"
                        : entry.source === "market") &&
                      `${entry.name} ${entry.description}`
                        .toLowerCase()
                        .includes(search.trim().toLowerCase()),
                  )
                  .map((entry) => (
                    <div
                      key={entry.id}
                      className="flex items-center gap-4 rounded-xl px-3 py-4 hover:bg-muted/50"
                    >
                      <SparklesIcon className="size-9 shrink-0 rounded-lg border p-2" />
                      <button
                        className="min-w-0 flex-1 text-left"
                        onClick={() => setSelected(entry.id)}
                        aria-label={`查看 ${entry.name} Skill`}
                      >
                        <span className="block font-medium">{entry.name}</span>
                        <span className="mt-1 block text-sm text-muted-foreground">
                          {entry.description}
                        </span>
                      </button>
                      <Button
                        variant="ghost"
                        size="icon"
                        disabled={pending}
                        aria-label={`${entry.favorite ? "取消收藏" : "收藏"} ${entry.name}`}
                        onClick={() =>
                          void mutate(() =>
                            entry.favorite ? unfavoriteSkill(entry.id) : favoriteSkill(entry.id),
                          )
                        }
                      >
                        <StarIcon className={entry.favorite ? "fill-current" : ""} />
                      </Button>
                    </div>
                  ))}
                {mine &&
                entries.every((entry) => !entry.favorite && entry.source !== "personal") ? (
                  <p className="py-8 text-sm text-muted-foreground">
                    还没有收藏的 Skill，从市场选择或制作一个。
                  </p>
                ) : null}
              </div>
            )}
          </>
        )}
      </div>
      <Dialog
        open={adding}
        onOpenChange={(open) => {
          if (!pending) setAdding(open);
        }}
      >
        <DialogPopup>
          <DialogHeader>
            <DialogTitle>制作 Skill</DialogTitle>
            <DialogDescription>
              仅保存在你的账号中，创建后自动收藏。不要填写密钥或凭证。
            </DialogDescription>
          </DialogHeader>
          <DialogPanel>
            <form
              className="grid gap-4"
              onSubmit={(event) => {
                event.preventDefault();
                void mutate(async () => {
                  await createSkill({ name, description, body });
                  if (getAuthGeneration() === generation) {
                    setAdding(false);
                    setMine(true);
                    setName("");
                    setDescription("");
                    setBody("");
                  }
                });
              }}
            >
              <label className="grid gap-2 text-sm">
                名称
                <Input
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  required
                  pattern="[a-z][a-z0-9]*(-[a-z0-9]+)*"
                  maxLength={64}
                  placeholder="my-review"
                  disabled={pending}
                />
              </label>
              <label className="grid gap-2 text-sm">
                说明
                <Input
                  value={description}
                  onChange={(e) => setDescription(e.target.value)}
                  required
                  maxLength={1024}
                  disabled={pending}
                />
              </label>
              <label className="grid gap-2 text-sm">
                指令正文
                <textarea
                  className="min-h-52 rounded-md border bg-transparent p-3 font-mono text-sm"
                  value={body}
                  onChange={(e) => setBody(e.target.value)}
                  required
                  maxLength={32768}
                  disabled={pending}
                />
              </label>
              {error ? (
                <p role="alert" className="text-sm text-destructive">
                  {error}
                </p>
              ) : null}
              <Button type="submit" disabled={pending}>
                {pending ? "正在保存…" : "创建并收藏"}
              </Button>
            </form>
          </DialogPanel>
        </DialogPopup>
      </Dialog>
    </CloudPage>
  );
}

export function SandboxSkillSelection({
  state,
  disabled,
}: {
  state: ReturnType<typeof useSandboxSkillSelection>;
  disabled: boolean;
}) {
  const entries = state.catalog.data?.items ?? [];
  return (
    <section className="grid gap-2">
      <h3 className="text-sm font-medium">Skill</h3>
      <p className="text-sm text-muted-foreground">选择已收藏的 Skill，保存后在新任务中使用。</p>
      {state.loading ? (
        <p role="status">正在读取 Skill…</p>
      ) : state.failed ? (
        <p role="alert">Skill 加载失败，请刷新后重试。</p>
      ) : (
        <>
          {entries
            .filter((entry) => entry.favorite || state.selected.includes(entry.id))
            .map((entry) => (
              <label
                key={entry.id}
                className="flex items-center gap-3 rounded-lg border border-border p-3"
              >
                <input
                  type="checkbox"
                  aria-label={`启用 ${entry.name} Skill`}
                  checked={state.selected.includes(entry.id)}
                  disabled={disabled}
                  onChange={(e) => state.toggle(entry.id, e.target.checked)}
                />
                <span>
                  <span className="block text-sm font-medium">{entry.name}</span>
                  <span className="block text-xs text-muted-foreground">{entry.description}</span>
                </span>
              </label>
            ))}
          {!entries.some((entry) => entry.favorite || state.selected.includes(entry.id)) ? (
            <p className="text-sm text-muted-foreground">
              还没有收藏的 Skill，请先到全局 Skill 市场收藏。
            </p>
          ) : null}
          {state.saved.data?.version && state.saved.data.sync_status !== "applied" ? (
            <p className="text-xs text-muted-foreground">
              {state.saved.data.sync_status === "failed"
                ? "同步失败，请检查托管目录冲突并重新保存。"
                : "配置待同步，请启动沙箱后重新保存。"}
            </p>
          ) : null}
        </>
      )}
    </section>
  );
}
