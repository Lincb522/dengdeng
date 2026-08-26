import { useEffect, type ReactNode } from "react";
import { Button, Drawer, Select, Spin } from "antd";
import { ArrowLeft, CreditCard, KeyRound, Library, RefreshCw } from "lucide-react";

import { encodeChannelModel, modelOptionName, useConfigStore } from "@/stores/use-config-store";
import { formatBalance, useDengDengSession } from "@/dengdeng/session";

export function DengDengSessionProvider({ children }: { children: ReactNode }) {
    const status = useDengDengSession((state) => state.status);
    const refresh = useDengDengSession((state) => state.refresh);
    const refreshUsage = useDengDengSession((state) => state.refreshUsage);
    const configOpen = useConfigStore((state) => state.isConfigOpen);
    const setConfigDialogOpen = useConfigStore((state) => state.setConfigDialogOpen);

    useEffect(() => {
        void refresh();
    }, [refresh]);

    useEffect(() => {
        if (status !== "authenticated") return;
        const timer = window.setInterval(() => void refreshUsage(), 30_000);
        const onFocus = () => void refreshUsage();
        window.addEventListener("focus", onFocus);
        return () => {
            window.clearInterval(timer);
            window.removeEventListener("focus", onFocus);
        };
    }, [refreshUsage, status]);

    useEffect(() => {
        if (!configOpen) return;
        useDengDengSession.getState().openSettings();
        setConfigDialogOpen(false);
    }, [configOpen, setConfigDialogOpen]);

    if (status === "loading") {
        return <main className="grid h-dvh place-items-center bg-[#f8f8f6] text-stone-600 dark:bg-[#11110f] dark:text-stone-300"><Spin size="large" /></main>;
    }
    if (status === "missing") {
        const returnTo = `${window.location.pathname}${window.location.search}${window.location.hash}`;
        window.location.replace(`/login?redirect=${encodeURIComponent(returnTo)}`);
        return <main className="grid h-dvh place-items-center bg-[#f8f8f6] text-sm text-stone-600 dark:bg-[#11110f] dark:text-stone-300">正在前往登录页…</main>;
    }
    if (status === "error") {
        return (
            <main className="grid h-dvh place-items-center bg-[#f8f8f6] px-5 dark:bg-[#11110f]">
                <section className="w-full max-w-sm rounded-2xl border border-stone-200 bg-white p-6 dark:border-stone-800 dark:bg-stone-950">
                    <h1 className="text-lg font-semibold text-stone-950 dark:text-stone-100">工作台连接失败</h1>
                    <p className="mt-2 text-sm text-stone-500">{useDengDengSession.getState().error}</p>
                    <div className="mt-5 flex gap-2"><Button icon={<ArrowLeft className="size-4" />} href="/dashboard">返回控制台</Button><Button type="primary" onClick={() => void refresh()}>重试</Button></div>
                </section>
            </main>
        );
    }

    return <>{children}<DengDengSettingsDrawer /></>;
}

function DengDengSettingsDrawer() {
    const user = useDengDengSession((state) => state.user);
    const usage = useDengDengSession((state) => state.usage);
    const keys = useDengDengSession((state) => state.keys);
    const selectedKeyId = useDengDengSession((state) => state.selectedKeyId);
    const models = useDengDengSession((state) => state.models);
    const open = useDengDengSession((state) => state.settingsOpen);
    const busy = useDengDengSession((state) => state.busy);
    const error = useDengDengSession((state) => state.error);
    const close = useDengDengSession((state) => state.closeSettings);
    const refresh = useDengDengSession((state) => state.refresh);
    const selectKey = useDengDengSession((state) => state.selectKey);
    const config = useConfigStore((state) => state.config);
    const updateConfig = useConfigStore((state) => state.updateConfig);
    const channelId = selectedKeyId ? `dengdeng-key-${selectedKeyId}` : "";
    const imageModels = models.filter((model) => model.capability === "image");

    return (
        <Drawer title="工作台设置" open={open} onClose={close} width="min(380px, 100vw)" styles={{ body: { padding: 20 } }}>
            <div className="space-y-6">
                <div className="flex items-center justify-between border-b border-stone-200 pb-4 dark:border-stone-800">
                    <div className="min-w-0"><strong className="block truncate text-sm">{user?.email}</strong><span className="mt-1 block text-xs text-stone-500">账户余额</span></div>
                    <strong className="text-lg tabular-nums">{formatBalance(user?.balance_micro)}</strong>
                </div>
                <dl className="grid grid-cols-2 gap-px overflow-hidden rounded-xl border border-stone-200 bg-stone-200 dark:border-stone-800 dark:bg-stone-800">
                    <div className="bg-white p-3 dark:bg-stone-950"><dt className="text-xs text-stone-500">今日请求</dt><dd className="mt-1 text-base font-semibold tabular-nums">{usage?.today.requests || 0}</dd></div>
                    <div className="bg-white p-3 dark:bg-stone-950"><dt className="text-xs text-stone-500">今日消费</dt><dd className="mt-1 text-base font-semibold tabular-nums">{formatBalance(usage?.today.cost_micro)}</dd></div>
                </dl>
                <label className="block space-y-2">
                    <span className="text-sm font-medium">API 密钥</span>
                    <Select className="w-full" value={selectedKeyId || undefined} loading={busy} placeholder="选择密钥" options={keys.map((key) => ({ value: key.id, label: `${key.name} · ${key.key_preview}` }))} onChange={(value) => void selectKey(value)} />
                </label>
                <label className="block space-y-2">
                    <span className="flex items-center justify-between text-sm font-medium"><span>生图模型</span><small className="font-normal text-stone-500">{imageModels.length} 个</small></span>
                    <Select
                        className="w-full"
                        value={modelOptionName(config.imageModel) || undefined}
                        disabled={!imageModels.length || busy}
                        placeholder="暂无可用生图模型"
                        options={imageModels.map((model) => ({ value: model.name, label: model.name }))}
                        onChange={(value) => {
                            const encoded = encodeChannelModel(channelId, value);
                            updateConfig("imageModel", encoded);
                            updateConfig("model", encoded);
                        }}
                    />
                </label>
                {error ? <p className="rounded-lg bg-red-50 px-3 py-2 text-sm text-red-700 dark:bg-red-950/30 dark:text-red-300">{error}</p> : null}
                <div className="grid grid-cols-3 gap-2">
                    <Button icon={<Library className="size-4" />} href="/image-workbench/library">创作库</Button>
                    <Button icon={<KeyRound className="size-4" />} href="/keys">密钥管理</Button>
                    <Button icon={<CreditCard className="size-4" />} href="/wallet">充值</Button>
                </div>
                <Button block icon={<RefreshCw className="size-4" />} loading={busy} onClick={() => void refresh()}>刷新配置</Button>
            </div>
        </Drawer>
    );
}
