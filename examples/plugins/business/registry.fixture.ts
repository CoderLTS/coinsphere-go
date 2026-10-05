// CI compiles the business example into the normal frontend registry.
import type { FrontendPluginModule } from "./sdk";
export const frontendPlugins: readonly {
  id: string;
  version: string;
  load: () => Promise<FrontendPluginModule>;
}[] = [
  {
    id: "example.business",
    version: "1.0.0",
    load: () => import("./installed/example_business/frontend/index.ts"),
  },
];
