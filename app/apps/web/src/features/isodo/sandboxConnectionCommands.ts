import { EnvironmentRegistry, EnvironmentSupervisor } from "@t3tools/client-runtime/connection";
import { createRuntimeCommand } from "@t3tools/client-runtime/state/runtime";
import type { EnvironmentId } from "@t3tools/contracts";
import * as Effect from "effect/Effect";
import { connectionAtomRuntime } from "../../connection/runtime";

// Closing a transport must not remove pairing credentials, projects or drafts.
export const setSandboxEnvironmentConnected = createRuntimeCommand(connectionAtomRuntime, {
  label: "isodo:sandbox-connection",
  execute: ({ environmentId, connected }: { environmentId: EnvironmentId; connected: boolean }) =>
    EnvironmentRegistry.pipe(
      Effect.flatMap((registry) =>
        registry.run(
          environmentId,
          EnvironmentSupervisor.pipe(
            Effect.flatMap((supervisor) =>
              connected ? supervisor.connect : supervisor.disconnect,
            ),
          ),
        ),
      ),
    ),
});
