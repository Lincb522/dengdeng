import type { ReactNode } from "react";
import { usePromptSourceScheduler } from "@/hooks/use-prompt-source-scheduler";

export function ClientRootInit({ children }: { children: ReactNode }) {
    usePromptSourceScheduler();
    return <>{children}</>;
}
