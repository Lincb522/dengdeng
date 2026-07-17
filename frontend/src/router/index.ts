import { createRouter, createWebHistory } from 'vue-router'
import { getToken } from '../api/client'
import { useAuth } from '../stores/auth'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/login', name: 'login', component: () => import('../views/LoginView.vue') },
    { path: '/legal/:documentId', name: 'legal', component: () => import('../views/LegalView.vue') },
    { path: '/studio', name: 'studio', component: () => import('../views/ImageStudioView.vue') },
    {
      path: '/',
      component: () => import('../layouts/ConsoleLayout.vue'),
      children: [
        { path: '', redirect: '/dashboard' },
        { path: 'dashboard', name: 'dashboard', component: () => import('../views/DashboardView.vue') },
        { path: 'models', name: 'models', component: () => import('../views/ModelPlazaView.vue') },
        { path: 'keys', name: 'keys', component: () => import('../views/KeysView.vue') },
        { path: 'usage', name: 'usage', component: () => import('../views/UsageView.vue') },
        { path: 'wallet', name: 'wallet', component: () => import('../views/WalletView.vue') },
        { path: 'profile', name: 'profile', component: () => import('../views/ProfileView.vue') },
        {
          path: 'admin',
          meta: { admin: true },
          children: [
            { path: '', redirect: '/admin/overview' },
            { path: 'overview', name: 'admin-overview', component: () => import('../views/admin/OverviewView.vue') },
            { path: 'monitoring', name: 'admin-monitoring', component: () => import('../views/admin/MonitoringView.vue') },
            { path: 'alerts', name: 'admin-alerts', component: () => import('../views/admin/AlertsView.vue') },
            { path: 'backups', name: 'admin-backups', component: () => import('../views/admin/BackupsView.vue') },
            { path: 'groups', name: 'admin-groups', component: () => import('../views/admin/GroupsView.vue') },
            { path: 'accounts', name: 'admin-accounts', component: () => import('../views/admin/AccountsView.vue') },
            { path: 'users', name: 'admin-users', component: () => import('../views/admin/UsersView.vue') },
            { path: 'models', name: 'admin-models', component: () => import('../views/admin/ModelsView.vue') },
            { path: 'prices', name: 'admin-prices', component: () => import('../views/admin/PricesView.vue') },
          { path: 'redeem', name: 'admin-redeem', component: () => import('../views/admin/RedeemView.vue') },
            { path: 'payment', name: 'admin-payment', component: () => import('../views/admin/PaymentView.vue') },
			  { path: 'proxies', name: 'admin-proxies', component: () => import('../views/admin/ProxiesView.vue') },
			  { path: 'settings', name: 'admin-settings', component: () => import('../views/admin/SettingsView.vue') },
          { path: 'usage', name: 'admin-usage', component: () => import('../views/admin/UsageView.vue') },
          ],
        },
      ],
    },
    { path: '/:pathMatch(.*)*', redirect: '/dashboard' },
  ],
})

router.beforeEach(async (to) => {
  if (to.name === 'login' || to.name === 'legal' || to.name === 'studio') return true
  if (!getToken()) return { name: 'login' }

  const auth = useAuth()
  if (!auth.user) {
    const ok = await auth.fetchMe()
    if (!ok) return { name: 'login' }
  }
  if (to.meta.admin || to.matched.some((r) => r.meta.admin)) {
    if (auth.user?.role !== 'admin') return { name: 'dashboard' }
  }
  return true
})

export default router
