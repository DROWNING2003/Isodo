import type { InjectionRule, InjectionRuleMaterial } from "../../api/public.generated";
import { modelEndpoint, modelsFromResponse } from "./agentConfigs";

export type ModelProtocol = "openai" | "anthropic";

export function modelDiscoveryHeaders(
  type: InjectionRule["type"],
  material: Pick<InjectionRuleMaterial, "api_key" | "token" | "headers">,
): Headers {
  const headers = new Headers({ Accept: "application/json" });
  if (type === "http") {
    for (const [name, value] of Object.entries(material.headers ?? {})) headers.set(name, value);
  } else {
    const secret = material.api_key ?? material.token ?? "";
    if (type === "anthropic") {
      headers.set("x-api-key", secret);
      headers.set("anthropic-version", "2023-06-01");
    } else if (type === "gemini") {
      headers.set("x-goog-api-key", secret);
    } else {
      headers.set("Authorization", `Bearer ${secret.replace(/^Bearer\s+/i, "")}`);
    }
  }
  return headers;
}

export async function discoverAgentModels(
  baseUrl: string,
  type: InjectionRule["type"],
  material: Pick<InjectionRuleMaterial, "api_key" | "token" | "headers">,
): Promise<string[]> {
  const base = baseUrl.trim().replace(/\/+$/, "");
  const url =
    type === "anthropic"
      ? `${base.replace(/\/v1$/, "")}/v1/models?limit=1000`
      : modelEndpoint(base);
  const response = await fetch(url, {
    headers: modelDiscoveryHeaders(type, material),
    credentials: "omit",
    redirect: "error",
    signal: AbortSignal.timeout(20_000),
  });
  if (!response.ok) throw new Error(`HTTP ${response.status}`);
  const models = [...new Set(modelsFromResponse(await response.json()))].filter(Boolean);
  if (!models.length) throw new Error("接口没有返回模型列表");
  return models;
}

export function validModelEndpoint(value: string): boolean {
  try {
    const url = new URL(value);
    return (
      ["http:", "https:"].includes(url.protocol) &&
      !url.username &&
      !url.password &&
      !url.search &&
      !url.hash
    );
  } catch {
    return false;
  }
}
