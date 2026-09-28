<template>
  <section class="sys-pane">
    <div class="panel-bar">
      <span class="title">系统信息</span>
      <span class="spacer"></span>
      <span v-if="snap && snap.hostname" class="path mono" :title="snap.hostname">{{ snap.hostname }}</span>
      <button
        class="icon-btn"
        :class="{ spinning: loading }"
        title="立即刷新"
        :disabled="loading"
        @click="load()"
      >
        <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round">
          <path d="M13.4 8a5.4 5.4 0 1 1-1.6-3.8" />
          <path d="M13.5 2.4v3.2h-3.2" />
        </svg>
      </button>
    </div>

    <div v-if="!sshId" class="sys-empty">
      <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round">
        <path d="M2.6 13.4V8.6" /><path d="M6.5 13.4V2.6" /><path d="M10.4 13.4v-6.2" /><path d="M14 13.4v-9" />
      </svg>
      <span>建立 SSH 会话后显示主机运行状态</span>
    </div>
    <div v-else-if="note" class="sys-empty">
      <span>{{ note }}</span>
      <!-- 监控开关在主机的编辑里，仅管理员可改，这里提供一键开启 -->
      <button v-if="canEnable" class="btn btn-sm btn-primary" :disabled="enabling" @click="enableMonitor">
        {{ enabling ? '开启中…' : '开启监控采集' }}
      </button>
    </div>
    <div v-else-if="!snap" class="sys-empty">
      <span>正在读取主机状态…</span>
    </div>

    <div v-else class="sys-body">
      <!-- CPU：实时占用 + 走势，下方补负载与运行时长 -->
      <div class="sys-sec">
        <div class="sys-head">
          <span class="sys-name">CPU</span>
          <span class="sys-val">{{ cpuPercent }}%<small>{{ snap.cpu.cores }} 核</small></span>
        </div>
        <svg class="spark cpu" viewBox="0 0 100 34" preserveAspectRatio="none">
          <path class="area" :d="cpuArea" />
          <polyline class="line" :points="cpuPoints" />
        </svg>
        <div class="sys-kv">
          <b>负载</b><span class="mono">{{ loadText }}</span>
          <b>运行</b><span>{{ uptimeText }}</span>
        </div>
      </div>

      <!-- 内存：占用条 + 交换 -->
      <div class="sys-sec">
        <div class="sys-head">
          <span class="sys-name">内存</span>
          <span class="sys-val">{{ memoryPercent }}%<small>{{ kbText(snap.memory.used_kb) }} / {{ kbText(snap.memory.total_kb) }}</small></span>
        </div>
        <div class="meter">
          <i :style="{ width: barWidth(snap.memory.used_percent), background: meterColor(snap.memory.used_percent) }"></i>
        </div>
        <div class="sys-kv">
          <b>交换</b><span class="mono">{{ swapText }}</span>
        </div>
      </div>

      <!-- 网络：上下行速率 + 走势 -->
      <div class="sys-sec">
        <div class="sys-head">
          <span class="sys-name">网络</span>
        </div>
        <div class="sys-net">
          <span class="rx"><i>↓</i>{{ rateText(netRate.rx) }}</span>
          <span class="tx"><i>↑</i>{{ rateText(netRate.tx) }}</span>
        </div>
        <svg class="spark net" viewBox="0 0 100 34" preserveAspectRatio="none">
          <path class="area" :d="netArea" />
          <polyline class="line" :points="netPoints" />
        </svg>
      </div>

      <!-- 磁盘：每个挂载点一行用量 -->
      <div class="sys-sec">
        <div class="sys-head">
          <span class="sys-name">磁盘</span>
          <span class="sys-val"><small>{{ snap.disks.length }} 个挂载点</small></span>
        </div>
        <div v-for="disk in snap.disks" :key="disk.mounted_on">
          <div class="sys-disk-head">
            <span class="mnt mono" :title="disk.mounted_on">{{ disk.mounted_on }}</span>
            <span class="pct mono">{{ disk.use_percent.toFixed(0) }}%</span>
          </div>
          <div class="meter">
            <i :style="{ width: barWidth(disk.use_percent), background: meterColor(disk.use_percent) }"></i>
          </div>
          <div class="sys-sub mono">{{ kbText(disk.used_kb) }} / {{ kbText(disk.size_kb) }}</div>
        </div>
        <div v-if="!snap.disks.length" class="sys-sub">未读取到挂载点</div>
      </div>

      <!-- 系统：发行版 / 内核 / 采集时间 -->
      <div class="sys-sec">
        <div class="sys-head">
          <span class="sys-name">系统</span>
        </div>
        <div class="sys-kv">
          <b>发行版</b><span :title="snap.os || ''">{{ snap.os || '-' }}</span>
          <b>内核</b><span class="mono" :title="snap.kernel || ''">{{ snap.kernel || '-' }}</span>
          <b>采集</b><span class="mono">{{ collectedText }}</span>
        </div>
        <p v-if="warnings" class="sys-warn">{{ warnings }}</p>
      </div>
    </div>
  </section>
