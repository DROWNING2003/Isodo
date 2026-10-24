import { createMDX } from 'fumadocs-mdx/next';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const withMDX = createMDX();
const projectRoot = path.dirname(fileURLToPath(import.meta.url));

export default withMDX({
  reactStrictMode: true,
  turbopack: { root: projectRoot },
});
