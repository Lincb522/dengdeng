import type { CSSProperties } from "react";
import { Tooltip } from "antd";
import { Keyboard, Settings2 } from "lucide-react";
import { useTranslation } from "react-i18next";

import { AnimatedThemeToggler } from "@/components/ui/animated-theme-toggler";
import { changeAppLocale, type AppLocale } from "@/i18n";
import { canvasThemes } from "@/lib/canvas-theme";
import { formatBalance, useDengDengSession } from "@/dengdeng/session";
import { useThemeStore } from "@/stores/use-theme-store";

type UserStatusActionsProps = {
    showConfig?: boolean;
    variant?: "default" | "canvas";
    onOpenShortcuts?: () => void;
};

export function UserStatusActions({ showConfig = true, variant = "default", onOpenShortcuts }: UserStatusActionsProps) {
    const { i18n, t } = useTranslation();
    const theme = useThemeStore((state) => state.theme);
    const setTheme = useThemeStore((state) => state.setTheme);
    const user = useDengDengSession((state) => state.user);
    const keys = useDengDengSession((state) => state.keys);
    const selectedKeyId = useDengDengSession((state) => state.selectedKeyId);
    const openSettings = useDengDengSession((state) => state.openSettings);
    const canvasTheme = canvasThemes[theme];
    const naturalIconClass = "inline-flex size-7 shrink-0 cursor-pointer items-center justify-center rounded-md text-stone-600 transition-colors hover:bg-black/5 hover:text-stone-950 dark:text-stone-300 dark:hover:bg-white/10 dark:hover:text-white [&_svg]:size-4";
    const iconStyle: CSSProperties | undefined = variant === "canvas" ? { color: canvasTheme.node.text } : undefined;
    const selectedKey = keys.find((key) => key.id === selectedKeyId);
    const locale = i18n.resolvedLanguage as AppLocale;
    const nextLocale = locale === "zh-CN" ? "en-US" : "zh-CN";
    const languageLabel = t("topNav.switchLanguage", { language: t(nextLocale === "zh-CN" ? "locale.zhCN" : "locale.enUS") });

    return (
        <div className="inline-flex shrink-0 items-center gap-1">
            {showConfig ? (
                <button type="button" className="mr-1 inline-flex h-8 max-w-56 items-center gap-2 rounded-lg border border-black/10 bg-white/75 px-2.5 text-xs font-medium text-stone-700 shadow-sm backdrop-blur transition hover:bg-white dark:border-white/10 dark:bg-stone-900/75 dark:text-stone-200 dark:hover:bg-stone-900" style={iconStyle} onClick={openSettings} aria-label="工作台设置" title={selectedKey?.name || "工作台设置"}>
                    <Settings2 className="size-4" />
                    <span className="hidden max-w-28 truncate sm:block">{selectedKey?.name || "选择密钥"}</span>
                    <span className="hidden tabular-nums opacity-65 sm:block">{formatBalance(user?.balance_micro)}</span>
                </button>
            ) : null}
            <Tooltip title={languageLabel} mouseEnterDelay={0.2}>
                <button type="button" className={`${naturalIconClass} text-[11px] font-semibold tracking-tight`} style={iconStyle} onClick={() => void changeAppLocale(nextLocale)} aria-label={languageLabel}>
                    {locale === "zh-CN" ? "中" : "EN"}
                </button>
            </Tooltip>
            <AnimatedThemeToggler theme={theme} onThemeChange={setTheme} className={naturalIconClass} style={iconStyle} aria-label={t(theme === "dark" ? "topNav.lightTheme" : "topNav.darkTheme")} title={t(theme === "dark" ? "topNav.lightTheme" : "topNav.darkTheme")} />
            {onOpenShortcuts ? (
                <button type="button" className={naturalIconClass} style={iconStyle} onClick={onOpenShortcuts} aria-label={t("topNav.shortcuts")} title={t("topNav.shortcuts")}>
                    <Keyboard className="size-4" />
                </button>
            ) : null}
        </div>
    );
}
