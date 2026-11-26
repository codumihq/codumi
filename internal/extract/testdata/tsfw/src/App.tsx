import { createBrowserRouter } from './fw';

function Layout() { return null; }
function Home() { return null; }
function Orders() { return null; }
function Order() { return null; }
function Settings() { return null; }

export function Rotas() {
  return (
    <Routes>
      <Route path="/" element={<Layout />}>
        <Route index element={<Home />} />
        <Route path="pedidos" element={<Orders />}>
          <Route path=":id" element={<Order />} />
        </Route>
      </Route>
    </Routes>
  );
}

export const router = createBrowserRouter([
  { path: '/conta', Component: Layout, children: [{ path: 'config', element: <Settings /> }] },
]);
