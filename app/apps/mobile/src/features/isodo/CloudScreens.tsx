import { useSyncExternalStore, useState } from "react";
import { useNavigation, StackActions, type StaticScreenProps } from "@react-navigation/native";
import { Alert, Pressable, ScrollView, View, useWindowDimensions } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import {
  createMockCloudService,
  type Agent,
  type PermissionMode,
  type SandboxTemplate,
} from "@t3tools/sandbox-client/cloud";
import { AppText as Text, AppTextInput as TextInput } from "../../components/AppText";
import { SymbolView } from "../../components/AppSymbol";
import { ComposerEditor } from "../../components/ComposerEditor";
import { NativeStackScreenOptions } from "../../native/StackHeader";
import { deriveLayout } from "../../lib/layout";

const cloud = createMockCloudService();
const useCloud = () => useSyncExternalStore(cloud.subscribe, cloud.getSnapshot);
const modes: readonly PermissionMode[] = ["supervised", "accept-edits", "auto", "full"];
const modeLabels: Record<PermissionMode, string> = {
  supervised: "监督执行",
  "accept-edits": "自动接受编辑",
  auto: "自动",
  full: "完全访问",
};
const agents: readonly Agent[] = ["Claude Code", "Codex", "dsh", "OpenCode"];
function action(fn: () => void) {
  try {
    fn();
  } catch (error) {
    Alert.alert("操作未完成", error instanceof Error ? error.message : "请重试");
  }
}
function Action({
  label,
  onPress,
  secondary = false,
}: {
  label: string;
  onPress: () => void;
  secondary?: boolean;
}) {
  return (
    <Pressable
      accessibilityRole="button"
      accessibilityLabel={label}
      onPress={onPress}
      className={`min-h-11 items-center justify-center rounded-xl px-4 py-3 active:opacity-70 ${secondary ? "border border-border bg-card" : "bg-primary"}`}
    >
      <Text
        className={`text-sm font-t3-medium ${secondary ? "text-foreground" : "text-primary-foreground"}`}
      >
        {label}
      </Text>
    </Pressable>
  );
}

export function CloudHomeScreen() {
  const navigation = useNavigation();
  const state = useCloud();
  const layout = deriveLayout(useWindowDimensions());
  const insets = useSafeAreaInsets();
  return (
    <ScrollView
      className="flex-1 bg-screen"
      contentInsetAdjustmentBehavior="automatic"
      contentContainerStyle={{ padding: 20, paddingBottom: insets.bottom + 24 }}
    >
      <NativeStackScreenOptions options={{ title: "isodo", headerShown: true }} />
      <View className="gap-5">
        <View>
          <Text className="text-2xl font-t3-bold">我的云端沙箱</Text>
          <Text className="mt-2 text-sm text-foreground-muted">
            Agent、文件与工具，都在独立的云端环境中。
          </Text>
        </View>
        <View className="flex-row gap-2">
          <Action label="创建沙箱" onPress={() => navigation.navigate("SandboxNew")} />
          <Action
            label="凭证库"
            secondary
            onPress={() => navigation.navigate("CloudCredentials")}
          />
        </View>
        <View className="flex-row flex-wrap justify-between gap-y-4">
          {state.sandboxes.map((sandbox) => (
            <View
              key={sandbox.id}
              style={{ width: layout.usesSplitView ? "48%" : "100%" }}
              className="gap-4 rounded-[20px] border border-border bg-card p-5"
            >
              <View className="flex-row items-center justify-between">
                <SymbolView name="cloud" size={23} tintColorClassName="accent-icon" />
                <Text className="text-xs text-foreground-muted">
                  {sandbox.status === "running" ? "运行中" : "休眠中"}
                </Text>
              </View>
              <Text className="text-lg font-t3-bold">{sandbox.name}</Text>
              <Text className="text-sm text-foreground-muted">
                {sandbox.cpu} CPU · {sandbox.memoryGB} GB RAM · 云端
              </Text>
              <Text className="text-xs text-foreground-muted">
                {sandbox.connectionIds.length} 个安全连接 ·{" "}
                {state.tasks.filter((t) => t.sandboxId === sandbox.id).length} 个任务
              </Text>
              <View className="flex-row gap-2">
                <Action
                  label="打开沙箱"
                  onPress={() => navigation.navigate("CloudSandbox", { sandboxId: sandbox.id })}
                />
                <Action
                  label={sandbox.status === "running" ? "休眠" : "唤醒"}
                  secondary
                  onPress={() =>
                    cloud.setStatus(sandbox.id, sandbox.status === "running" ? "paused" : "running")
                  }
                />
              </View>
            </View>
          ))}
        </View>
        <Text className="text-xs text-foreground-muted">
          演示数据 · Mock。当前操作不会创建真实云资源。
        </Text>
      </View>
    </ScrollView>
  );
}

