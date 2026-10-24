import { openapi } from '@/lib/openapi';

export async function GET(_request: Request, context: { params: Promise<{ id: string }> }) {
  const { id } = await context.params;
  if (id !== 'public') return Response.json({ error: 'Not found' }, { status: 404 });
  const schema = await openapi.getSchema(id);
  return Response.json(schema.bundled);
}

export async function generateStaticParams() {
  return Object.keys(await openapi.getSchemas()).map((id) => ({ id }));
}
