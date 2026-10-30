import * as Context from "effect/Context";
import * as Clock from "effect/Clock";
import * as Effect from "effect/Effect";
import * as Layer from "effect/Layer";
import type * as Scope from "effect/Scope";

import type { ConnectionCatalogEntry } from "./catalog.ts";
import type {
  ConnectionAttemptError,
  ConnectionAttemptStage,
  PreparedConnection,
} from "./model.ts";
import * as ConnectionResolver from "./resolver.ts";
import * as RpcSession from "../rpc/session.ts";

export type ConnectionDriverProgress =
  | {
      readonly stage: "preparing";
    }
  | {
      readonly stage: Exclude<ConnectionAttemptStage, "preparing">;
      readonly prepared: PreparedConnection;
    };

export interface EnvironmentConnectionLease {
  readonly prepared: PreparedConnection;
  readonly session: RpcSession.RpcSession;
}

export class ConnectionDriver extends Context.Service<
  ConnectionDriver,
  {
    readonly connect: (
      entry: ConnectionCatalogEntry,
      reportProgress: (progress: ConnectionDriverProgress) => Effect.Effect<void>,
    ) => Effect.Effect<EnvironmentConnectionLease, ConnectionAttemptError, Scope.Scope>;
  }
>()("@t3tools/client-runtime/connection/driver/ConnectionDriver") {}

/** @public Service construction is part of the canonical Effect module API. */
export const make = Effect.gen(function* () {
  const resolver = yield* ConnectionResolver.ConnectionResolver;
  const sessions = yield* RpcSession.RpcSessionFactory;

  const connect = Effect.fn("ConnectionDriver.connect")(function* (
    entry: ConnectionCatalogEntry,
    reportProgress: (progress: ConnectionDriverProgress) => Effect.Effect<void>,
  ) {
    const target = entry.target;
    yield* Effect.annotateCurrentSpan({
      "connection.environment.id": target.environmentId,
      "connection.target.kind": target._tag,
    });
    yield* reportProgress({ stage: "preparing" });
    const prepareStartedAt = yield* Clock.currentTimeMillis;
    const prepared = yield* resolver.prepare(entry);
    yield* Effect.logInfo("Connection stage completed.").pipe(
      Effect.annotateLogs({
        "environment.id": target.environmentId,
        stage: "resolver_prepare",
        duration_ms: (yield* Clock.currentTimeMillis) - prepareStartedAt,
      }),
    );
    yield* reportProgress({ stage: "opening", prepared });
    const connectStartedAt = yield* Clock.currentTimeMillis;
    const session = yield* sessions.connect(prepared);
    yield* Effect.logInfo("Connection stage completed.").pipe(
      Effect.annotateLogs({
        "environment.id": target.environmentId,
        stage: "websocket_connect",
        duration_ms: (yield* Clock.currentTimeMillis) - connectStartedAt,
      }),
    );
    yield* reportProgress({ stage: "synchronizing", prepared });
    const readyStartedAt = yield* Clock.currentTimeMillis;
    yield* session.ready;
    yield* Effect.logInfo("Connection stage completed.").pipe(
      Effect.annotateLogs({
        "environment.id": target.environmentId,
        stage: "initial_sync_ready",
        duration_ms: (yield* Clock.currentTimeMillis) - readyStartedAt,
      }),
    );
    return { prepared, session } satisfies EnvironmentConnectionLease;
  });

  return ConnectionDriver.of({ connect });
});

export const layer = Layer.effect(ConnectionDriver, make);
