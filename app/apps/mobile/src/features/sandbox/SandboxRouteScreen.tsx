import { NativeHeaderToolbar, NativeStackScreenOptions } from "../../native/StackHeader";
import { useFocusEffect, useNavigation } from "@react-navigation/native";
import { useCallback, useEffect, useRef, useState } from "react";
import { Alert, Platform, ScrollView, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";

import { AndroidScreenHeader } from "../../components/AndroidScreenHeader";
import { AppText as Text } from "../../components/AppText";
import { EmptyState } from "../../components/EmptyState";
import { ConnectionSheetButton } from "../connection/ConnectionSheetButton";
import { MOBILE_T3_SERVER_BUNDLE_URL, useSandboxApi } from "./useSandboxApi";
import { useSandboxCredentials } from "./useSandboxCredentials";
import { getAdditionalInjections } from "./useSandboxCredentials";
import { isSandboxConnecting, type SandboxInfo, sandboxUrl } from "./sandboxTypes";
import { detectCodexProviders, DEFAULT_SANDBOX_SKILLS } from "@t3tools/shared/sandbox";
import {
  useSavedRemoteConnections,
  useRemoteConnections,
} from "../../state/use-remote-environment-registry";
import { environmentCatalog } from "../../connection/catalog";
import { useAtomCommand } from "../../state/use-atom-command";
import { sandboxIdFromUrl } from "@t3tools/shared/sandbox";
import * as Cause from "effect/Cause";
import { AsyncResult } from "effect/unstable/reactivity";
import { cn } from "../../lib/cn";
import { SymbolView } from "../../components/AppSymbol";

function formatTTL(endAt: string): string {
  const end = new Date(endAt).getTime();
  const now = Date.now();
  const remaining = Math.max(0, end - now);
  const hours = Math.floor(remaining / 3600000);
  const minutes = Math.floor((remaining % 3600000) / 60000);
  if (hours > 0) return `${hours}h ${minutes}m remaining`;
  return `${minutes}m remaining`;
}

function ttlColor(endAt: string): string {
  const end = new Date(endAt).getTime();
  const remaining = end - Date.now();
  if (remaining < 30 * 60000) return "text-amber-500";
  return "text-foreground-muted";
}

export function SandboxRouteScreen() {
  const navigation = useNavigation();
  const insets = useSafeAreaInsets();
  const { credentials, hasRequired } = useSandboxCredentials();
  const {
    listSandboxes,
    deleteSandbox,
    pauseSandbox,
    resumeSandbox,
    refreshSandbox,
    connectSandbox,
    startSandboxT3Server,
    getPairingUrl,
  } = useSandboxApi({
    apiKey: credentials.e2bApiKey ?? "",
    apiUrl: credentials.e2bApiUrl,
    serverBundleUrl: MOBILE_T3_SERVER_BUNDLE_URL,
  });
  const { onConnectPress } = useRemoteConnections();
  const { savedConnectionsById } = useSavedRemoteConnections();
  const removeEnv = useAtomCommand(environmentCatalog.remove, "sandbox env remove");
  const [sandboxes, setSandboxes] = useState<SandboxInfo[]>([]);
  const [connecting, setConnecting] = useState<string | null>(null);

  const loadSandboxes = useCallback(async () => {
    if (!hasRequired) return;
    try {
      const list = await listSandboxes();
      setSandboxes(list);
      const apiIds = new Set(list.map((s) => s.sandboxID));
      for (const conn of Object.values(savedConnectionsById)) {
        const url = conn.displayUrl || conn.httpBaseUrl || "";
        if (!url) continue;
        const id = sandboxIdFromUrl(url);
        if (id && !apiIds.has(id)) await removeEnv(conn.environmentId);
      }
    } catch {}
  }, [hasRequired, listSandboxes, savedConnectionsById, removeEnv]);

  const lastLoadRef = useRef(0);

  useEffect(() => {
    if (!hasRequired) return;
    void loadSandboxes();
  }, [hasRequired, loadSandboxes]);

  useFocusEffect(
    useCallback(() => {
      if (!hasRequired) return;
      const now = Date.now();
      if (now - lastLoadRef.current < 10_000) return;
      lastLoadRef.current = now;
      void loadSandboxes();
    }, [hasRequired, loadSandboxes]),
  );

  const handleDelete = useCallback(
    (sandboxID: string) => {
      Alert.alert("Delete Sandbox", "This cannot be undone.", [
        { text: "Cancel", style: "cancel" },
        {
          text: "Delete",
          style: "destructive",
          onPress: async () => {
            try {
              await deleteSandbox(sandboxID);
              await loadSandboxes();
            } catch (err) {
              Alert.alert("Error", err instanceof Error ? err.message : String(err));
            }
          },
        },
      ]);
    },
    [deleteSandbox, loadSandboxes],
  );

  const handleRefresh = useCallback(
    async (sandboxID: string) => {
      try {
        await refreshSandbox(sandboxID, 3600);
        await loadSandboxes();
      } catch (err) {
        Alert.alert("Error", err instanceof Error ? err.message : String(err));
      }
    },
    [refreshSandbox, loadSandboxes],
  );

  const handlePause = useCallback(
    async (sandboxID: string) => {
      try {
        await pauseSandbox(sandboxID);
        await loadSandboxes();
      } catch (err) {
        Alert.alert("Error", err instanceof Error ? err.message : String(err));
      }
    },
    [pauseSandbox, loadSandboxes],
  );

  const handleResume = useCallback(
    async (sandboxID: string) => {
      try {
        await resumeSandbox(sandboxID, 3600);
        await loadSandboxes();
      } catch (err) {
        Alert.alert("Error", err instanceof Error ? err.message : String(err));
      }
    },
    [resumeSandbox, loadSandboxes],
  );

  const handleConnect = useCallback(
    async (sandboxID: string) => {
      setConnecting(sandboxID);
      try {
        const connectedSandbox = await connectSandbox(sandboxID, 3600);
        const additional = await getAdditionalInjections();
        const providers = detectCodexProviders([
          ...(credentials.openaiApiKey && credentials.openaiBaseUrl
            ? [{ base_url: credentials.openaiBaseUrl }]
            : []),
          ...additional.map((i) => ({ base_url: i.base_url })),
        ]);
        await startSandboxT3Server(connectedSandbox, providers, DEFAULT_SANDBOX_SKILLS);
        const pairingUrl = await getPairingUrl(
          connectedSandbox.sandboxID,
          connectedSandbox.domain,
          connectedSandbox,
        );
        const result = await onConnectPress(pairingUrl);
        if (AsyncResult.isSuccess(result)) {
          navigation.goBack();
        } else {
          const connectionError = Cause.squash(result.cause);
          Alert.alert(
            "Connection Failed",
            connectionError instanceof Error
              ? connectionError.message
              : "Could not connect to this sandbox.",
          );
        }
      } catch (err) {
        Alert.alert("Error", err instanceof Error ? err.message : String(err));
      } finally {
        setConnecting(null);
      }
    },
    [
      connectSandbox,
      credentials.openaiApiKey,
      credentials.openaiBaseUrl,
      getPairingUrl,
      navigation,
      onConnectPress,
      startSandboxT3Server,
    ],
  );

  if (!hasRequired) {
    return (
      <View collapsable={false} className="flex-1 bg-sheet">
        <NativeStackScreenOptions options={{ title: "Sandboxes" }} />
        <ScrollView
          contentInsetAdjustmentBehavior="automatic"
          className="flex-1"
          contentContainerStyle={{ paddingHorizontal: 20, paddingTop: 16 }}
        >
          <EmptyState
            variant="card"
            title="Credentials Required"
            detail="Set your Sandbox API key and OpenAI key in Settings to manage sandboxes."
            actionLabel="Open Settings"
            onAction={() => navigation.navigate("SettingsSheet")}
          />
        </ScrollView>
      </View>
    );
  }

  return (
    <View collapsable={false} className="flex-1 bg-sheet">
      <NativeStackScreenOptions
        options={{
          ...(Platform.OS === "android" ? { headerShown: false } : null),
          title: "Sandboxes",
        }}
      />
      {Platform.OS === "android" ? (
        <AndroidScreenHeader
          title="Sandboxes"
          onBack={() => navigation.goBack()}
          actions={[
            {
              accessibilityLabel: "Refresh",
              icon: "arrow.clockwise",
              onPress: () => void loadSandboxes(),
            },
          ]}
        />
      ) : (
        <NativeHeaderToolbar placement="right">
          <NativeHeaderToolbar.Button
            icon="arrow.clockwise"
            onPress={() => void loadSandboxes()}
            separateBackground
          />
        </NativeHeaderToolbar>
      )}
      <ScrollView
        contentInsetAdjustmentBehavior="automatic"
        showsVerticalScrollIndicator={false}
        className="flex-1"
        contentInset={{ bottom: Math.max(insets.bottom, 18) + 80 }}
        contentContainerStyle={{ paddingHorizontal: 20, paddingTop: 16 }}
      >
        <View collapsable={false} className="gap-5">
          {sandboxes.length === 0 ? (
            <EmptyState
              variant="card"
              title="No Sandboxes"
              detail="Create a sandbox to run Boundly in the cloud."
            />
          ) : (
            <View collapsable={false} className="overflow-hidden rounded-[24px] bg-card">
              {sandboxes.map((sb, i) => (
                <SandboxListItem
                  key={sb.sandboxID}
                  sandbox={sb}
                  isConnecting={isSandboxConnecting(sb.sandboxID, connecting)}
                  isFirst={i === 0}
                  onConnect={() => void handleConnect(sb.sandboxID)}
                  onPause={() => void handlePause(sb.sandboxID)}
                  onResume={() => void handleResume(sb.sandboxID)}
                  onRefresh={() => void handleRefresh(sb.sandboxID)}
                  onDelete={() => handleDelete(sb.sandboxID)}
                />
              ))}
            </View>
          )}
        </View>
      </ScrollView>
      <View
        collapsable={false}
        className="absolute bottom-4 right-5"
        style={{ paddingBottom: insets.bottom }}
      >
        <ConnectionSheetButton
          icon="plus"
          label="Create Sandbox"
          tone="primary"
          onPress={() => navigation.navigate("SandboxNew")}
        />
      </View>
    </View>
  );
}

function SandboxListItem({
  sandbox: sb,
  isConnecting,
  isFirst,
  onConnect,
  onPause,
  onResume,
  onRefresh,
  onDelete,
}: {
  readonly sandbox: SandboxInfo;
  readonly isConnecting: boolean;
  readonly isFirst: boolean;
  readonly onConnect: () => void;
  readonly onPause: () => void;
  readonly onResume: () => void;
  readonly onRefresh: () => void;
  readonly onDelete: () => void;
}) {
  return (
    <View collapsable={false} className={cn("p-4 gap-2", !isFirst && "border-t border-border")}>
      <View collapsable={false} className="flex-row items-center justify-between">
        <View collapsable={false} className="flex-row items-center gap-2">
          <SymbolView name="cloud" size={16} tintColorClassName="accent-icon" type="monochrome" />
          <Text className="text-base font-t3-medium text-foreground">
            {sb.metadata?.name || sb.alias || sb.sandboxID.slice(0, 12)}
          </Text>
          {sb.state === "paused" && (
            <View className="px-1.5 py-0.5 rounded-full bg-amber-500/15">
              <Text className="text-[10px] font-t3-medium text-amber-500">Paused</Text>
            </View>
          )}
        </View>
        {sb.state === "running" && (
          <Text className={cn("text-xs", ttlColor(sb.endAt))}>{formatTTL(sb.endAt)}</Text>
        )}
      </View>
      <Text className="text-xs text-foreground-muted">
        {sandboxUrl(sb.sandboxID, sb.domain) ?? "Public domain unavailable"}
      </Text>
      <View collapsable={false} className="flex-row gap-2 mt-1">
        {sb.state === "running" ? (
          <ConnectionSheetButton
            compact
            icon="link"
            grow
            label={isConnecting ? "Connecting..." : "Connect"}
            disabled={isConnecting}
            tone="primary"
            onPress={onConnect}
          />
        ) : (
          <ConnectionSheetButton
            compact
            icon="play"
            grow
            label="Resume"
            tone="primary"
            onPress={onResume}
          />
        )}
        {sb.state === "running" && (
          <ConnectionSheetButton
            compact
            iconOnly
            icon="pause"
            label="Pause"
            tone="secondary"
            onPress={onPause}
          />
        )}
        <ConnectionSheetButton
          compact
          iconOnly
          icon="clock"
          label="Refresh"
          tone="secondary"
          onPress={onRefresh}
        />
        <ConnectionSheetButton
          compact
          iconOnly
          icon="trash"
          label="Delete"
          tone="danger"
          onPress={onDelete}
        />
      </View>
    </View>
  );
}
