import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './App.vue'
import router from './router'
import { responsiveTable } from './directives/responsiveTable'
import './style.css'

const app = createApp(App)
app.use(createPinia())
app.use(router)
app.directive('responsive-table', responsiveTable)
app.mount('#app')
