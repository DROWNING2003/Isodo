import { NativeStackScreenOptions } from "../../native/StackHeader";
import { useNavigation } from "@react-navigation/native";
import { useCallback, useEffect, useRef, useState } from "react";
import { Alert, Platform, ScrollView, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";

import { AndroidScreenHeader } from "../../components/AndroidScreenHeader";
import { AppText as Text, AppTextInput as TextInput } from "../../components/AppText";
import { ConnectionSheetButton } from "../connection/ConnectionSheetButton";
import {
  useSandboxCredentials,
  getAdditionalInjections,
  setAdditionalInjections,
} from "./useSandboxCredentials";
import type { HttpInjection } from "@t3tools/sandbox-client";
import {
  DEFAULT_OPENAI_BASE_URL,
  DEFAULT_TEMPLATE_ID,
  mergeSandboxCredentials,
} from "./sandboxTypes";
import { DEFAULT_SANDBOX_API_URL } from "./useSandboxApi";

const BASE_URL_PRESETS = [
  {
    label: "OpenAI",
    value: "https://api.openai.com",
    header: "Authorization",
    headerPrefix: "Bearer ",
  },
  { label: "Anthropic", value: "https://api.anthropic.com", header: "x-api-key", headerPrefix: "" },
  {
    label: "Gemini",
    value: "https://generativelanguage.googleapis.com",
    header: "x-goog-api-key",
    headerPrefix: "",
  },
  {
    label: "Qiniu",
    value: "https://api.qnaigc.com",
    header: "Authorization",
    headerPrefix: "Bearer ",
  },
];

interface Entry {
  id: number;
  baseUrl: string;
  apiKey: string;
  header: string;
  headerPrefix: string;
}

let nextId = 0;

function loadEntries(injections: HttpInjection[]): Entry[] {
  return injections.map((inj, i) => {
    const key = inj.headers ? (Object.keys(inj.headers)[0] ?? "Authorization") : "Authorization";
    const val = inj.headers ? (Object.values(inj.headers)[0] ?? "") : "";
    const preset = BASE_URL_PRESETS.find((p) => p.value === inj.base_url);
    return {
      id: i,
      baseUrl: inj.base_url,
      apiKey: val.replace(preset?.headerPrefix ?? "", ""),
      header: preset?.header ?? key,
      headerPrefix: preset?.headerPrefix ?? "",
    };
  });
}

function saveEntries(entries: Entry[]): HttpInjection[] {
  return entries
    .filter((e) => e.baseUrl.trim() && e.apiKey.trim())
    .map((e) => ({
      type: "http",
      base_url: e.baseUrl.trim(),
      headers: { [e.header || "Authorization"]: `${e.headerPrefix}${e.apiKey.trim()}` },
    }));
}

export function SettingsSandboxCredentials() {
  const { credentials, loaded, saveCredentials } = useSandboxCredentials();
  const navigation = useNavigation();
  const insets = useSafeAreaInsets();
  const initialized = useRef(false);

  const [e2bKey, setE2bKey] = useState(credentials.e2bApiKey ?? "");
  const [e2bApiUrl, setE2bApiUrl] = useState(credentials.e2bApiUrl ?? "");
  const [openaiApiKey, setOpenaiApiKey] = useState(credentials.openaiApiKey ?? "");
  const [openaiBaseUrl, setOpenaiBaseUrl] = useState(
    credentials.openaiBaseUrl || DEFAULT_OPENAI_BASE_URL,
  );
  const [templateID, setTemplateID] = useState(credentials.templateID || DEFAULT_TEMPLATE_ID);
  const [entries, setEntries] = useState<Entry[]>([]);
  const [saving, setSaving] = useState(false);
  const [baseUrlMenuOpen, setBaseUrlMenuOpen] = useState<number | null>(null);

  useEffect(() => {
    if (!loaded || initialized.current) return;
    initialized.current = true;
    setE2bKey(credentials.e2bApiKey ?? "");
    setE2bApiUrl(credentials.e2bApiUrl ?? "");
    setOpenaiApiKey(credentials.openaiApiKey ?? "");
    setOpenaiBaseUrl(credentials.openaiBaseUrl || DEFAULT_OPENAI_BASE_URL);
    setTemplateID(credentials.templateID || DEFAULT_TEMPLATE_ID);
    getAdditionalInjections().then((inj) => {
      const loaded = loadEntries(inj);
      setEntries(loaded);
      nextId = loaded.length;
    });
  }, [credentials, loaded]);

  const addEntry = useCallback(() => {
    setEntries((prev) => [
      ...prev,
      { id: nextId++, baseUrl: "", apiKey: "", header: "Authorization", headerPrefix: "Bearer " },
    ]);
  }, []);

  const removeEntry = useCallback((id: number) => {
    setEntries((prev) => prev.filter((e) => e.id !== id));
  }, []);

  const updateEntry = useCallback((id: number, field: keyof Entry, value: string) => {
    setEntries((prev) =>
      prev.map((e) => {
        if (e.id !== id) return e;
        const updated = { ...e, [field]: value };
        if (field === "baseUrl") {
          const preset = BASE_URL_PRESETS.find((p) => p.value === value);
          if (preset) {
            updated.header = preset.header;
            updated.headerPrefix = preset.headerPrefix;
          }
        }
        return updated;
      }),
    );
  }, []);

  const handleSave = useCallback(async () => {
    setSaving(true);
    try {
      await saveCredentials(
        mergeSandboxCredentials(credentials, {
          e2bApiKey: e2bKey.trim(),
          e2bApiUrl: e2bApiUrl.trim(),
          openaiApiKey: openaiApiKey.trim(),
          openaiBaseUrl: openaiBaseUrl.trim(),
          templateID: templateID.trim() || DEFAULT_TEMPLATE_ID,
        }),
      );
      await setAdditionalInjections(saveEntries(entries));
      if (navigation.canGoBack()) navigation.goBack();
    } catch (error) {
      Alert.alert(
        "Could Not Save Credentials",
        error instanceof Error ? error.message : String(error),
      );
    } finally {
      setSaving(false);
    }
  }, [
    e2bKey,
    e2bApiUrl,
    openaiApiKey,
    openaiBaseUrl,
    templateID,
    entries,
    saveCredentials,
    navigation,
    credentials,
  ]);

  return (
    <View collapsable={false} className="flex-1 bg-sheet">
      <NativeStackScreenOptions
        options={{
          ...(Platform.OS === "android" ? { headerShown: false } : null),
          title: "Sandbox Credentials",
        }}
      />
      {Platform.OS === "android" ? (
        <AndroidScreenHeader title="Sandbox Credentials" onBack={() => navigation.goBack()} />
      ) : null}
      <ScrollView
        contentInsetAdjustmentBehavior="automatic"
        showsVerticalScrollIndicator={false}
        className="flex-1"
        contentInset={{ bottom: Math.max(insets.bottom, 18) + 18 }}
        contentContainerStyle={{ paddingHorizontal: 20, paddingTop: 16 }}
      >
        <View collapsable={false} className="gap-5">
          <View collapsable={false} className="gap-4 rounded-[24px] bg-card p-4">
            <View collapsable={false} className="gap-1.5">
              <Text className="text-2xs font-t3-bold tracking-[0.8px] uppercase text-foreground-muted">
                Sandbox API Key
              </Text>
              <TextInput
                autoCapitalize="none"
                autoCorrect={false}
                secureTextEntry
                placeholder="sk-..."
                value={e2bKey}
                onChangeText={setE2bKey}
                className="rounded-[14px] border border-input-border bg-input px-4 py-3.5 text-base text-foreground"
              />
            </View>
            <View collapsable={false} className="gap-1.5">
              <Text className="text-2xs font-t3-bold tracking-[0.8px] uppercase text-foreground-muted">
                Sandbox API URL
              </Text>
              <TextInput
                autoCapitalize="none"
                autoCorrect={false}
                keyboardType="url"
                placeholder={DEFAULT_SANDBOX_API_URL}
                value={e2bApiUrl}
                onChangeText={setE2bApiUrl}
                className="rounded-[14px] border border-input-border bg-input px-4 py-3.5 text-base text-foreground"
              />
            </View>
            <View collapsable={false} className="gap-1.5">
              <Text className="text-2xs font-t3-bold tracking-[0.8px] uppercase text-foreground-muted">
                Template ID
              </Text>
              <TextInput
                autoCapitalize="none"
                autoCorrect={false}
                placeholder={DEFAULT_TEMPLATE_ID}
                value={templateID}
                onChangeText={setTemplateID}
                className="rounded-[14px] border border-input-border bg-input px-4 py-3.5 text-base text-foreground"
              />
            </View>
            <View collapsable={false} className="gap-1.5">
              <Text className="text-2xs font-t3-bold tracking-[0.8px] uppercase text-foreground-muted">
                OpenAI API Key
              </Text>
              <TextInput
                autoCapitalize="none"
                autoCorrect={false}
                secureTextEntry
                placeholder="sk-..."
                value={openaiApiKey}
                onChangeText={setOpenaiApiKey}
                className="rounded-[14px] border border-input-border bg-input px-4 py-3.5 text-base text-foreground"
              />
            </View>
            <View collapsable={false} className="gap-1.5">
              <Text className="text-2xs font-t3-bold tracking-[0.8px] uppercase text-foreground-muted">
                OpenAI-Compatible Base URL
              </Text>
              <TextInput
                autoCapitalize="none"
                autoCorrect={false}
                keyboardType="url"
                placeholder={DEFAULT_OPENAI_BASE_URL}
                value={openaiBaseUrl}
                onChangeText={setOpenaiBaseUrl}
                className="rounded-[14px] border border-input-border bg-input px-4 py-3.5 text-base text-foreground"
              />
            </View>

            <View collapsable={false} className="gap-3 mt-2">
              <View className="flex-row items-center justify-between">
                <Text className="text-2xs font-t3-bold tracking-[0.8px] uppercase text-foreground-muted">
                  Provider
                </Text>
                <ConnectionSheetButton
                  compact
                  icon="plus"
                  label="Add"
                  tone="secondary"
                  onPress={addEntry}
                />
              </View>
              {entries.map((entry) => {
                const preset = BASE_URL_PRESETS.find((p) => p.value === entry.baseUrl);
                return (
                  <View
                    key={entry.id}
                    className="gap-2 rounded-[16px] border border-input-border p-3"
                  >
                    <View className="flex-row items-center gap-2">
                      <ConnectionSheetButton
                        compact
                        icon="list.bullet"
                        label={preset ? preset.label : "Custom"}
                        tone="secondary"
                        onPress={() =>
                          setBaseUrlMenuOpen(baseUrlMenuOpen === entry.id ? null : entry.id)
                        }
                      />
                      <TextInput
                        className="flex-1 rounded-[10px] border border-input-border bg-input px-3 py-2 text-xs text-foreground"
                        autoCapitalize="none"
                        autoCorrect={false}
                        keyboardType="url"
                        placeholder="https://api.example.com"
                        value={entry.baseUrl}
                        onChangeText={(v) => updateEntry(entry.id, "baseUrl", v)}
                      />
                      <ConnectionSheetButton
                        compact
                        icon="trash"
                        label=""
                        tone="danger"
                        onPress={() => removeEntry(entry.id)}
                      />
                    </View>
                    {baseUrlMenuOpen === entry.id && (
                      <View className="flex-row flex-wrap gap-1.5">
                        {BASE_URL_PRESETS.map((p) => (
                          <ConnectionSheetButton
                            key={p.value}
                            compact
                            icon="list.bullet"
                            label={p.label}
                            tone="secondary"
                            onPress={() => {
                              updateEntry(entry.id, "baseUrl", p.value);
                              setBaseUrlMenuOpen(null);
                            }}
                          />
                        ))}
                      </View>
                    )}
                    <TextInput
                      className="rounded-[10px] border border-input-border bg-input px-3 py-2 text-xs text-foreground"
                      autoCapitalize="none"
                      autoCorrect={false}
                      secureTextEntry
                      placeholder={`API Key (${entry.header})`}
                      value={entry.apiKey}
                      onChangeText={(v) => updateEntry(entry.id, "apiKey", v)}
                    />
                  </View>
                );
              })}
            </View>

            <ConnectionSheetButton
              icon="checkmark"
              label={saving ? "Saving..." : "Save"}
              tone="primary"
              disabled={!loaded || saving}
              onPress={() => {
                void handleSave();
              }}
            />
          </View>
        </View>
      </ScrollView>
    </View>
  );
}
