<template>
  <div class="dash">
    <div class="grid grid-4">
      <div class="stat">
        <span class="stat-icon primary">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round">
            <rect x="3" y="4" width="18" height="7" rx="2" /><rect x="3" y="13" width="18" height="7" rx="2" />
            <path d="M7 7.5h.01M7 16.5h.01" />
          </svg>
        </span>
        <div>
          <div class="stat-label">可访问主机</div>
          <div class="stat-value">{{ hosts.length }}</div>
        </div>
      </div>
      <div class="stat">
        <span class="stat-icon success">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round">
            <path d="M4 12h4l2-5 3 10 2-5h5" />
          </svg>
        </span>
        <div>
          <div class="stat-label">活动会话</div>
          <div class="stat-value">{{ sessions.length }}</div>
        </div>
      </div>
      <div class="stat">
        <span class="stat-icon warn">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round">
            <rect x="3" y="4" width="18" height="16" rx="2.4" /><path d="m7.5 9.5 2.5 2.4-2.5 2.4" /><path d="M13 14.6h3.6" />
          </svg>
        </span>
        <div>
          <div class="stat-label">命令片段</div>
          <div class="stat-value">{{ commandCount }}</div>
        </div>
      </div>
      <div class="stat">
        <span class="stat-icon danger">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round">
            <path d="M4.2 7h15.6" /><path d="M9.6 7V4.8h4.8V7" /><path d="M6.2 7l.9 12.2h9.8L17.8 7" />
          </svg>
        </span>
        <div>
          <div class="stat-label">回收站主机</div>
          <div class="stat-value">{{ trashCount }}</div>
        </div>
      </div>
    </div>

    <!-- 主机列表：按最近连接时间倒序，方便一键回到常用机器 -->
    <div class="card">
      <div class="card-title">
        <h3>主机列表</h3>
        <div class="row">
          <input v-model="search" placeholder="搜索名称 / 地址 / 备注" style="width: 220px" spellcheck="false" />
          <button class="btn btn-sm" @click="clearRecentRecords">清除记录</button>
          <button class="btn btn-sm" @click="reload">刷新</button>
        </div>
      </div>

      <div v-if="!recentHosts.length" class="empty">还没有可连接的主机，先去「资源管理」添加</div>
      <table v-else>
        <thead>
          <tr>
            <th>主机</th>
            <th>地址</th>
            <th>所属文件夹</th>
            <th>最近连接</th>
            <th style="width: 180px"></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="item in recentHosts" :key="item.id">
            <td>
              <div class="cell-main">{{ item.name }}</div>
              <div v-if="item.remark" class="muted" style="font-size: 11px">{{ item.remark }}</div>
            </td>
            <td class="mono">{{ item.username }}@{{ item.host }}:{{ item.port }}</td>
            <td class="muted">{{ item.folder_name || '未分类' }}</td>
            <td>
              <span v-if="recentAt(item.id)" class="badge badge-ok">{{ relativeTime(recentAt(item.id)) }}</span>
              <span v-else class="muted">从未连接</span>
            </td>
            <td class="row wrap">
              <button class="btn btn-sm btn-primary" :disabled="busy === item.id" @click="connect(item)">
                {{ busy === item.id ? '连接中…' : '连接' }}
              </button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <div class="card">
      <div class="card-title">
        <h3>活动会话</h3>
        <button class="btn btn-sm" @click="reload">刷新</button>
      </div>
      <div v-if="!sessions.length" class="empty">当前没有进行中的会话</div>
      <table v-else>
        <thead>
          <tr>
            <th>主机</th>
            <th>用户</th>
            <th>会话 ID</th>
            <th>开始时间</th>
            <th>最后活动</th>
            <th></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="item in sessions" :key="item.id">
            <td class="cell-main">{{ item.ssh_name || item.ssh_id }}</td>
            <td>{{ item.username || item.user_id }}</td>
            <td class="mono">{{ item.id }}</td>
            <td class="muted">{{ formatTime(item.started_at) }}</td>
            <td class="muted">{{ formatTime(item.last_active) }}</td>
            <td class="row">
              <button class="btn btn-sm" @click="openTerminal(item)">进入</button>
              <button class="btn btn-sm btn-danger" @click="closeSession(item)">断开</button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <div class="card">
      <div class="card-title">
        <h3>快速开始</h3>
      </div>
      <div class="row wrap">
        <router-link class="btn btn-primary" to="/resources">新建 / 管理主机</router-link>
        <router-link class="btn" to="/terminal">打开终端</router-link>
        <router-link class="btn" to="/account">绑定两步验证</router-link>
      </div>
      <p class="muted" style="margin: 10px 0 0">
        会话由后端保持连接，浏览器断线后重新进入终端即可恢复。
      </p>
    </div>
  </div>
