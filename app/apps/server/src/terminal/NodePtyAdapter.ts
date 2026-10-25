import * as NodeModule from "node:module";

import * as Effect from "effect/Effect";
import * as Exit from "effect/Exit";
import * as FileSystem from "effect/FileSystem";
import * as Layer from "effect/Layer";
import * as Path from "effect/Path";
import * as Schema from "effect/Schema";
import { HostProcessArchitecture, HostProcessPlatform } from "@t3tools/shared/hostProcess";

import * as PtyAdapter from "./PtyAdapter.ts";

export class NodePtyModuleLoadError extends Schema.TaggedError<NodePtyModuleLoadError>()(
  "NodePtyModuleLoadError",
  {
    platform: Schema.String,
    architecture: Schema.String,
    cause: Schema.Defect(),
  },
) {
  override get message(): string {
    return `Failed to load node-pty for ${this.platform}-${this.architecture}.`;
  }
}

type NodePtyModuleLoader = () => Promise<typeof import("node-pty")>;

type PipeProcessStream = {
  on(event: "data", listener: (data: { toString(): string }) => void): unknown;
};

export type PipeChildProcess = {
  readonly pid: number | null;
  readonly stdin: {
    readonly destroyed: boolean;
    write(data: string): boolean;
  };
  readonly stdout: PipeProcessStream;
  readonly stderr: PipeProcessStream;
  readonly killed: boolean;
  kill(signal?: NodeJS.Signals): boolean;
  on(
    event: "exit",
    listener: (exitCode: number | null, signal: NodeJS.Signals | null) => void,
  ): unknown;
  on(event: "error", listener: (error: unknown) => void): unknown;
};

export type PipeChildProcessSpawner = (
  command: string,
  args: ReadonlyArray<string>,
  options: {
    cwd: string;
    env: NodeJS.ProcessEnv;
    stdio: ["pipe", "pipe", "pipe"];
  },
) => PipeChildProcess;

const nodeChildProcess = NodeModule.createRequire(import.meta.url)("node:child_process") as {
  spawn: PipeChildProcessSpawner;
};

let didEnsureSpawnHelperExecutable = false;

class PipePtyProcess implements PtyAdapter.PtyProcess {
  private readonly dataListeners = new Set<(data: string) => void>();
  private readonly exitListeners = new Set<(event: PtyAdapter.PtyExitEvent) => void>();
  private didExit = false;

  private readonly process: PipeChildProcess;

  constructor(process: PipeChildProcess) {
    this.process = process;
    this.process.stdout.on("data", (data) => this.emitData(data.toString()));
    this.process.stderr.on("data", (data) => this.emitData(data.toString()));
    this.process.on("exit", (exitCode) => {
      this.emitExit({ exitCode: exitCode ?? 1, signal: null });
    });
    this.process.on("error", () => {
      this.emitExit({ exitCode: 1, signal: null });
    });
  }

  get pid(): number {
    return this.process.pid ?? -1;
  }

  write(data: string): void {
    if (!this.process.stdin.destroyed) {
      this.process.stdin.write(data);
    }
  }

  resize(_cols: number, _rows: number): void {
    // A pipe does not expose terminal dimensions. The fallback intentionally
    // keeps the same public contract while allowing the server to start.
  }

  kill(signal?: string): void {
    if (!this.process.killed) {
      this.process.kill(signal as NodeJS.Signals | undefined);
    }
  }

  onData(callback: (data: string) => void): () => void {
    this.dataListeners.add(callback);
    return () => {
      this.dataListeners.delete(callback);
    };
  }

  onExit(callback: (event: PtyAdapter.PtyExitEvent) => void): () => void {
    this.exitListeners.add(callback);
    return () => {
      this.exitListeners.delete(callback);
    };
  }

  private emitData(data: string): void {
    if (this.didExit) return;
    for (const listener of this.dataListeners) {
      listener(data);
    }
  }

  private emitExit(event: PtyAdapter.PtyExitEvent): void {
    if (this.didExit) return;
    this.didExit = true;
    for (const listener of this.exitListeners) {
      listener(event);
    }
  }
}

function makePipePtyAdapter(
  spawnChildProcess: PipeChildProcessSpawner = nodeChildProcess.spawn,
): PtyAdapter.PtyAdapter["Service"] {
  return PtyAdapter.PtyAdapter.of({
    spawn: Effect.fn("NodePtyAdapter.spawnPipeFallback")(function* (input) {
      const process = yield* Effect.try({
        try: () =>
          spawnChildProcess(input.shell, input.args ?? [], {
            cwd: input.cwd,
            env: input.env,
            stdio: ["pipe", "pipe", "pipe"],
          }),
        catch: (cause) =>
          new PtyAdapter.PtySpawnError({
            adapter: "child-process-pipe",
            shell: input.shell,
            cause,
          }),
      });
      return new PipePtyProcess(process);
    }),
  });
}

