import { createFileRoute } from '../fw';

export const Route = createFileRoute('/posts/$id')({ component: PostPage });

function PostPage() {
  return null;
}
