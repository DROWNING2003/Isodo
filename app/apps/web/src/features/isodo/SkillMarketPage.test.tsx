import { act, type ReactNode } from "react";
import { create, type ReactTestRenderer } from "react-test-renderer";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, afterEach, it, expect, vi } from "vite-plus/test";
import { SkillMarketPage, SandboxSkillSelection } from "./SkillMarketPage";
import { useSandboxSkillSelection } from "./skillMarket";
const fixture = vi.hoisted(() => ({
  generation: 0,
  items: [
    {
      id: "skill-a",
      name: "review",
      description: "Review evidence",
      body: "Inspect errors",
      source: "market",
      favorite: false,
    },
  ],
  create: vi.fn(),
  favorite: vi.fn(),
  unfavorite: vi.fn(),
  set: vi.fn(),
  saved: {
    skill_ids: [] as string[],
    version: 0,
    applied_version: 0,
    sync_status: "pending",
    restart_required: false,
  },
}));
vi.mock("./auth", () => ({
  getAuthGeneration: () => fixture.generation,
  useAuth: () => ({ status: "authenticated" }),
}));
vi.mock("../../api/public.generated", () => ({
  listSkills: async () => ({ items: fixture.items }),
  createSkill: (...args: unknown[]) => fixture.create(...args),
  favoriteSkill: (...args: unknown[]) => fixture.favorite(...args),
  unfavoriteSkill: (...args: unknown[]) => fixture.unfavorite(...args),
  getSandboxSkills: async () => fixture.saved,
  setSandboxSkills: (...args: unknown[]) => fixture.set(...args),
}));
vi.mock("./CloudPages", () => ({
  CloudPage: ({ children }: { children: ReactNode }) => <div>{children}</div>,
}));
vi.mock("../../components/ui/button", () => ({
  Button: ({ children, ...props }: React.ComponentProps<"button">) => (
    <button {...props}>{children}</button>
  ),
}));
vi.mock("../../components/ui/input", () => ({
  Input: (props: React.ComponentProps<"input">) => <input {...props} />,
}));
vi.mock("../../components/ui/dialog", () => {
  const Slot = ({ children }: { children: ReactNode }) => <div>{children}</div>;
  return {
    Dialog: ({ open, children }: { open: boolean; children: ReactNode }) =>
      open ? <div>{children}</div> : null,
    DialogPopup: Slot,
    DialogHeader: Slot,
    DialogTitle: Slot,
    DialogDescription: Slot,
    DialogPanel: Slot,
  };
});
let renderer: ReactTestRenderer;
let client: QueryClient;
let state: ReturnType<typeof useSandboxSkillSelection>;
function Selection() {
  state = useSandboxSkillSelection("sandbox-a", true);
  return <SandboxSkillSelection state={state} disabled={false} />;
}
async function render(element: ReactNode) {
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    renderer = create(<QueryClientProvider client={client}>{element}</QueryClientProvider>);
  });
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 15));
  });
}
beforeEach(() => {
  fixture.generation = 0;
  fixture.items[0]!.favorite = false;
  fixture.saved = {
    skill_ids: [],
    version: 0,
    applied_version: 0,
    sync_status: "pending",
    restart_required: false,
  };
  vi.clearAllMocks();
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
});
afterEach(async () => {
  await act(async () => renderer?.unmount());
  client?.clear();
  vi.unstubAllGlobals();
});
it("offers market search, explicit favorites and a private Skill creation form", async () => {
  await render(<SkillMarketPage />);
  await act(async () =>
    renderer.root
      .findByProps({ "aria-label": "搜索 Skill" })
      .props.onChange({ target: { value: "no-match" } }),
  );
  expect(renderer.root.findAllByProps({ "aria-label": "查看 review Skill" })).toHaveLength(0);
  await act(async () =>
    renderer.root
      .findByProps({ "aria-label": "搜索 Skill" })
      .props.onChange({ target: { value: "" } }),
  );

  await act(async () => renderer.root.findByProps({ "aria-label": "收藏 review" }).props.onClick());
  expect(fixture.favorite).toHaveBeenCalledWith("skill-a");
  await act(async () =>
    renderer.root
      .findAllByType("button")
      .find((x) => x.children.some((v) => v === "制作 Skill"))!
      .props.onClick(),
  );
  const fields = renderer.root.findAllByType("input").filter((x) => !x.props["aria-label"]);
  await act(async () => {
    fields[0]!.props.onChange({ target: { value: "my-review" } });
    fields[1]!.props.onChange({ target: { value: "Read code" } });
    renderer.root
      .findByType("textarea")
      .props.onChange({ target: { value: "Inspect actual behavior" } });
  });
  await act(async () => renderer.root.findByType("form").props.onSubmit({ preventDefault() {} }));
  expect(fixture.create).toHaveBeenCalledWith({
    name: "my-review",
    description: "Read code",
    body: "Inspect actual behavior",
  });
});
it("restores saved selection, sends complete deselection and reports a failed sync", async () => {
  fixture.items[0]!.favorite = true;
  fixture.saved.skill_ids = ["skill-a"];
  fixture.saved.version = 1;
  fixture.saved.applied_version = 1;
  fixture.saved.sync_status = "applied";
  await render(<Selection />);
  expect(renderer.root.findByType("input").props.checked).toBe(true);
  await act(async () =>
    renderer.root.findByType("input").props.onChange({ target: { checked: false } }),
  );
  expect(state.changed).toBe(true);
  fixture.set.mockResolvedValueOnce({
    ...fixture.saved,
    skill_ids: [],
    version: 2,
    sync_status: "failed",
  });
  await expect(state.apply()).rejects.toThrow("尚未同步");
  expect(fixture.set).toHaveBeenCalledWith("sandbox-a", { skill_ids: [] });
});
it("clears private form content after account generation changes", async () => {
  await render(<SkillMarketPage />);
  await act(async () =>
    renderer.root
      .findAllByType("button")
      .find((x) => x.children.some((v) => v === "制作 Skill"))!
      .props.onClick(),
  );
  await act(async () =>
    renderer.root
      .findByType("textarea")
      .props.onChange({ target: { value: "private instruction" } }),
  );
  fixture.generation++;
  await act(async () =>
    renderer.update(
      <QueryClientProvider client={client}>
        <SkillMarketPage />
      </QueryClientProvider>,
    ),
  );
  expect(renderer.root.findAllByType("textarea")).toHaveLength(0);
});
