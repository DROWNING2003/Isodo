import type { ServerProviderSkill } from "@t3tools/contracts";
import type { SkillEntry, SkillSelection } from "../../api/public.generated";

const managedPath = /^\/workspace\/\.agents\/skills\/isodo-[0-9a-f-]{36}\/SKILL\.md$/;

/** Overlay only Control-confirmed installed metadata on the provider snapshot.
 * Older Guest T3 builds can cache an empty provider Skill catalog; dispatch
 * still uses the existing native Skill chip and Codex input contract.
 */
export function mergeManagedSkills(
  native: readonly ServerProviderSkill[],
  catalog: readonly SkillEntry[],
  selection: SkillSelection | undefined,
): readonly ServerProviderSkill[] {
  const result = native.filter((skill) => !managedPath.test(skill.path));
  if (
    !selection ||
    selection.sync_status !== "applied" ||
    selection.version !== selection.applied_version
  )
    return result;
  const ids = new Set(selection.skill_ids);
  const names = new Set(result.map((skill) => skill.name));
  for (const skill of catalog) {
    if (!ids.has(skill.id) || names.has(skill.name)) continue;
    result.push({
      name: skill.name,
      description: skill.description,
      path: `/workspace/.agents/skills/isodo-${skill.id}/SKILL.md`,
      enabled: true,
      scope: "repo",
    });
    names.add(skill.name);
  }
  return result;
}
