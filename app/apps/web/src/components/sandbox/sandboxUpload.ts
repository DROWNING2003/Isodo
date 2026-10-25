export const DEFAULT_SANDBOX_UPLOAD_DIRECTORY = "/workspace/project";

export function sandboxUploadPath(directory: string, fileName: string): string {
  const trimmedDirectory = directory.trim() || DEFAULT_SANDBOX_UPLOAD_DIRECTORY;
  if (!trimmedDirectory.startsWith("/")) {
    throw new Error("Target directory must be an absolute path.");
  }
  const normalizedDirectory = trimmedDirectory.replace(/\/+$/, "") || "/";

  const normalizedFileName = fileName.trim();
  if (
    !normalizedFileName ||
    normalizedFileName.includes("/") ||
    normalizedFileName.includes("\\")
  ) {
    throw new Error("File name must not contain a path separator.");
  }

  return normalizedDirectory === "/"
    ? `/${normalizedFileName}`
    : `${normalizedDirectory}/${normalizedFileName}`;
}
