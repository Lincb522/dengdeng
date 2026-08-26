import { create } from "zustand";

import {
    encodeChannelModel,
    guessCapability,
    modelOptionName,
    type ChannelModel,
    type ModelCapability,
    useConfigStore,
} from "@/stores/use-config-store";

const TOKEN_KEY = "dd_token";
const SELECTED_KEY_STORAGE = "dengdeng.image-workbench.selected-key";

type DengDengUser = {
    id: number;
    email: string;
    balance_micro: number;
};

export type DengDengKey = {
    id: number;
    name: string;
    key_preview: string;
    status: string;
    secret_available: boolean;
};

type CatalogueItem = {
    name: string;
    kind: "chat" | "image";
    available: boolean;
};

type ModelsResponse = {
    data?: Array<{ id?: string }>;
    error?: { message?: string };
};

type UsageSummary = {
    today: { requests: number; input_tokens: number; output_tokens: number; cost_micro: number };
    month: { requests: number; input_tokens: number; output_tokens: number; cost_micro: number };
};

type SessionStatus = "loading" | "authenticated" | "missing" | "error";

type SessionStore = {
    status: SessionStatus;
    user: DengDengUser | null;
    usage: UsageSummary | null;
    keys: DengDengKey[];
    selectedKeyId: number;
    models: ChannelModel[];
    settingsOpen: boolean;
    busy: boolean;
    error: string;
    refresh: () => Promise<void>;
    refreshUsage: () => Promise<void>;
    selectKey: (keyId: number) => Promise<void>;
    openSettings: () => void;
    closeSettings: () => void;
};

function currentToken() {
    return localStorage.getItem(TOKEN_KEY) || "";
}

function clearActiveCredential() {
    const config = useConfigStore.getState().config;
    useConfigStore.setState({
        config: {
            ...config,
            apiKey: "",
            channels: config.channels.map((channel) => ({ ...channel, apiKey: "" })),
        },
    });
}

async function consoleRequest<T>(path: string): Promise<T> {
    const token = currentToken();
    const response = await fetch(path, {
        headers: token ? { Authorization: `Bearer ${token}` } : undefined,
    });
    const payload = await response.json().catch(() => null) as { data?: T; error?: { message?: string }; message?: string } | null;
    if (!response.ok) throw new Error(payload?.error?.message || payload?.message || `请求失败（${response.status}）`);
    return payload?.data as T;
}

function selectedKeyFromStorage(keys: DengDengKey[]) {
    const stored = Number(localStorage.getItem(SELECTED_KEY_STORAGE) || 0);
    return keys.find((key) => key.id === stored) || keys[0] || null;
}

function capabilityForModel(model: string, catalogue: Map<string, CatalogueItem>): ModelCapability {
    const item = catalogue.get(model.toLowerCase());
    if (item) return item.kind === "image" ? "image" : "text";
    return guessCapability(model);
}

function preferredModel(models: ChannelModel[], capability: ModelCapability, current: string) {
    const currentName = modelOptionName(current);
    const matched = models.find((model) => model.capability === capability && model.name === currentName)
        || models.find((model) => model.capability === capability);
    return matched?.name || "";
}