const resolveNodePtySpawnHelperPath = Effect.gen(function* () {
  const requireForNodePty = NodeModule.createRequire(import.meta.url);
  const path = yield* Path.Path;
  const fs = yield* FileSystem.FileSystem;
  const platform = yield* HostProcessPlatform;
  const architecture = yield* HostProcessArchitecture;

  const packageJsonPath = requireForNodePty.resolve("node-pty/package.json");
  const packageDir = path.dirname(packageJsonPath);
  const candidates = [
    path.join(packageDir, "build", "Release", "spawn-helper"),
    path.join(packageDir, "build", "Debug", "spawn-helper"),
    path.join(packageDir, "prebuilds", `${platform}-${architecture}`, "spawn-helper"),
  ];

  for (const candidate of candidates) {
    if (yield* fs.exists(candidate)) {
      return candidate;
    }
  }
  return null;
}).pipe(Effect.orElseSucceed(() => null));

const ensureNodePtySpawnHelperExecutable = Effect.fn(function* () {
  const fs = yield* FileSystem.FileSystem;
  const platform = yield* HostProcessPlatform;
  if (platform === "win32") return;
  if (didEnsureSpawnHelperExecutable) return;

  const helperPath = yield* resolveNodePtySpawnHelperPath;
  if (!helperPath) return;
  didEnsureSpawnHelperExecutable = true;

  if (!(yield* fs.exists(helperPath))) {
    return;
  }

  // Best-effort: avoid FileSystem.stat in packaged mode where some fs metadata can be missing.
  yield* fs.chmod(helperPath, 0o755).pipe(Effect.orElseSucceed(() => undefined));
});

class NodePtyProcess implements PtyAdapter.PtyProcess {
  private readonly process: import("node-pty").IPty;
  private readonly platform: NodeJS.Platform;

  constructor(process: import("node-pty").IPty, platform: NodeJS.Platform) {
    this.process = process;
    this.platform = platform;
  }

  get pid(): number {
    return this.process.pid;
  }

  write(data: string): void {
    this.process.write(data);
  }

  resize(cols: number, rows: number): void {
    this.process.resize(cols, rows);
  }

  kill(signal?: string): void {
    // node-pty terminates the Windows process tree without a POSIX signal.
    this.process.kill(this.platform === "win32" ? undefined : signal);
  }

  onData(callback: (data: string) => void): () => void {
    const disposable = this.process.onData(callback);
    return () => {
      disposable.dispose();
    };
  }

  onExit(callback: (event: PtyAdapter.PtyExitEvent) => void): () => void {
    const disposable = this.process.onExit((event) => {
      callback({
        exitCode: event.exitCode,
        signal: event.signal ?? null,
      });
    });
    return () => {
      disposable.dispose();
    };
  }
}

export const make = Effect.fn("NodePtyAdapter.make")(function* (
  loadNodePtyModule: NodePtyModuleLoader = () => import("node-pty"),
) {
  const fs = yield* FileSystem.FileSystem;
  const path = yield* Path.Path;
  const platform = yield* HostProcessPlatform;
  const architecture = yield* HostProcessArchitecture;

  const nodePty = yield* Effect.tryPromise({
    try: loadNodePtyModule,
    catch: (cause) =>
      new NodePtyModuleLoadError({
        platform,
        architecture,
        cause,
      }),
  }).pipe(Effect.orDie);

  const ensureNodePtySpawnHelperExecutableCached = yield* Effect.cached(
    ensureNodePtySpawnHelperExecutable().pipe(
      Effect.provideService(FileSystem.FileSystem, fs),
      Effect.provideService(Path.Path, path),
      Effect.provideService(HostProcessPlatform, platform),
      Effect.provideService(HostProcessArchitecture, architecture),
      Effect.orElseSucceed(() => undefined),
    ),
  );

  return PtyAdapter.PtyAdapter.of({
    spawn: Effect.fn("NodePtyAdapter.spawn")(function* (input) {
      yield* ensureNodePtySpawnHelperExecutableCached;
      // node-pty only writes `name` into the child's TERM on the Unix path;
      // the ConPTY path leaves the environment untouched, so Windows children
      // inherit a missing or 16-color TERM unless it is set here.
      const env =
        platform === "win32" && input.env["TERM"] === undefined
          ? { ...input.env, TERM: "xterm-256color" }
          : input.env;
      const ptyProcess = yield* Effect.try({
        try: () =>
          nodePty.spawn(input.shell, input.args ?? [], {
            cwd: input.cwd,
            cols: input.cols,
            rows: input.rows,
            env,
            name: "xterm-256color",
          }),
        catch: (cause) =>
          new PtyAdapter.PtySpawnError({
            adapter: "node-pty",
            shell: input.shell,
            cause,
          }),
      });
      return new NodePtyProcess(ptyProcess, platform);
    }),
  });
});

export const layer = Layer.effect(PtyAdapter.PtyAdapter, make());

export const makeWithFallback = Effect.fn("NodePtyAdapter.makeWithFallback")(function* (
  loadNodePtyModule: NodePtyModuleLoader = () => import("node-pty"),
  spawnChildProcess: PipeChildProcessSpawner = nodeChildProcess.spawn,
) {
  const nodePtyExit = yield* Effect.exit(make(loadNodePtyModule));
  if (Exit.isSuccess(nodePtyExit)) return nodePtyExit.value;

  yield* Effect.logWarning("node-pty unavailable; using pipe-based terminal fallback", {
    cause: nodePtyExit.cause,
  });
  return makePipePtyAdapter(spawnChildProcess);
});

export const layerWithFallback = Layer.effect(PtyAdapter.PtyAdapter, makeWithFallback());
