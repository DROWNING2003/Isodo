import path from 'node:path';
import { createOpenAPI } from 'fumadocs-openapi/server';

export const openapi = createOpenAPI({
  input: {
    public: path.resolve(process.cwd(), '../api/openapi/openapi-public.yaml'),
  },
  includeDocument: false,
});
