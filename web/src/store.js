import { computed, reactive } from 'vue'
import { authApi } from './api'

// 全局状态：当前用户、权限集合、提示消息与终端标签页。
export const state = reactive({
  user: null,
  permissions: {},
  allResources: false,
  forcedTotp: false,
  mustChangePassword: false,
  loaded: false,
  tabs: [],
  toasts: [],
  latency: 0, // 最近一次终端心跳往返时延（毫秒），由状态栏展示
  fileDialog: { open: false, hostId: '' } // 文件传输卡片弹窗（不再占用独立路由页面）
})

export const isAdmin = computed(() => !!state.user && state.user.role === 'admin')

export function canAccess(sshId) {
  if (state.allResources || isAdmin.value) return true
  return !!state.permissions[sshId]
}

export function canSftp(sshId) {
  if (state.allResources || isAdmin.value) return true
  const perm = state.permissions[sshId]
  return !!perm && !!perm.can_sftp
}

export function canMonitor(sshId) {
  if (state.allResources || isAdmin.value) return true
  const perm = state.permissions[sshId]
  return !!perm && !!perm.can_monitor
}

export async function loadSession() {
  const data = await authApi.me()
  state.user = data.user
  state.permissions = data.permissions || {}
  state.allResources = !!data.all_resources
  state.forcedTotp = !!data.forced_totp
  state.mustChangePassword = !!data.must_change_pwd
  state.loaded = true
  return state.user
}

export function clearSession() {
  state.user = null
  state.permissions = {}
  state.allResources = false
  state.mustChangePassword = false
  state.loaded = true
}

export function toast(message, type = 'info', timeout = 3600) {
  const id = Date.now() + Math.random()
  state.toasts.push({ id, message, type })
  window.setTimeout(() => {
    const index = state.toasts.findIndex((item) => item.id === id)
    if (index >= 0) state.toasts.splice(index, 1)
  }, timeout)
}

export function toastError(err) {
  toast(err && err.message ? err.message : '操作失败', 'error')
}

// ---------------------------------------------------------------- 文件传输弹窗

export function openFileTransfer(hostId = '') {
  state.fileDialog.hostId = hostId || ''
  state.fileDialog.open = true
}

export function closeFileTransfer() {
  state.fileDialog.open = false
}

// ---------------------------------------------------------------- 终端标签页

export function openTab(tab) {
  const exists = state.tabs.find((item) => item.id === tab.id)
  if (!exists) state.tabs.push(tab)
  return tab.id
}

export function closeTab(id) {
  const index = state.tabs.findIndex((item) => item.id === id)
  if (index >= 0) {
    const [removed] = state.tabs.splice(index, 1)
    if (removed && typeof removed.onClose === 'function') removed.onClose()
  }
}

export function findTab(id) {
  return state.tabs.find((item) => item.id === id)
}
