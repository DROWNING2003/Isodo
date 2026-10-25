import type { SandboxInfo } from "@t3tools/shared/sandbox";
import { FileIcon, LoaderCircleIcon, UploadIcon, XIcon } from "lucide-react";
import { useEffect, useState } from "react";

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
import { DEFAULT_SANDBOX_UPLOAD_DIRECTORY } from "./sandboxUpload";

interface Props {
  readonly open: boolean;
  readonly sandbox: SandboxInfo | null;
  readonly defaultDirectory?: string;
  readonly onOpenChange: (open: boolean) => void;
  readonly onUpload: (files: readonly File[], directory: string) => Promise<void>;
}

function formatFileSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${Math.round(bytes / 1024)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

export function SandboxUploadDialog({
  open,
  sandbox,
  defaultDirectory = DEFAULT_SANDBOX_UPLOAD_DIRECTORY,
  onOpenChange,
  onUpload,
}: Props) {
  const [directory, setDirectory] = useState(defaultDirectory);
  const [files, setFiles] = useState<readonly File[]>([]);
  const [uploading, setUploading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!open) return;
    setDirectory(defaultDirectory);
    setFiles([]);
    setUploading(false);
    setError(null);
  }, [defaultDirectory, open, sandbox?.sandboxID]);

  async function handleSubmit() {
    if (files.length === 0 || uploading) return;
    setUploading(true);
    setError(null);
    try {
      await onUpload(files, directory);
      onOpenChange(false);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "上传失败，请重试。");
    } finally {
      setUploading(false);
    }
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(nextOpen) => {
        if (!uploading) onOpenChange(nextOpen);
      }}
    >
      <DialogPopup>
        <DialogHeader>
          <DialogTitle>上传文件</DialogTitle>
          <DialogDescription>
            {sandbox?.alias || sandbox?.sandboxID.slice(0, 8) || "当前沙箱"}
          </DialogDescription>
        </DialogHeader>
        <DialogPanel className="space-y-5">
          <div className="space-y-2">
            <label className="text-sm font-medium" htmlFor="sandbox-upload-directory">
              目标目录
            </label>
            <Input
              id="sandbox-upload-directory"
              value={directory}
              onChange={(event) => setDirectory(event.target.value)}
              placeholder={defaultDirectory}
              disabled={uploading}
              spellCheck={false}
            />
          </div>
          <div className="space-y-2">
            <label className="text-sm font-medium" htmlFor="sandbox-upload-files">
              文件
            </label>
            <Input
              id="sandbox-upload-files"
              type="file"
              nativeInput
              multiple
              onClick={(event) => {
                event.currentTarget.value = "";
              }}
              onChange={(event) => setFiles(Array.from(event.target.files ?? []))}
              disabled={uploading}
            />
          </div>
          {files.length > 0 ? (
            <div className="divide-y divide-border rounded-lg border border-border/70">
              {files.map((file) => (
                <div
                  key={`${file.name}-${file.lastModified}-${file.size}`}
                  className="flex min-w-0 items-center gap-2 px-3 py-2 text-sm"
                >
                  <FileIcon className="size-4 shrink-0 text-muted-foreground" />
                  <span className="min-w-0 flex-1 truncate">{file.name}</span>
                  <span className="shrink-0 text-xs text-muted-foreground">
                    {formatFileSize(file.size)}
                  </span>
                  <Button
                    size="icon-xs"
                    variant="ghost"
                    aria-label={`移除 ${file.name}`}
                    title={`移除 ${file.name}`}
                    disabled={uploading}
                    onClick={() =>
                      setFiles((current) => current.filter((candidate) => candidate !== file))
                    }
                  >
                    <XIcon />
                  </Button>
                </div>
              ))}
            </div>
          ) : null}
          {error ? (
            <p className="text-sm text-destructive" role="alert">
              {error}
            </p>
          ) : null}
        </DialogPanel>
        <DialogFooter>
          <Button variant="ghost" onClick={() => onOpenChange(false)} disabled={uploading}>
            取消
          </Button>
          <Button onClick={() => void handleSubmit()} disabled={files.length === 0 || uploading}>
            {uploading ? <LoaderCircleIcon className="animate-spin" /> : <UploadIcon />}
            {uploading ? "上传中" : "上传"}
          </Button>
        </DialogFooter>
      </DialogPopup>
    </Dialog>
  );
}
