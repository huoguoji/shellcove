<template>
  <div class="layout">
    <aside class="sidebar">
      <div class="brand">
        <div class="brand-logo">WS</div>
        <div class="brand-text">
          <div class="brand-title">ShellCove</div>
          <div class="brand-sub">控制台 v{{ version }}</div>
        </div>
      </div>

      <nav class="nav">
        <!-- 文件传输已移到终端工具条（见 views/TerminalView.vue），侧栏不再单独占位 -->
        <router-link
          v-for="item in navMain"
          :key="item.name"
          class="nav-item"
          :class="{ active: route.name === item.name }"
          :to="{ name: item.name }"
        >
          <svg class="nav-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" v-html="item.icon"></svg>
          <span class="nav-label">{{ item.label }}</span>
          <span v-if="item.name === 'terminal' && state.tabs.length" class="nav-count">{{ state.tabs.length }}</span>
        </router-link>
      </nav>

      <nav v-if="isAdmin" class="nav">
        <div class="nav-group-title">管理员</div>
        <router-link
          v-for="item in navAdmin"
          :key="item.name"
          class="nav-item"
          :class="{ active: route.name === item.name }"
          :to="{ name: item.name }"
        >
          <svg class="nav-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" v-html="item.icon"></svg>
          <span class="nav-label">{{ item.label }}</span>
        </router-link>
      </nav>

      <!-- 底部：系统信息在上，登录账户在下 -->
      <div class="sidebar-footer">
        <section class="side-sec">
          <div class="side-sec-title"><span>系统信息</span><span>ShellCove</span></div>
          <div class="side-kv"><span>版本</span><b>v{{ version }}</b></div>
          <!-- 会话列表在终端里就能看到，侧栏不再重复统计 -->
          <div class="side-kv"><span>终端时延</span><b>{{ state.latency > 0 ? state.latency + ' ms' : '—' }}</b></div>
          <div class="side-kv"><span>本地时间</span><ClockText /></div>
        </section>

        <section class="side-sec">
          <div class="side-sec-title"><span>登录账户</span></div>
          <div class="user-chip">
            <span class="avatar">{{ avatarText }}</span>
            <span class="user-meta">
              <span class="user-name">{{ state.user?.username || '-' }}</span>
              <span class="user-role">{{ isAdmin ? '管理员' : '普通用户' }}</span>
            </span>
            <button class="icon-btn" title="退出登录" @click="logout">
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round">
                <path d="M15 4.5h3.5A1.5 1.5 0 0 1 20 6v12a1.5 1.5 0 0 1-1.5 1.5H15" /><path d="M10 8.5 6.5 12l3.5 3.5" /><path d="M6.5 12H15" />
              </svg>
            </button>
          </div>
        </section>
      </div>
    </aside>

    <main class="main">
      <header class="topbar">
        <div class="crumb">
          <span class="crumb-root">ShellCove</span>
          <span class="crumb-sep">/</span>
          <span class="crumb-current">{{ pageTitle }}</span>
        </div>
        <div class="spacer"></div>
        <span v-if="forcedTotpTip" class="badge badge-warn">管理员要求启用两步验证</span>
        <ThemeSwitch />
      </header>
      <section class="content" :class="{ 'content-flush': isTerminal }">
        <!-- 终端页用 keep-alive 缓存：切走再回来不重建 xterm，也不重连回放历史输出 -->
        <router-view v-slot="{ Component }">
          <keep-alive :include="['TerminalView']">
            <component :is="Component" />
          </keep-alive>
        </router-view>
      </section>

      <!-- 状态栏：页面上下文与全局提示（账号/版本等信息已移至侧栏底部） -->
      <footer class="statusbar">
        <span class="nowrap"><i class="dot" :class="state.tabs.length ? 'on' : ''"></i>{{ pageTitle }}</span>
        <span class="sep"></span>
        <span class="nowrap">{{ state.tabs.length ? '已连接 ' + state.tabs.length + ' 个会话' : '当前没有进行中的会话' }}</span>
        <span style="flex: 1"></span>
        <span v-if="forcedTotpTip" class="nowrap" style="color: var(--warn-text)">两步验证未开启</span>
        <span class="sep"></span>
        <span class="nowrap">会话由后端保持，浏览器断线后可自动重连</span>
      </footer>
    </main>

    <!-- 全局文件传输卡片弹窗 -->
    <FileTransferDialog />
  </div>
</template>

