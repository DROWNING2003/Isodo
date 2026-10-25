import { defineConfig } from "vite-plus";

export default defineConfig({
  pack: {
    entry: ["src/main.ts", "src/preload.ts"],
    format: "cjs",
    outDir: "dist-electron",
    dts: false,
    clean: true,
    outExtensions: () => ({ js: ".cjs" }),
  },
});