</template>

<script setup>
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { commandApi, sessionApi, sshApi, trashApi } from '../api'
import { isAdmin, openTab, toast, toastError } from '../store'
import { clearRecent, markRecent, recentAt, relativeTime } from '../recent'

const router = useRouter()
const hosts = ref([])
const sessions = ref([])
const commandCount = ref(0)
const trashCount = ref(0)
const search = ref('')
const busy = ref('')
const recentTick = ref(0)

let timer = null

// 最近连接时间来自浏览器本地记录，recentTick 用于清除记录后强制重算。
const recentHosts = computed(() => {
  void recentTick.value
  const keyword = search.value.trim().toLowerCase()
  const filtered = keyword
    ? hosts.value.filter((host) =>
        [host.name, host.host, host.username, host.remark].some((value) => (value || '').toLowerCase().includes(keyword))
      )
    : hosts.value.slice()
  return filtered.sort((a, b) => recentAt(b.id) - recentAt(a.id))
})

onMounted(async () => {
  await reload()
  timer = window.setInterval(loadSessions, 8000)
  document.addEventListener('visibilitychange', onVisibility)
})

onBeforeUnmount(() => {
  if (timer) window.clearInterval(timer)
  document.removeEventListener('visibilitychange', onVisibility)
})

// 回到前台补一次，后台时不打接口。
function onVisibility() {
  if (!document.hidden) loadSessions()
}

async function reload() {
  try {
    const [ssh, sessionsData, commands] = await Promise.all([
      sshApi.list(),
      sessionApi.list(),
      commandApi.list()
    ])
    hosts.value = ssh.items || []
    sessions.value = sessionsData.items || []
    commandCount.value = commands.total || 0
    if (isAdmin.value) {
      const trash = await trashApi.list()
      trashCount.value = trash.total || 0
    }
  } catch (err) {
    toastError(err)
  }
}

async function loadSessions() {
  if (document.hidden) return
  try {
    const data = await sessionApi.list()
    const items = data.items || []
    // 内容没变就不替换数组，省掉每 8 秒一次的整表重渲染。
    if (JSON.stringify(items) === JSON.stringify(sessions.value)) return
    sessions.value = items
  } catch (err) {
    // 轮询失败静默处理，避免打断用户
  }
}

async function connect(item) {
  busy.value = item.id
  try {
    const data = await sshApi.connect(item.id, { cols: 120, rows: 32, title: item.name })
    markRecent(item.id)
    openTab({ id: data.session_id, sshId: item.id, title: item.name, status: 'attached', monitored: data.monitored })
    router.push('/terminal')
  } catch (err) {
    toastError(err)
  } finally {
    busy.value = ''
  }
}

function clearRecentRecords() {
  clearRecent()
  recentTick.value += 1
  toast('已清除本地连接记录', 'success')
}

function openTerminal(item) {
  openTab({ id: item.id, sshId: item.ssh_id, title: item.ssh_name || item.ssh_id, status: 'attached' })
  router.push('/terminal')
}

async function closeSession(item) {
  try {
    await sessionApi.close(item.id)
    toast('会话已断开', 'success')
    await loadSessions()
  } catch (err) {
    toastError(err)
  }
}

function formatTime(value) {
  if (!value) return '-'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toLocaleString()
}
</script>
