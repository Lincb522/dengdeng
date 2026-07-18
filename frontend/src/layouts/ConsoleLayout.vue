<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { useAuth } from '../stores/auth'
import BrandMark from '../components/BrandMark.vue'
import ThemeToggle from '../components/ThemeToggle.vue'
import { formatMoney } from '../api/types'

const auth = useAuth()
const route = useRoute()
const railOpen = ref(false)

const userNav = [
  { to: '/dashboard', label: '总览', icon: 'M3 13h8V3H3v10zm0 8h8v-6H3v6zm10 0h8V11h-8v10zm0-18v6h8V3h-8z' },
  { to: '/studio', label: '图像创作', icon: 'M21 19V5a2 2 0 0 0-2-2H5a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2zM8.5 13.5l2.5 3 3.5-4.5L19 18H5l3.5-4.5zM8 10a1.5 1.5 0 1 1 0-3 1.5 1.5 0 0 1 0 3z' },
  { to: '/models', label: '模型广场', icon: 'M12 2 3.5 6.7v10.6L12 22l8.5-4.7V6.7L12 2zm0 2.3 5.9 3.2L12 10.8 6.1 7.5 12 4.3zm-6.5 5 5.5 3v7.9l-5.5-3V9.3zm7.5 10.9v-7.9l5.5-3v7.9l-5.5 3z' },
  { to: '/keys', label: 'API 密钥', icon: 'M12.65 10a6 6 0 1 0 0 4H17v4h4v-4h2v-4H12.65zM7 14a2 2 0 1 1 0-4 2 2 0 0 1 0 4z' },
  { to: '/usage', label: '用量明细', icon: 'M3 3v18h18v-2H5V3H3zm4 12h2v4H7v-4zm4-6h2v10h-2V9zm4 3h2v7h-2v-7zm4-6h2v13h-2V6z' },
  { to: '/wallet', label: '钱包', icon: 'M21 7H5a1 1 0 0 1 0-2h14V3H5a3 3 0 0 0-3 3v12a3 3 0 0 0 3 3h16a1 1 0 0 0 1-1V8a1 1 0 0 0-1-1zm-4 7a1.5 1.5 0 1 1 0-3 1.5 1.5 0 0 1 0 3z' },
  { to: '/referrals', label: '推广中心', icon: 'M16 11a3 3 0 1 0 0-6 3 3 0 0 0 0 6ZM8 11a3 3 0 1 0 0-6 3 3 0 0 0 0 6ZM16 13c-3.33 0-6 1.79-6 4v3h12v-3c0-2.21-2.67-4-6-4ZM8 13c-3.33 0-6 1.79-6 4v3h6v-3c0-1.2.4-2.32 1.1-3.29A8.65 8.65 0 0 0 8 13Z' },
  { to: '/profile', label: '账户设置', icon: 'M12 12a5 5 0 1 0-5-5 5 5 0 0 0 5 5zm0 2c-3.33 0-10 1.67-10 5v3h20v-3c0-3.33-6.67-5-10-5z' },
]

