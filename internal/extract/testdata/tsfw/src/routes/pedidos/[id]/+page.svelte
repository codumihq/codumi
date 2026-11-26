<script lang="ts">
  import Botao from '$lib/Botao.svelte';
  import { formatar, buscar as carregar } from '$lib/api';
  import { supabase } from '$lib/sb';
  export let data;
  const p = carregar(data.id);
  async function pagar() {
    await supabase.from('orders').update({ status: 'paid' });
    await fetch(`/api/pedidos/${data.id}/pago`, { method: 'POST' });
  }
</script>

<h1>Pedido {formatar(data.total)}</h1>
<Botao texto="Pagar" on:click={pagar} />
