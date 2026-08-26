import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

const webDir = dirname(fileURLToPath(import.meta.url));

export default defineConfig({
    base: "/image-workbench/",
    plugins: [react()],
    build: {
        outDir: "../../backend/internal/web/dist/image-workbench",
        emptyOutDir: true,
    },
    server: {
        proxy: {
            "/api": "http://127.0.0.1:9100",
            "/v1": "http://127.0.0.1:9100",
            "/health": "http://127.0.0.1:9100",
        },
    },
    resolve: {
        alias: {
            "@": resolve(webDir, "src"),
        },
    },
    define: {
        __APP_VERSION__: JSON.stringify("DengDeng AI"),
        __APP_RELEASES__: JSON.stringify([]),
    },
});
