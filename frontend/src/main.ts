import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './App.vue'
import router from './router'
import { responsiveTable } from './directives/responsiveTable'
import { isChunkLoadError, reportClientError } from './api/errorReporting'
import { isAppError } from './api/errors'
import { useToast } from './stores/toast'
import './style.css'
import './styles/control-theme.css'
import './styles/dashboard-editorial.css'
import './styles/youth-workspace.css'
import './styles/public-youth.css'
import './styles/auth-redesign.css'

const app = createApp(App)
const pinia = createPinia()
app.use(pinia)
app.use(router)
app.directive('responsive-table', responsiveTable)

const toast = useToast(pinia)

const chunkReloadKey = 'dd.chunk-reload-at'

function isRoutineSessionError(error: unknown) {
	return isAppError(error) && ['auth.required', 'auth.session_expired', 'auth.session_changed'].includes(error.code)
}

function recoverChunkLoad(error: unknown, source: 'vue' | 'promise') {
	if (!isChunkLoadError(error)) return false
	reportClientError(error, source)
	const now = Date.now()
	let previous = 0
	try {
		previous = Number(sessionStorage.getItem(chunkReloadKey) || 0)
	} catch { /* storage can be unavailable in restricted embedded browsers */ }
	if (!Number.isFinite(previous) || now - previous > 60_000) {
		try { sessionStorage.setItem(chunkReloadKey, String(now)) } catch { /* reload still works */ }
		window.location.reload()
		return true
	}
	toast.show('页面资源已更新，请手动刷新页面', 'error', {
		title: '页面资源加载失败',
		action: '刷新后将加载最新版本。',
	})
	return true
}

function isInjectedHostError(error: unknown) {
	const message = error instanceof Error ? error.message : String(error || '')
	return message.includes('window.weixinPostMessageHandlers')
}

app.config.errorHandler = (error, _instance, info) => {
  if (isRoutineSessionError(error) || isInjectedHostError(error)) return
  if (recoverChunkLoad(error, 'vue')) return
  console.error('[ui]', info, error)
  reportClientError(error, 'vue', info)
  toast.showError(error, '页面运行异常，请刷新后重试')
}
window.addEventListener('unhandledrejection', (event) => {
  if (isRoutineSessionError(event.reason) || isInjectedHostError(event.reason)) {
		event.preventDefault()
		return
	}
  if (recoverChunkLoad(event.reason, 'promise')) {
		event.preventDefault()
		return
	}
  console.error('[promise]', event.reason)
  reportClientError(event.reason, 'promise')
  toast.showError(event.reason, '操作没有正常完成，请稍后重试')
})
window.addEventListener('error', (event) => {
  if (event.error && !isInjectedHostError(event.error)) reportClientError(event.error, 'window', `${event.filename || '页面'}:${event.lineno || 0}`)
})
window.addEventListener('offline', () => {
  toast.show('网络连接已断开', 'error', { title: '当前处于离线状态', action: '恢复网络后可以继续操作。' })
})
window.addEventListener('online', () => {
  toast.show('网络连接已恢复', 'success')
})

app.mount('#app')