export function CloudCreateScreen() {
  const navigation = useNavigation();
  const [name, setName] = useState("");
  const [template, setTemplate] = useState<SandboxTemplate>("dev");
  return (
    <ScrollView
      className="flex-1 bg-screen"
      contentInsetAdjustmentBehavior="automatic"
      contentContainerStyle={{ padding: 24, gap: 20 }}
    >
      <NativeStackScreenOptions options={{ title: "创建云端沙箱", headerShown: true }} />
      <Text className="text-xl font-t3-bold">从一个用途开始</Text>
      <View className="gap-2">
        {(
          [
            ["dev", "项目开发"],
            ["daily", "日常助手"],
            ["blank", "空白环境"],
          ] as const
        ).map(([key, label]) => (
          <Action
            key={key}
            label={label}
            secondary={template !== key}
            onPress={() => setTemplate(key)}
          />
        ))}
      </View>
      <TextInput
        accessibilityLabel="沙箱名称"
        value={name}
        onChangeText={setName}
        placeholder="我的云端工作空间"
        className="rounded-xl border border-border bg-card p-4 text-base text-foreground"
        maxLength={48}
      />
      <Text className="text-sm text-foreground-muted">
        云端 · 2 CPU / 4 GB / 20 GB。创建后按需授权安全连接。
      </Text>
      <Action
        label="创建云端沙箱"
        onPress={() =>
          action(() => {
            const sandbox = cloud.createSandbox({ name, template });
            navigation.dispatch(StackActions.replace("CloudSandbox", { sandboxId: sandbox.id }));
          })
        }
      />
    </ScrollView>
  );
}

export function CloudCredentialsScreen() {
  const state = useCloud();
  return (
    <ScrollView
      className="flex-1 bg-screen"
      contentInsetAdjustmentBehavior="automatic"
      contentContainerStyle={{ padding: 20, gap: 16 }}
    >
      <NativeStackScreenOptions options={{ title: "凭证库", headerShown: true }} />
      <Text className="text-xl font-t3-bold">安全连接，按沙箱授权</Text>
      {state.credentials.map((credential) => (
        <View key={credential.id} className="gap-2 rounded-[20px] border border-border bg-card p-5">
          <Text className="text-base font-t3-bold">{credential.label}</Text>
          <Text className="text-sm text-foreground-muted">{credential.service} · 示例连接</Text>
        </View>
      ))}
      <Text className="text-sm text-foreground-muted">
        在沙箱的安全连接中授权或撤销。演示不接收真实 API Key。
      </Text>
    </ScrollView>
  );
}