</template>

<script setup>
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { sshApi } from '../api'
import { isAdmin, toast, toastError } from '../store'

const props = defineProps({
  sshId: { type: [String, Number], default: '' },
  enabled: { type: Boolean, default: true }
})

const emit = defineEmits(['enabled'])

const POLL_MS = 5000
const HISTORY = 48

const snap = ref(null)
const note = ref('')
const loading = ref(false)
const enabling = ref(false)
const canEnable = ref(false)
const cpuHistory = ref([])
const netHistory = ref([])
const netRate = ref({ rx: 0, tx: 0 })

let timer = null
let previous = null

const cpuPercent = computed(() => (snap.value ? snap.value.cpu.usage_percent.toFixed(1) : '0.0'))
const memoryPercent = computed(() => (snap.value ? snap.value.memory.used_percent.toFixed(1) : '0.0'))
const loadText = computed(() => {
  const cpu = snap.value && snap.value.cpu
  return cpu ? `${cpu.load1} / ${cpu.load5} / ${cpu.load15}` : '-'
})
const uptimeText = computed(() => {
  const seconds = snap.value && snap.value.cpu ? snap.value.cpu.uptime_seconds : 0
  if (!seconds) return '-'
  const day = Math.floor(seconds / 86400)
  const hour = Math.floor((seconds % 86400) / 3600)
  const minute = Math.floor((seconds % 3600) / 60)
  return day > 0 ? `${day} 天 ${hour} 小时` : hour > 0 ? `${hour} 小时 ${minute} 分` : `${minute} 分钟`
})
const swapText = computed(() => {
  const memory = snap.value && snap.value.memory
  if (!memory || !memory.swap_total_kb) return '未启用'
  return `${kbText(memory.swap_used_kb)} / ${kbText(memory.swap_total_kb)}`
})
const cpuPoints = computed(() => toPoints(cpuHistory.value, 100))
const netPoints = computed(() => {
  const max = Math.max(1024, ...netHistory.value)
  return toPoints(netHistory.value, max)
})
const cpuArea = computed(() => areaPath(cpuPoints.value))
const netArea = computed(() => areaPath(netPoints.value))

// 采集时间只保留本地时刻，避免侧栏里塞下一整串 RFC3339。
const collectedText = computed(() => {
  const raw = snap.value && snap.value.collected_at
  if (!raw) return '-'
  const date = new Date(raw)
  return Number.isNaN(date.getTime()) ? raw : date.toLocaleTimeString('zh-CN', { hour12: false })
})
const warnings = computed(() => ((snap.value && snap.value.warnings) || []).join('；'))

watch(() => [props.sshId, props.enabled], restart, { immediate: true })

onMounted(() => document.addEventListener('visibilitychange', onVisibility))

onBeforeUnmount(() => {
  stopTimer()
  document.removeEventListener('visibilitychange', onVisibility)
})

function restart() {
  stopTimer()
  snap.value = null
  previous = null
  cpuHistory.value = []
  netHistory.value = []
  netRate.value = { rx: 0, tx: 0 }
  canEnable.value = false

  if (!props.sshId) {
    note.value = ''
    return
  }
  if (!props.enabled) {
    canEnable.value = isAdmin.value
    note.value = isAdmin.value
      ? '该主机未开启监控采集，开启后即可查看 CPU / 内存 / 交换 / 磁盘'
      : '该主机未开启监控采集，请联系管理员开启'
    return
  }
  note.value = ''
  load()
  startTimer()
}

