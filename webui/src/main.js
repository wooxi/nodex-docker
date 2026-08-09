import { createApp } from 'vue'
import { createRouter, createWebHashHistory } from 'vue-router'
import ElementPlus from 'element-plus'
import zhCn from 'element-plus/es/locale/lang/zh-cn'
import 'element-plus/dist/index.css'
import App from './App.vue'
import Login from './views/Login.vue'
import Dashboard from './views/Dashboard.vue'
import Nodes from './views/Nodes.vue'
import NodeEdit from './views/NodeEdit.vue'
import PanelConfig from './views/PanelConfig.vue'
import SystemConfig from './views/SystemConfig.vue'
import Logs from './views/Logs.vue'

const router = createRouter({
  history: createWebHashHistory(),
  routes: [
    { path: '/login', component: Login },
    { path: '/', component: Dashboard },
    { path: '/nodes', component: Nodes },
    { path: '/nodes/:id', component: NodeEdit },
    { path: '/panel', component: PanelConfig },
    { path: '/system', component: SystemConfig },
    { path: '/logs', component: Logs }
  ]
})

router.beforeEach((to) => {
  if (to.path !== '/login' && !localStorage.getItem('nodex_token')) {
    return '/login'
  }
})

const app = createApp(App)
app.use(router)
app.use(ElementPlus, { locale: zhCn })
app.mount('#app')
