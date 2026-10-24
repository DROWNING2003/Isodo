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
});