// 开启监控采集：编辑主机接口仅管理员可用，成功后由父组件更新会话的监控状态。
async function enableMonitor() {
  enabling.value = true
  try {
    const host = await sshApi.get(props.sshId)
    await sshApi.update(props.sshId, {
      name: host.name,
      remark: host.remark,
      host: host.host,
      port: host.port,
      username: host.username,
      auth_type: host.auth_type,
      folder_id: host.folder_id || null,
      monitor_enabled: true,
      proxy_type: host.proxy_type || '',
      proxy_host: host.proxy_host || '',
      proxy_port: host.proxy_port || 0,
      jump_ssh_id: host.jump_ssh_id || null
    })
    emit('enabled')
    toast('已开启监控采集', 'success')
  } catch (err) {
    toastError(err)
  } finally {
    enabling.value = false
  }
}

function startTimer() {
  if (timer !== null || !props.sshId || !props.enabled || document.hidden) return
  timer = window.setInterval(() => load({ silent: true }), POLL_MS)
}

function stopTimer() {
  if (timer !== null) window.clearInterval(timer)
  timer = null
}

// 回到前台立即补一次数据，不必等下一个轮询周期。
function onVisibility() {
  if (document.hidden) {
    stopTimer()
    return
  }
  if (!props.sshId || !props.enabled || note.value) return
  load({ silent: true })
  startTimer()
}

async function load(options = {}) {
  if (!props.sshId) return
  // 后台轮询不点亮刷新按钮：否则每 5 秒闪一次加载动画，还连带整块重渲染。
  const silent = options.silent === true
  if (!silent) loading.value = true
  try {
    const data = await sshApi.monitor(props.sshId)
    snap.value = data
    note.value = ''
    pushHistory(data)
  } catch (err) {
    note.value = err && err.message ? err.message : '读取监控失败'
    stopTimer()
  } finally {
    if (!silent) loading.value = false
  }
}

function pushHistory(data) {
  cpuHistory.value = [...cpuHistory.value, data.cpu.usage_percent].slice(-HISTORY)

  const net = aggregateNetwork(data.network)
  if (previous) {
    const seconds = Math.max(1, (Date.parse(data.collected_at) - Date.parse(previous.at)) / 1000)
    const rx = Math.max(0, (net.rx - previous.rx) / seconds)
    const tx = Math.max(0, (net.tx - previous.tx) / seconds)
    netRate.value = { rx, tx }
    netHistory.value = [...netHistory.value, rx + tx].slice(-HISTORY)
  }
  previous = { at: data.collected_at, rx: net.rx, tx: net.tx }
}

function aggregateNetwork(list) {
  const items = Array.isArray(list) ? list : []
  return items.reduce((acc, item) => {
    if (item.name === 'lo') return acc
    acc.rx += item.rx_bytes || 0
    acc.tx += item.tx_bytes || 0
    return acc
  }, { rx: 0, tx: 0 })
}

function toPoints(values, max) {
  if (!values.length) return ''
  const list = values.length === 1 ? [values[0], values[0]] : values
  const scale = max > 0 ? max : 1
  return list
    .map((value, index) => {
      const x = (index / (list.length - 1)) * 100
      const y = 32 - Math.min(1, value / scale) * 30
      return `${x.toFixed(2)},${y.toFixed(2)}`
    })
    .join(' ')
}

// 折线点串转成闭合路径，给曲线补一层淡色面积。
function areaPath(points) {
  if (!points) return ''
  return `M 0 34 L ${points.split(' ').join(' L ')} L 100 34 Z`
}

function barWidth(percent) {
  return Math.max(0, Math.min(100, percent || 0)) + '%'
}

function meterColor(percent) {
  if (percent >= 90) return 'var(--danger)'
  if (percent >= 75) return 'var(--warn)'
  return 'var(--primary)'
}

function kbText(value) {
  const kb = Number(value) || 0
  if (kb >= 1024 * 1024) return (kb / 1024 / 1024).toFixed(1) + ' GB'
  if (kb >= 1024) return (kb / 1024).toFixed(1) + ' MB'
  return kb + ' KB'
}

function rateText(bytes) {
  const value = Number(bytes) || 0
  if (value >= 1024 * 1024) return (value / 1024 / 1024).toFixed(1) + ' MB/s'
  if (value >= 1024) return (value / 1024).toFixed(1) + ' KB/s'
  return Math.round(value) + ' B/s'
}
</script>
