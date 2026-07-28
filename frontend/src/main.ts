import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './App.vue'
import router from './router'
import { responsiveTable } from './directives/responsiveTable'
import { reportClientError } from './api/errorReporting'
import { useToast } from './stores/toast'
import './style.css'
import './styles/control-theme.css'
import './styles/pastel-theme.css'

const app = createApp(App)
const pinia = createPinia()
app.use(pinia)
app.use(router)
app.directive('responsive-table', responsiveTable)

const toast = useToast(pinia)
app.config.errorHandler = (error, _instance, info) => {
  console.error('[ui]', info, error)
  reportClientError(error, 'vue', info)
  toast.showError(error, '页面运行异常，请刷新后重试')
}
window.addEventListener('unhandledrejection', (event) => {
  console.error('[promise]', event.reason)
  reportClientError(event.reason, 'promise')
  toast.showError(event.reason, '操作没有正常完成，请稍后重试')
})
window.addEventListener('error', (event) => {
  if (event.error) reportClientError(event.error, 'window', `${event.filename || '页面'}:${event.lineno || 0}`)
})
window.addEventListener('offline', () => {
  toast.show('网络连接已断开', 'error', { title: '当前处于离线状态', action: '恢复网络后可以继续操作。' })
})
window.addEventListener('online', () => {
  toast.show('网络连接已恢复', 'success')
})

app.mount('#app')
