'use client';

import { preload } from 'react-dom';
import { createOpenAPIPage } from 'fumadocs-openapi/ui';

export const OpenAPIPage = createOpenAPIPage({
  async loadDocument(id) {
    const url = `/api/openapi/${encodeURIComponent(id)}`;
    if (typeof window === 'undefined') {
      preload(url, { as: 'fetch', crossOrigin: 'anonymous' });
      const { openapi } = await import('@/lib/openapi');
      return (await openapi.getSchema(id)).bundled;
    }

    const response = await fetch(url);
    if (!response.ok) throw new Error(`Unable to load OpenAPI document: ${response.status}`);
    return response.json();
  },
});