const adminNav = [
  { to: '/admin/overview', label: '运营总览', icon: 'M3 13h8V3H3v10zm0 8h8v-6H3v6zm10 0h8V11h-8v10zm0-18v6h8V3h-8z' },
  { to: '/admin/monitoring', label: '运行监控', icon: 'M3 13h2v-2H3v2zm4 0h2V7H7v6zm4 0h2V3h-2v10zm4 0h2V9h-2v4zm2 6H5v-2H3v4h18v-4h-2v2z' },
  { to: '/admin/alerts', label: '告警与巡检', icon: 'M12 2a8 8 0 0 0-8 8v4l-2 3v2h20v-2l-2-3v-4a8 8 0 0 0-8-8zm0 20a3 3 0 0 0 2.83-2H9.17A3 3 0 0 0 12 22z' },
  { to: '/admin/backups', label: '数据库备份', icon: 'M19 3H5a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2V5a2 2 0 0 0-2-2zm-7 16a3 3 0 1 1 0-6 3 3 0 0 1 0 6zM17 9H7V5h10v4z' },
  { to: '/admin/groups', label: '分组管理', icon: 'M4 8h4V4H4v4zm6 12h4v-4h-4v4zm-6 0h4v-4H4v4zm0-6h4v-4H4v4zm6 0h4v-4h-4v4zm6-10v4h4V4h-4zm-6 4h4V4h-4v4zm6 6h4v-4h-4v4zm0 6h4v-4h-4v4z' },
  { to: '/admin/accounts', label: '上游账号', icon: 'M20 6h-4V4a2 2 0 0 0-2-2h-4a2 2 0 0 0-2 2v2H4a2 2 0 0 0-2 2v11a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2zm-10-2h4v2h-4V4z' },
  { to: '/admin/users', label: '用户管理', icon: 'M16 11a4 4 0 1 0-4-4 4 4 0 0 0 4 4zm-8 0a3 3 0 1 0-3-3 3 3 0 0 0 3 3zm0 2c-2.33 0-7 1.17-7 3.5V19h7v-2.5c0-.85.33-2.34 2.37-3.47A12.3 12.3 0 0 0 8 13zm8 0c-2.67 0-8 1.33-8 4v3h16v-3c0-2.67-5.33-4-8-4z' },
  { to: '/admin/models', label: '模型配置', icon: 'M4 5a3 3 0 0 1 3-3h10a3 3 0 0 1 3 3v4h-2V5a1 1 0 0 0-1-1H7a1 1 0 0 0-1 1v14a1 1 0 0 0 1 1h5v2H7a3 3 0 0 1-3-3V5zm10 7h7v2h-7v-2zm0 4h7v2h-7v-2zm0-8h7v2h-7V8z' },
  { to: '/admin/prices', label: '模型定价', icon: 'M12 2a10 10 0 1 0 10 10A10 10 0 0 0 12 2zm1 15h-2v-1.1a3.9 3.9 0 0 1-2.5-1.5l1.4-1.4a2.6 2.6 0 0 0 2.1 1c.8 0 1.5-.3 1.5-1s-.9-.9-2-1.2c-1.5-.4-3-1-3-2.8 0-1.4 1-2.5 2.5-2.9V5h2v1.1a3.6 3.6 0 0 1 2.2 1.3L13.8 8.8a2.3 2.3 0 0 0-1.8-.8c-.7 0-1.3.3-1.3.9s.8.8 1.9 1.1c1.5.4 3.1 1 3.1 2.9 0 1.5-1.1 2.6-2.7 3z' },
  { to: '/admin/redeem', label: '兑换码', icon: 'M20 6h-2.18A3 3 0 0 0 18 5a3 3 0 0 0-3-3 3.9 3.9 0 0 0-3 1.5A3.9 3.9 0 0 0 9 2a3 3 0 0 0-3 3 3 3 0 0 0 .18 1H4a2 2 0 0 0-2 2v3h20V8a2 2 0 0 0-2-2zM2 20a2 2 0 0 0 2 2h7V12H2v8zm11 2h7a2 2 0 0 0 2-2v-8h-9v10z' },
  { to: '/admin/referrals', label: '推广分成', icon: 'M12 2a5 5 0 0 0-5 5c0 3.75 5 9 5 9s5-5.25 5-9a5 5 0 0 0-5-5zm0 2a3 3 0 0 1 3 3c0 1.9-1.8 4.7-3 6.3C10.8 11.7 9 8.9 9 7a3 3 0 0 1 3-3zm-8 14h16v2H4v-2z' },
  { to: '/admin/payment', label: '支付中心', icon: 'M3 5a3 3 0 0 1 3-3h12a3 3 0 0 1 3 3v2h-2V5a1 1 0 0 0-1-1H6a1 1 0 0 0-1 1v14a1 1 0 0 0 1 1h5v2H6a3 3 0 0 1-3-3V5zm12 5h7v2h-7v-2zm0 4h7v2h-7v-2zm0 4h5v2h-5v-2z' },
  { to: '/admin/proxies', label: '代理配置', icon: 'M7 3h10a4 4 0 0 1 4 4v10a4 4 0 0 1-4 4H7a4 4 0 0 1-4-4V7a4 4 0 0 1 4-4zm0 2a2 2 0 0 0-2 2v10a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V7a2 2 0 0 0-2-2H7zm2 3h6v2H9V8zm0 4h6v2H9v-2zm0 4h4v2H9v-2z' },
  { to: '/admin/settings', label: '系统设置', icon: 'M19.43 12.98c.04-.32.07-.65.07-.98s-.02-.66-.07-.98l2.11-1.65-2-3.46-2.49 1a7.3 7.3 0 0 0-1.69-.98L15 3.28h-4l-.37 2.65c-.61.24-1.17.57-1.69.98l-2.49-1-2 3.46 2.11 1.65c-.04.32-.07.65-.07.98s.02.66.07.98l-2.11 1.65 2 3.46 2.49-1c.52.41 1.08.74 1.69.98L11 20.72h4l.37-2.65c.61-.24 1.17-.57 1.69-.98l2.49 1 2-3.46-2.11-1.65zM13 16.5a4.5 4.5 0 1 1 0-9 4.5 4.5 0 0 1 0 9z' },
  { to: '/admin/usage', label: '全站用量', icon: 'M9 17H7v-7h2v7zm4 0h-2V7h2v10zm4 0h-2v-4h2v4zm2.5 2.5h-15V5H3v16h18v-1.5h-1.5z' },
  { to: '/admin/updates', label: '版本更新', icon: 'M12 3a9 9 0 0 0-8.6 6.35L1 7v6h6l-2.1-2.1A7 7 0 1 1 5 15H3a9 9 0 1 0 9-12zm1 4h-2v6l5 3 1-1.7-4-2.3V7z' },
]

const adminNavGroups = [
  { label: '运行中心', items: adminNav.slice(0, 4) },
  { label: '接入与模型', items: [adminNav[4], adminNav[5], adminNav[12], adminNav[7], adminNav[8]] },
  { label: '用户与交易', items: [adminNav[6], adminNav[9], adminNav[10], adminNav[11]] },
  { label: '系统维护', items: [adminNav[14], adminNav[13], adminNav[15]] },
]

