// stand-ins for the framework functions, so the example compiles alone
export const createFileRoute = (p: string) => (o: any) => o;
export const createServerFileRoute = (p: string) => ({ methods: (o: any) => o });
export const createBrowserRouter = (r: any[]) => r;
