import { getTemplateBuild, getTemplateTag, listTemplates } from "../../api/public.generated";

/** The default tag is also the version resolved when a sandbox is created. */
export async function loadCloudAppTemplates() {
  const candidates = [];
  let offset = 0;
  for (;;) {
    const page = await listTemplates({ scope: "available", limit: 100, offset });
    candidates.push(...page.items);
    offset += page.items.length;
    if (page.items.length === 0 || offset >= page.total) break;
  }
  const options = await Promise.all(
    candidates.map(async (template) => {
      try {
        const tag = await getTemplateTag(template.id, "default");
        const build = await getTemplateBuild(template.id, tag.build_id);
        return build.status === "ready"
          ? { id: template.id, label: template.name, ready: true }
          : null;
      } catch (error) {
        // An unpublished default build is not a creatable template. Other failures
        // must be visible rather than masquerading as an empty catalog.
        if (error instanceof Error && "status" in error && error.status === 404) return null;
        throw error;
      }
    }),
  );
  return options
    .filter((option) => option !== null)
    .sort(
      (a, b) =>
        Number(b.label === "base") - Number(a.label === "base") || a.label.localeCompare(b.label),
    );
}
