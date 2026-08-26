import { Images, Library } from "lucide-react";
import { Link, useLocation } from "react-router-dom";
import { useTranslation } from "react-i18next";

import { UserStatusActions } from "@/components/layout/user-status-actions";
import { cn } from "@/lib/utils";

export function AppTopNav() {
    const { pathname } = useLocation();
    const { t } = useTranslation();
    if (/^\/canvas\/[^/]+/.test(pathname)) return null;

    return (
        <header className="sticky top-0 z-20 h-14 shrink-0 border-b border-stone-200 bg-background/90 backdrop-blur-xl dark:border-stone-800">
            <div className="mx-auto flex h-full max-w-7xl items-center justify-between gap-4 px-4 sm:px-6">
                <div className="flex min-w-0 items-center gap-6">
                    <a href="/" className="flex shrink-0 items-center gap-2 text-sm font-semibold text-stone-950 dark:text-stone-100">
                        <span className="grid size-7 place-items-center rounded-lg bg-stone-950 text-xs font-bold text-white dark:bg-white dark:text-stone-950">D</span>
                        <span>DengDeng AI</span>
                    </a>
                    <nav className="hidden items-center gap-1 sm:flex">
                        <Link to="/canvas" className={cn("rounded-lg px-3 py-2 text-sm", pathname.startsWith("/canvas") ? "bg-stone-950 text-white dark:bg-white dark:text-stone-950" : "text-stone-500 hover:text-stone-950 dark:hover:text-stone-100")}>画布</Link>
                        <Link to="/assets" className={cn("inline-flex items-center gap-1.5 rounded-lg px-3 py-2 text-sm", pathname.startsWith("/assets") ? "bg-stone-950 text-white dark:bg-white dark:text-stone-950" : "text-stone-500 hover:text-stone-950 dark:hover:text-stone-100")}><Images className="size-4" />资产</Link>
                        <Link to="/library" className={cn("inline-flex items-center gap-1.5 rounded-lg px-3 py-2 text-sm", pathname.startsWith("/library") ? "bg-stone-950 text-white dark:bg-white dark:text-stone-950" : "text-stone-500 hover:text-stone-950 dark:hover:text-stone-100")}><Library className="size-4" />{t("creationLibrary.title")}</Link>
                    </nav>
                </div>
                <UserStatusActions />
            </div>
        </header>
    );
}
