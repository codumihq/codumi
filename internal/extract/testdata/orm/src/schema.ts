import { pgTable, serial } from "drizzle-orm/pg-core";
export const itens = pgTable("itens_pedido", { id: serial("id") });
