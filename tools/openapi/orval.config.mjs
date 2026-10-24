export default {
  admin: {
    input: '../../api/openapi/openapi.yaml',
    output: {
      target: '../../app/apps/web/src/api/admin.generated.ts',
      client: 'fetch',
      httpClient: 'fetch',
      mode: 'single',
      prettier: false,
      override: {
        fetch: { includeHttpResponseReturnType: false },
        mutator: { path: '../../app/apps/web/src/api/transport.ts', name: 'isodoFetch' },
      },
    },
  },
  public: {
    input: '../../api/openapi/openapi-public.yaml',
    output: {
      target: '../../app/apps/web/src/api/public.generated.ts',
      client: 'fetch',
      httpClient: 'fetch',
      mode: 'single',
      prettier: false,
      override: {
        fetch: { includeHttpResponseReturnType: false },
        mutator: { path: '../../app/apps/web/src/api/transport.ts', name: 'isodoFetch' },
      },
    },
  },
};
