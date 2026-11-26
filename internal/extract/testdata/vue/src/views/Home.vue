<script setup lang="ts">
import BaseBotao from '@/components/base/BaseBotao.vue'
import { formatar } from '../components/util'
const total = formatar(10)
</script>

<template>
  <BaseBotao @click="formatar(2)" />
  <base-botao />
  <Cartao />
</template>
