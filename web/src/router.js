import { createRouter, createWebHistory } from 'vue-router'
import { clearSession, loadSession, state } from './store'

const routes = [
  { path: '/login', name: 'login', component: () => import('./views/Login.vue'), meta: { public: true } },
  {
    path: '/',
    component: () => import('./views/Layout.vue'),
    children: [
      { path: '', name: 'dashboard', component: () => import('./views/Dashboard.vue') },
      { path: 'resources', name: 'resources', component: () => import('./views/Resources.vue') },
      { path: 'terminal', name: 'terminal', component: () => import('./views/TerminalView.vue') },
      // 文件传输已改为全局卡片弹窗（见 components/FileTransferDialog.vue），旧链接由兜底路由回到概览
      { path: 'commands', name: 'commands', component: () => import('./views/Commands.vue') },
      { path: 'account', name: 'account', component: () => import('./views/Account.vue') },
      { path: 'users', name: 'users', component: () => import('./views/Users.vue'), meta: { admin: true } },
      { path: 'permissions', name: 'permissions', component: () => import('./views/Permissions.vue'), meta: { admin: true } },
      { path: 'audit', name: 'audit', component: () => import('./views/Audit.vue'), meta: { admin: true } },
      { path: 'trash', name: 'trash', component: () => import('./views/Trash.vue'), meta: { admin: true } },
      { path: 'backup', name: 'backup', component: () => import('./views/Backup.vue'), meta: { admin: true } },
      { path: 'settings', name: 'settings', component: () => import('./views/Settings.vue'), meta: { admin: true } }
    ]
  },
  { path: '/:pathMatch(.*)*', redirect: '/' }
]

const router = createRouter({
  history: createWebHistory(),
  routes
})

router.beforeEach(async (to) => {
  if (to.meta.public) return true

  if (!state.loaded) {
    try {
      await loadSession()
    } catch (err) {
      clearSession()
      return { name: 'login', query: { redirect: to.fullPath } }
    }
  }
  if (!state.user) {
    return { name: 'login', query: { redirect: to.fullPath } }
  }
  // 首次登录或密码被重置后必须改密，其他页面一律拦截。
  if (state.mustChangePassword && to.name !== 'account') {
    return { name: 'account', query: { force: '1' } }
  }
  if (to.meta.admin && state.user.role !== 'admin') {
    return { name: 'dashboard' }
  }
  return true
})

export default router
