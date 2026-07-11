import { createRouter, createWebHistory } from 'vue-router'

declare module 'vue-router' {
  interface RouteMeta {
    title?: string
  }
}

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    {
      path: '/',
      name: 'dashboard',
      component: () => import('@/pages/DashboardPage.vue'),
      meta: { title: 'Dashboard' },
    },
    {
      path: '/routes',
      name: 'routes',
      component: () => import('@/pages/RoutesPage.vue'),
      meta: { title: 'Routes' },
    },
    {
      path: '/routes/:name',
      name: 'route-detail',
      component: () => import('@/pages/RouteDetailPage.vue'),
      meta: { title: 'Route Detail' },
    },
    {
      path: '/transports',
      name: 'transports',
      component: () => import('@/pages/TransportsPage.vue'),
      meta: { title: 'Transports' },
    },
    {
      path: '/logs',
      name: 'logs',
      component: () => import('@/pages/LogsPage.vue'),
      meta: { title: 'Logs' },
    },
    {
      path: '/diagnostics',
      name: 'diagnostics',
      component: () => import('@/pages/DiagnosticsPage.vue'),
      meta: { title: 'Diagnostics' },
    },
    {
      path: '/settings',
      name: 'settings',
      component: () => import('@/pages/SettingsPage.vue'),
      meta: { title: 'Settings' },
    },
    { path: '/:pathMatch(.*)*', redirect: '/' },
  ],
})

router.afterEach((to) => {
  document.title = to.meta.title ? `${to.meta.title} · PipeRouter` : 'PipeRouter'
})
