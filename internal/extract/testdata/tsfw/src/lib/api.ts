export function formatar(v: number) { return 'R$ ' + v; }
export async function buscar(id: string) { return fetch(`/api/pedidos/${id}`); }