export function CloudSandboxScreen({ route }: StaticScreenProps<{ sandboxId: string }>) {
  const { sandboxId } = route.params;
  const state = useCloud();
  const insets = useSafeAreaInsets();
  const sandbox = state.sandboxes.find((s) => s.id === sandboxId);
  const tasks = state.tasks.filter((t) => t.sandboxId === sandboxId);
  const [tab, setTab] = useState<"tasks" | "files" | "connections" | "snapshots">("tasks");
  const [prompt, setPrompt] = useState("");
  const [agent, setAgent] = useState<Agent>("Claude Code");
  const [mode, setMode] = useState<PermissionMode>("supervised");
  const navigation = useNavigation();
  if (!sandbox)
    return (
      <View>
        <Text>沙箱不存在</Text>
      </View>
    );
  return (
    <View className="flex-1 bg-screen">
      <NativeStackScreenOptions options={{ title: sandbox.name, headerShown: true }} />
      <View className="border-b border-border px-4 py-2">
        <Text className="text-xs text-foreground-muted">
          云端沙箱 · Mock · {sandbox.status === "running" ? "运行中" : "休眠中"}
        </Text>
      </View>
      <View className="flex-row gap-1 px-3 py-2">
        {(
          [
            ["tasks", "任务"],
            ["files", "文件"],
            ["connections", "连接"],
            ["snapshots", "快照"],
          ] as const
        ).map(([key, label]) => (
          <Pressable
            key={key}
            accessibilityRole="button"
            onPress={() => setTab(key)}
            className={`min-h-11 flex-1 items-center justify-center rounded-xl ${tab === key ? "bg-card" : ""}`}
          >
            <Text className="text-sm">{label}</Text>
          </Pressable>
        ))}
      </View>
      <ScrollView
        className="flex-1"
        contentInsetAdjustmentBehavior="automatic"
        contentContainerStyle={{ padding: 16, gap: 16 }}
      >
        {tab === "tasks" &&
          (sandbox.status === "paused" ? (
            <Action label="唤醒沙箱" onPress={() => cloud.setStatus(sandboxId, "running")} />
          ) : (
            <>
              <Text className="text-xl font-t3-bold">今天，想完成什么？</Text>
              {tasks.map((task) => (
                <View
                  key={task.id}
                  className="gap-3 rounded-[20px] border border-border bg-card p-4"
                >
                  <Text className="text-base font-t3-bold">{task.title}</Text>
                  <Text className="text-xs text-foreground-muted">{task.agent} · 模拟执行</Text>
                  <Text className="text-sm">{task.output}</Text>
                  {task.status === "approval" && (
                    <View className="flex-row gap-2">
                      <Action
                        label="允许本次"
                        onPress={() => action(() => cloud.resolveTask(task.id, true))}
                      />
                      <Action
                        label="拒绝"
                        secondary
                        onPress={() => action(() => cloud.resolveTask(task.id, false))}
                      />
                    </View>
                  )}
                </View>
              ))}
            </>
          ))}
        {tab === "files" &&
          (sandbox.files.length ? (
            sandbox.files.map((file) => (
              <View key={file.path} className="gap-2 rounded-xl border border-border bg-card p-4">
                <Text className="text-sm font-t3-bold">/workspace/{file.path}</Text>
                <Text className="text-sm text-foreground-muted">{file.content}</Text>
              </View>
            ))
          ) : (
            <Text className="text-sm text-foreground-muted">
              还没有文件，让 Agent 创建第一个产出。
            </Text>
          ))}
        {tab === "connections" && (
          <>
            <Text className="text-sm text-foreground-muted">凭证在沙箱外管理，按需授权。</Text>
            {state.credentials.map((credential) => {
              const granted = sandbox.connectionIds.includes(credential.id);
              return (
                <View
                  key={credential.id}
                  className="gap-3 rounded-xl border border-border bg-card p-4"
                >
                  <Text className="text-base">{credential.label}</Text>
                  <Action
                    label={granted ? "撤销" : "授权"}
                    secondary={granted}
                    onPress={() =>
                      action(() => cloud.setConnection(sandboxId, credential.id, !granted))
                    }
                  />
                </View>
              );
            })}
          </>
        )}
        {tab === "snapshots" && (
          <>
            <Text className="text-sm text-foreground-muted">
              保存文件与环境，不含运行中内存。恢复时重新授权连接。
            </Text>
            <Action
              label="创建快照"
              onPress={() =>
                action(() => {
                  cloud.takeSnapshot(sandboxId);
                })
              }
            />
            {state.snapshots
              .filter((s) => s.sourceSandboxId === sandboxId)
              .map((snapshot) => (
                <View
                  key={snapshot.id}
                  className="gap-3 rounded-xl border border-border bg-card p-4"
                >
                  <Text className="text-sm">{snapshot.name}</Text>
                  <Action
                    label="创建独立沙箱"
                    secondary
                    onPress={() =>
                      action(() => {
                        const restored = cloud.restoreSnapshot(snapshot.id);
                        navigation.dispatch(
                          StackActions.push("CloudSandbox", { sandboxId: restored.id }),
                        );
                      })
                    }
                  />
                </View>
              ))}
          </>
        )}
      </ScrollView>
      {tab === "tasks" && sandbox.status === "running" && (
        <View
          className="gap-2 border-t border-border px-4 pt-3"
          style={{ paddingBottom: Math.max(insets.bottom, 12) }}
        >
          <View className="rounded-xl border border-border bg-card p-3">
            <ComposerEditor
              value={prompt}
              onChangeText={setPrompt}
              placeholder="让 Agent 在这个沙箱里做点什么…"
              style={{ minHeight: 64, maxHeight: 130 }}
              multiline
            />
            <View className="flex-row items-center justify-between gap-2">
              <Pressable
                accessibilityRole="button"
                accessibilityLabel="切换 Agent"
                onPress={() => setAgent(agents[(agents.indexOf(agent) + 1) % agents.length]!)}
                className="min-h-11 justify-center"
              >
                <Text className="text-xs text-foreground-muted">{agent} ▾</Text>
              </Pressable>
              <Pressable
                accessibilityRole="button"
                accessibilityLabel="切换执行权限"
                onPress={() => setMode(modes[(modes.indexOf(mode) + 1) % modes.length]!)}
                className="min-h-11 justify-center"
              >
                <Text className="text-xs text-foreground-muted">{modeLabels[mode]} ▾</Text>
              </Pressable>
              <Action
                label="发送"
                onPress={() =>
                  action(() => {
                    cloud.createTask({ sandboxId, prompt, agent, mode });
                    setPrompt("");
                  })
                }
              />
            </View>
          </View>
          <Text className="text-center text-[10px] text-foreground-muted">模拟执行 · 云端沙箱</Text>
        </View>
      )}
    </View>
  );
}