async function connectKey(key: DengDengKey) {
    const [secretResult, catalogueItems] = await Promise.all([
        consoleRequest<{ plain: string }>(`/api/user/keys/${key.id}/secret`),
        consoleRequest<CatalogueItem[] | null>("/api/user/model-catalog"),
    ]);
    if (!secretResult?.plain) throw new Error("无法读取所选密钥，请在密钥管理中重新生成");

    const response = await fetch("/v1/models", {
        headers: { Authorization: `Bearer ${secretResult.plain}` },
    });
    const payload = await response.json().catch(() => null) as ModelsResponse | null;
    if (!response.ok) throw new Error(payload?.error?.message || `读取模型失败（${response.status}）`);
    const actualNames = Array.from(new Set((payload?.data || []).map((item) => item.id?.trim()).filter((name): name is string => Boolean(name))));
    if (!actualNames.length) throw new Error("所选密钥当前没有可用模型");

    const catalogue = new Map((Array.isArray(catalogueItems) ? catalogueItems : [])
        .filter((item) => item.available)
        .map((item) => [item.name.toLowerCase(), item]));
    const models = actualNames.map((name) => ({ name, capability: capabilityForModel(name, catalogue) }));
    const config = useConfigStore.getState().config;
    const channelId = `dengdeng-key-${key.id}`;
    const channel = {
        id: channelId,
        name: key.name || key.key_preview,
        baseUrl: window.location.origin,
        apiKey: secretResult.plain,
        apiFormat: "openai" as const,
        models,
    };
    const modelOptions = models.map((model) => encodeChannelModel(channelId, model.name));
    const imageModel = preferredModel(models, "image", config.imageModel);
    const videoModel = preferredModel(models, "video", config.videoModel);
    const textModel = preferredModel(models, "text", config.textModel);
    const audioModel = preferredModel(models, "audio", config.audioModel);
    useConfigStore.setState({
        config: {
            ...config,
            channelMode: "local",
            baseUrl: window.location.origin,
            apiKey: secretResult.plain,
            apiFormat: "openai",
            channels: [channel],
            models: modelOptions,
            model: imageModel ? encodeChannelModel(channelId, imageModel) : modelOptions[0],
            imageModel: imageModel ? encodeChannelModel(channelId, imageModel) : "",
            videoModel: videoModel ? encodeChannelModel(channelId, videoModel) : "",
            textModel: textModel ? encodeChannelModel(channelId, textModel) : "",
            audioModel: audioModel ? encodeChannelModel(channelId, audioModel) : "",
        },
    });
    return models;
}

export const useDengDengSession = create<SessionStore>((set, get) => ({
    status: "loading",
    user: null,
    usage: null,
    keys: [],
    selectedKeyId: 0,
    models: [],
    settingsOpen: false,
    busy: false,
    error: "",
    refresh: async () => {
        if (!currentToken()) {
            clearActiveCredential();
            set({ status: "missing", user: null, usage: null, keys: [], selectedKeyId: 0, models: [], error: "" });
            return;
        }
        set({ busy: true, error: "" });
        try {
            const [user, rawKeys, usage] = await Promise.all([
                consoleRequest<DengDengUser>("/api/user/me"),
                consoleRequest<DengDengKey[] | null>("/api/user/keys"),
                consoleRequest<UsageSummary>("/api/user/usage/summary"),
            ]);
            const keys = (Array.isArray(rawKeys) ? rawKeys : []).filter((key) => key.status === "active" && key.secret_available);
            const selected = selectedKeyFromStorage(keys);
            if (!selected) clearActiveCredential();
            set({ status: "authenticated", user, usage, keys, selectedKeyId: selected?.id || 0, models: [], busy: Boolean(selected), error: selected ? "" : "没有可用于工作台的密钥" });
            if (selected) {
                const models = await connectKey(selected);
                set({ models, busy: false });
            }
        } catch (error) {
            const message = error instanceof Error ? error.message : "工作台配置加载失败";
            if (/登录|unauthorized|token|401/i.test(message)) set({ status: "missing", user: null, usage: null, keys: [], selectedKeyId: 0, models: [], busy: false, error: "" });
            else set({ status: get().user ? "authenticated" : "error", busy: false, error: message });
        }
    },
    refreshUsage: async () => {
        if (!currentToken()) return;
        try {
            const [user, usage] = await Promise.all([
                consoleRequest<DengDengUser>("/api/user/me"),
                consoleRequest<UsageSummary>("/api/user/usage/summary"),
            ]);
            set({ user, usage });
        } catch {}
    },
    selectKey: async (keyId) => {
        const key = get().keys.find((item) => item.id === keyId);
        if (!key) return;
        localStorage.setItem(SELECTED_KEY_STORAGE, String(key.id));
        clearActiveCredential();
        set({ selectedKeyId: key.id, models: [], busy: true, error: "" });
        try {
            const models = await connectKey(key);
            set({ models, busy: false });
        } catch (error) {
            set({ busy: false, error: error instanceof Error ? error.message : "切换密钥失败" });
        }
    },
    openSettings: () => set({ settingsOpen: true }),
    closeSettings: () => set({ settingsOpen: false }),
}));

export function formatBalance(balanceMicro = 0) {
    return `$${(balanceMicro / 1_000_000).toFixed(2)}`;
}
