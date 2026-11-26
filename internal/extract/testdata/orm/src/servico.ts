import { itens } from "./schema";
export async function listar() {
  await prisma.pedido.findMany({ where: { pago: true } });
  await db.select().from(itens);
  await db.insert(itens).values({});
  await db.query.itens.findFirst();
}
export async function criar() {
  return prisma.cliente.create({ data: {} });
}
