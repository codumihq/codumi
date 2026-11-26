import { createRouter, createWebHistory } from 'vue-router'
import Home from '../views/Home.vue'

export default createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', component: Home },
    { path: '/pedidos', component: () => import('@/views/Pedidos.vue'),
      children: [{ path: ':id', component: () => import('../views/Pedido.vue') }] },
  ],
})
