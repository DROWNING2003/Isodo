import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { generateFiles } from 'fumadocs-openapi';
import { createOpenAPI } from 'fumadocs-openapi/server';

const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const appDir = path.resolve(scriptDir, '..');
const schemaPath = path.resolve(appDir, '../api/openapi/openapi-public.yaml');
const outputDir = path.resolve(appDir, 'content/docs/api-reference/endpoints');

const openapi = createOpenAPI({
  input: { public: schemaPath },
});

await generateFiles({
  input: openapi,
  output: outputDir,
  includeDescription: true,
  beforeWrite(files) {
    // Keep endpoint URLs stable while organizing navigation by developer task.
    const groups = new Map([
      ['沙箱管理', []],
      ['应用访问', []],
      ['安全连接', []],
      ['模板管理', []],
      ['模板构建', []],
      ['模板版本与标签', []],
    ]);
    for (const entry of this.generatedEntries.public) {
      if (entry.type !== 'operation') {
        throw new Error(`Unexpected OpenAPI entry: ${entry.type}`);
      }
      const route = entry.item.path;
      let group;
      if (route.startsWith('/v1/injection-rules')) group = '安全连接';
      else if (/\/app(?:-link)?$/.test(route)) group = '应用访问';
      else if (route.startsWith('/v1/sandboxes')) group = '沙箱管理';
      else if (route.includes('/builds')) group = '模板构建';
      else if (route.includes('/tags/')) group = '模板版本与标签';
      else if (route.startsWith('/v1/templates') || route === '/v1/system-templates') group = '模板管理';
      else throw new Error(`Assign an API navigation group for ${route}`);
      groups.get(group).push(entry.path.replace(/\.mdx$/, ''));
    }
    files.push({
      path: 'meta.json',
      content: JSON.stringify({
        title: 'HTTP 端点',
        pages: [...groups].flatMap(([title, pages]) => [`---${title}---`, ...pages]),
      }, null, 2) + '\n',
    });
  },
});