const isAdmin = computed(() => auth.user?.role === 'admin')
const balance = computed(() => formatMoney(auth.user?.balance_micro ?? 0))
const initials = computed(() => (auth.user?.email || 'D').trim().slice(0, 1).toUpperCase())

watch(
  () => route.fullPath,
  () => {
    railOpen.value = false
  },
)

function closeRail() {
  railOpen.value = false
}

function handleKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape') closeRail()
}

onMounted(() => {
  void auth.loadPublicSettings()
  window.addEventListener('keydown', handleKeydown)
})
onBeforeUnmount(() => window.removeEventListener('keydown', handleKeydown))

function isActive(to: string) {
  return route.path === to || route.path.startsWith(to + '/')
}
</script>

<template>
  <div class="app-shell">
    <div class="ambient-field" aria-hidden="true">
      <span class="ambient-orb ambient-orb--one"></span>
      <span class="ambient-orb ambient-orb--two"></span>
      <span class="ambient-orb ambient-orb--three"></span>
    </div>

    <div class="console-shell">
      <div
        v-if="railOpen"
        class="fixed inset-0 z-30 bg-ink-950/20 lg:hidden"
        aria-hidden="true"
        @click="closeRail"
      ></div>

      <aside id="primary-navigation" class="side-rail" :class="{ 'is-open': railOpen }">
        <div class="rail-brand">
          <BrandMark :size="34" />
          <div class="rail-brand-copy min-w-0">
            <div class="rail-brand-name truncate">{{ auth.siteName }}</div>
            <div class="rail-brand-caption">蹬蹬ai</div>
          </div>
          <span class="rail-brand-state"><i></i>在线</span>
          <button type="button" class="rail-close" aria-label="关闭导航" @click="closeRail">
            <svg viewBox="0 0 24 24" aria-hidden="true"><path d="m6 6 12 12M18 6 6 18" /></svg>
          </button>
        </div>

        <nav class="rail-nav" aria-label="主导航">
          <section class="rail-nav-group">
            <p class="rail-section">工作区</p>
            <RouterLink v-for="item in userNav" :key="item.to" :to="item.to" class="rail-link" :class="{ 'is-active': isActive(item.to) }">
              <span class="rail-link-icon"><svg viewBox="0 0 24 24"><path :d="item.icon" /></svg></span>
              <span>{{ item.label }}</span>
              <i class="rail-link-mark"></i>
            </RouterLink>
          </section>

          <template v-if="isAdmin">
            <section v-for="group in adminNavGroups" :key="group.label" class="rail-nav-group">
              <p class="rail-section">{{ group.label }}</p>
              <RouterLink v-for="item in group.items" :key="item.to" :to="item.to" class="rail-link" :class="{ 'is-active': isActive(item.to) }">
                <span class="rail-link-icon"><svg viewBox="0 0 24 24"><path :d="item.icon" /></svg></span>
                <span>{{ item.label }}</span>
                <i class="rail-link-mark"></i>
              </RouterLink>
            </section>
          </template>
        </nav>

        <div class="rail-account">
          <div class="rail-account-profile">
            <span class="rail-account-avatar" aria-hidden="true">{{ initials }}</span>
            <div><strong :title="auth.user?.email">{{ auth.user?.email }}</strong><small>{{ isAdmin ? '管理员账户' : '个人账户' }}</small></div>
          </div>
          <div class="rail-account-balance">
            <span>可用余额</span><strong class="num">{{ balance }}</strong>
          </div>
          <div class="rail-account-actions">
            <ThemeToggle class="rail-theme-toggle" />
            <button type="button" class="rail-logout" aria-label="退出登录" title="退出登录" @click="auth.logout()">
              <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M10 4H5v16h5v-2H7V6h3V4zm5.6 3.6L14.2 9l2 2H9v2h7.2l-2 2 1.4 1.4L20 12l-4.4-4.4z" /></svg>
            </button>
          </div>
        </div>
      </aside>

      <section class="workspace">
        <header class="workspace-bar">
          <div class="flex items-center gap-3">
            <button
              type="button"
              class="mobile-rail-trigger"
              aria-label="打开导航"
              aria-controls="primary-navigation"
              :aria-expanded="railOpen"
              @click="railOpen = true"
            >
              <svg viewBox="0 0 24 24" class="h-4 w-4 fill-current"><path d="M4 6h16v2H4V6zm0 5h16v2H4v-2zm0 5h16v2H4v-2z" /></svg>
            </button>
            <div class="workspace-context">
              <span class="workspace-dot"></span>
              <span>服务运行正常</span>
            </div>
          </div>
          <div class="workspace-user">
            <span class="hidden max-w-44 truncate sm:inline" :title="auth.user?.email">{{ auth.user?.email }}</span>
            <span class="workspace-avatar" aria-hidden="true">{{ initials }}</span>
          </div>
        </header>

        <main class="workspace-main">
          <RouterView v-slot="{ Component }">
            <Transition name="page-swap" mode="out-in">
              <component :is="Component" />
            </Transition>
          </RouterView>
        </main>
      </section>
    </div>
  </div>
</template>