<script setup>
import { computed, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { authApi } from '../api'
import { clearSession, isAdmin, state } from '../store'
import ThemeSwitch from '../components/ThemeSwitch.vue'
import ClockText from '../components/ClockText.vue'
import FileTransferDialog from '../components/FileTransferDialog.vue'

const router = useRouter()
const route = useRoute()
const version = ref('0.3.2')
const forcedTotpTip = computed(() => state.forcedTotp && state.user && !state.user.totp_enabled)
const avatarText = computed(() => {
  const name = (state.user && state.user.username) || '?'
  return name.slice(0, 1).toUpperCase()
})

// 侧栏导航：名称与路由 name 对齐，v-for 渲染避免 router-link 前缀匹配导致「概览」常驻高亮。
const navMain = [
  { name: 'dashboard', label: '概览', icon: '<rect x="3.5" y="3.5" width="7" height="7" rx="1.8"/><rect x="13.5" y="3.5" width="7" height="7" rx="1.8"/><rect x="3.5" y="13.5" width="7" height="7" rx="1.8"/><rect x="13.5" y="13.5" width="7" height="7" rx="1.8"/>' },
  { name: 'resources', label: '资源管理', icon: '<rect x="3" y="4" width="18" height="7" rx="2"/><rect x="3" y="13" width="18" height="7" rx="2"/><path d="M7 7.5h.01M7 16.5h.01"/>' },
  { name: 'terminal', label: '终端', icon: '<rect x="3" y="4" width="18" height="16" rx="2.4"/><path d="m7.5 9.5 2.8 2.5-2.8 2.5"/><path d="M13 15h4"/>' },
  { name: 'commands', label: '命令管理', icon: '<rect x="3" y="4" width="18" height="16" rx="2.4"/><path d="m7.5 9.5 2.5 2.4-2.5 2.4"/><path d="M13 14.6h3.6"/>' },
  { name: 'account', label: '账号与安全', icon: '<path d="M12 3.4 5.2 6.1v5.2c0 4.2 2.8 8 6.8 9.3 4-1.3 6.8-5.1 6.8-9.3V6.1z"/><path d="m9.4 12 1.9 1.9 3.5-3.6"/>' }
]

const navAdmin = [
  { name: 'users', label: '用户管理', icon: '<circle cx="9.6" cy="8.2" r="3.4"/><path d="M3.6 19.4v-1.6a3.4 3.4 0 0 1 3.4-3.4h5.2a3.4 3.4 0 0 1 3.4 3.4v1.6"/><path d="M16.4 5.2a3.4 3.4 0 0 1 0 6.6M17.4 14.6a3.4 3.4 0 0 1 3 3.2v1.6"/>' },
  { name: 'permissions', label: '资源授权', icon: '<rect x="4" y="10.4" width="16" height="10.2" rx="2.2"/><path d="M8 10.4V7.6a4 4 0 0 1 8 0v2.8"/><path d="M12 14.6v2"/>' },
  { name: 'audit', label: '审计日志', icon: '<path d="M6.2 3.6h8l3.6 3.6v13.2H6.2z"/><path d="M14.2 3.6v3.8h3.6"/><path d="M9.4 12.4h5.2M9.4 16h3.6"/>' },
  { name: 'trash', label: '回收站', icon: '<path d="M4.2 7h15.6"/><path d="M9.6 7V4.8h4.8V7"/><path d="M6.2 7l.9 12.2h9.8L17.8 7"/>' },
  { name: 'backup', label: '备份迁移', icon: '<path d="M20 12a8 8 0 1 1-2.4-5.7"/><path d="M20 4.6V9.2h-4.6"/>' },
  { name: 'settings', label: '系统设置', icon: '<circle cx="12" cy="12" r="3.2"/><path d="M12 2.8v2.4M12 18.8v2.4M2.8 12h2.4M18.8 12h2.4M5.5 5.5l1.7 1.7M16.8 16.8l1.7 1.7M5.5 18.5l1.7-1.7M16.8 7.2l1.7-1.7"/>' }
]

const titles = {
  dashboard: '概览',
  resources: '资源管理',
  terminal: '终端',
  commands: '命令管理',
  account: '账号与安全',
  users: '用户管理',
  permissions: '资源授权',
  trash: '回收站',
  audit: '审计日志',
  backup: '备份迁移',
  settings: '系统设置'
}
const pageTitle = computed(() => titles[route.name] || 'ShellCove')
// 终端页需要整屏工作区，去掉内容区留白与滚动
const isTerminal = computed(() => route.name === 'terminal')

async function logout() {
  try {
    await authApi.logout()
  } catch (err) {
    // 忽略登出异常，前端始终清理本地状态
  }
  clearSession()
  await router.replace({ name: 'login' })
}
</script>
