/** 前端应用启动入口。 */
import App from './App.vue'
import { createApp } from 'vue'
import { initStore } from './store'
import { initRouter } from './router'
import language from './locales'
import '@styles/core/tailwind.css'
import '@styles/index.scss'
import { setupGlobDirectives } from './directives'
import { setupErrorHandle } from './utils/sys/error-handle'

document.addEventListener(
  'touchstart',
  function () {},
  { passive: false }
)

const app = createApp(App)
initStore(app)
initRouter(app)
setupGlobDirectives(app)
setupErrorHandle(app)

app.use(language)
app.mount('#app')
