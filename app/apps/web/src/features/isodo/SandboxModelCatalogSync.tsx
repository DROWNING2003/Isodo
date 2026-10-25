import { useEffect, useRef, useState } from "react";
import { useEnvironments } from "../../state/environments";
import { serverEnvironment } from "../../state/server";
import { useAtomCommand } from "../../state/use-atom-command";
import { sandboxIdForEnvironment } from "./sandboxEnvironmentMap";
import { sandboxModelSettingsPatch } from "./sandboxModels";
import { useQueryClient } from "@tanstack/react-query";
import {
  useServerSandboxCredentials,
  CREDENTIAL_DOCUMENTS_CHANGED,
  credentialDocumentsKey,
} from "./credentialDocuments";

export function SandboxModelCatalogSync() {
  const { environments } = useEnvironments();
  const catalog = useServerSandboxCredentials();
  const client = useQueryClient();
  const update = useAtomCommand(serverEnvironment.updateSettings, "同步沙箱模型目录");
  const pending = useRef(new Set<string>());
  const [version, refresh] = useState(0);
  useEffect(() => {
    const changed = () => {
      refresh((value) => value + 1);
      void client.invalidateQueries({ queryKey: credentialDocumentsKey });
    };
    window.addEventListener(CREDENTIAL_DOCUMENTS_CHANGED, changed);
    window.addEventListener("storage", changed);
    return () => {
      window.removeEventListener(CREDENTIAL_DOCUMENTS_CHANGED, changed);
      window.removeEventListener("storage", changed);
    };
  }, [client]);
  useEffect(() => {
    for (const environment of environments) {
      const sandboxId = sandboxIdForEnvironment(environment.environmentId);
      if (!sandboxId || environment.connection.phase !== "connected" || !environment.serverConfig)
        continue;
      const models = catalog.data?.[sandboxId]?.models;
      if (!models) continue;
      const patch = sandboxModelSettingsPatch(environment.serverConfig.settings, models);
      if (!patch) continue;
      const signature = `${environment.environmentId}:${version}:${JSON.stringify(patch)}`;
      if (pending.current.has(signature)) continue;
      pending.current.add(signature);
      void update({ environmentId: environment.environmentId, input: { patch } });
    }
  }, [environments, update, version, catalog.data]);
  return null;
}
