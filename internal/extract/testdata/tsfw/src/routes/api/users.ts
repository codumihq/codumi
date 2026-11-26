import { createServerFileRoute } from '../../fw';

export const ServerRoute = createServerFileRoute('/api/users').methods({
  GET: async () => new Response('[]'),
  POST: async () => new Response('ok'),
});
