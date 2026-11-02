import { expect, it } from "vite-plus/test";
import { mergeManagedSkills } from "./managedSkills";
import type { SkillEntry, SkillSelection } from "../../api/public.generated";
const id = "11111111-1111-4111-8111-111111111111";
const catalog: SkillEntry[] = [
  {
    id,
    name: "review",
    description: "Read evidence",
    body: "review",
    source: "personal",
    favorite: true,
  },
];
const applied: SkillSelection = {
  skill_ids: [id],
  version: 2,
  applied_version: 2,
  sync_status: "applied",
  restart_required: true,
};
it("exposes only applied selection through the native composer Skill contract", () => {
  const manual = {
    name: "manual",
    path: "/workspace/.agents/skills/manual/SKILL.md",
    enabled: true,
  };
  expect(mergeManagedSkills([manual], catalog, applied)).toEqual([
    manual,
    {
      name: "review",
      description: "Read evidence",
      path: `/workspace/.agents/skills/isodo-${id}/SKILL.md`,
      enabled: true,
      scope: "repo",
    },
  ]);
  expect(mergeManagedSkills([], catalog, { ...applied, sync_status: "failed" })).toEqual([]);
  expect(mergeManagedSkills([], catalog, { ...applied, applied_version: 1 })).toEqual([]);
  expect(mergeManagedSkills([], catalog, undefined)).toEqual([]);
});
it("removes stale managed metadata after deselection while preserving other Skills", () => {
  const stale = {
    name: "review",
    path: `/workspace/.agents/skills/isodo-${id}/SKILL.md`,
    enabled: true,
  };
  expect(mergeManagedSkills([stale], catalog, { ...applied, skill_ids: [] })).toEqual([]);
  expect(mergeManagedSkills([stale], [], applied)).toEqual([]);
});
