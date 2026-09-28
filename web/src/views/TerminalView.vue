<template>
  <!-- 工作区：工具条 → 会话标签条 → [终端 | 系统信息(按需)] → 底部面板(按需) -->
  <div class="workspace">
    <div class="ws-toolbar">
      <button class="btn btn-sm btn-primary" @click="openPicker">新建会话</button>
      <span class="sep"></span>
      <button class="icon-btn" title="重新连接当前会话" :disabled="!activeTab || activeTab.status === 'ready'" @click="activeTab && reconnect(activeTab)">
        <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round">
          <path d="M13.4 8a5.4 5.4 0 1 1-1.6-3.8" /><path d="M13.5 2.4v3.2h-3.2" />
        </svg>
      </button>
      <button class="icon-btn" title="清屏" :disabled="!activeTab" @click="clearActive">
        <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round">
          <path d="M2.6 3.4h10.8" /><path d="M5.4 3.4V2.2h5.2v1.2" /><path d="M4 3.4l.7 10.4h6.6l.7-10.4" />
        </svg>
      </button>
      <span class="sep"></span>
      <button class="icon-btn" :class="{ active: showFiles }" title="显示/隐藏底部文件管理面板" @click="toggleFiles">
        <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round">
          <path d="M2.6 3.6h4l1.3 1.7h5.5v7.1H2.6z" />
        </svg>
      </button>
      <button class="icon-btn" title="文件传输" :disabled="!activeTab" @click="openTransfer">
        <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round">
          <path d="M8 10.4V2.8" /><path d="M5 5.8 8 2.8l3 3" /><path d="M2.8 10.4v2.4h10.4v-2.4" />
        </svg>
      </button>
      <button class="icon-btn" :class="{ active: showSide }" title="显示/隐藏系统信息" @click="toggleSide">
        <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round">
          <path d="M2.6 13.4V8.6" /><path d="M6.5 13.4V2.6" /><path d="M10.4 13.4v-6.2" /><path d="M14 13.4v-9" />
        </svg>
      </button>
      <span class="sep"></span>
      <button
        class="icon-btn"
        :class="{ active: showFiles && bottomTab === 'cmd' }"
        title="执行命令"
        :disabled="!activeTab"
        @click="openCommandPanel"
      >
        <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round">
          <path d="m2.8 4.6 2.4 2.4-2.4 2.4" /><path d="M7.4 10.6h5.4" />
        </svg>
      </button>
      <span class="spacer" style="flex: 1"></span>
      <span class="status-text">{{ activeTab ? statusText(activeTab) : '未选择会话' }}</span>
      <span v-if="activeTab && activeTab.monitored" class="badge badge-ok">监控</span>
      <span class="badge mono" :title="'终端往返时延'">{{ latencyText }}</span>
    </div>

    <div class="ws-tabs">
      <div
        v-for="tab in state.tabs"
        :key="tab.id"
        class="ws-tab"
        :class="{ active: tab.id === activeId }"
        :title="tab.title"
        @click="activate(tab.id)"
      >
        <span :style="{ color: statusColor(tab.status) }">●</span>
        <span class="tab-title">{{ tab.title || '会话' }}</span>
        <span class="close" title="关闭会话" @click.stop="closeTerminal(tab)">✕</span>
      </div>
      <span v-if="!state.tabs.length" class="status-text" style="display: flex; align-items: center; padding: 0 6px">暂无会话</span>
      <span style="flex: 1"></span>
      <button class="icon-btn new-tab" title="新建会话" @click="openPicker">＋</button>
    </div>

    <div class="ws-body">
      <div class="ws-main">
        <div class="ws-term" @contextmenu.prevent="openTermCtx($event)">
          <div v-show="!state.tabs.length" class="ws-empty">选择一个主机建立 SSH 会话，支持断线重连与窗口自适应。</div>
          <div
            v-for="tab in state.tabs"
            v-show="tab.id === activeId"
            :key="tab.id + '-host'"
            :ref="(el) => setHost(tab.id, el)"
            class="term-host"
          ></div>

          <!-- 查找条：浮在终端右上角，Enter 下一个 / Shift+Enter 上一个 / Esc 关闭 -->
          <div v-if="finder.open" class="term-find">
            <input
              ref="findInput"
              v-model="finder.keyword"
              placeholder="查找内容"
              spellcheck="false"
              @input="runFind"
              @keydown="onFindKey"
            />
            <span class="term-find-count">{{ findCountText }}</span>
            <button class="icon-btn" title="上一个 (Shift+Enter)" :disabled="!finder.count" @click="findPrev">
              <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">
                <path d="M4 10l4-4 4 4" />
              </svg>
            </button>
            <button class="icon-btn" title="下一个 (Enter)" :disabled="!finder.count" @click="findNext">
              <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">
                <path d="M4 6l4 4 4-4" />
              </svg>
            </button>
            <button class="icon-btn" title="关闭 (Esc)" @click="closeFind">
              <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round">
                <path d="M4.4 4.4l7.2 7.2M11.6 4.4l-7.2 7.2" />
              </svg>
            </button>
          </div>
        </div>

        <template v-if="showFiles">
          <div class="splitter horizontal" title="拖拽调整高度" @pointerdown="startResize('files', $event)"></div>
          <div class="ws-bottom" :style="{ height: fileHeight + 'px' }">
            <!-- 底部面板：文件管理与命令执行两个标签页 -->
            <div class="pane-tabs">
              <button class="pane-tab" :class="{ active: bottomTab === 'files' }" @click="bottomTab = 'files'">
                <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round">
                  <path d="M2.6 3.6h4l1.3 1.7h5.5v7.1H2.6z" />
                </svg>
                文件管理
              </button>
              <button class="pane-tab" :class="{ active: bottomTab === 'cmd' }" @click="bottomTab = 'cmd'">
                <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round">
                  <path d="m2.8 4.6 2.4 2.4-2.4 2.4" /><path d="M7.4 10.6h5.4" />
                </svg>
                执行命令
              </button>
              <span class="spacer"></span>
              <span class="pane-hint">{{ activeTab ? activeTab.title || '当前会话' : '未选择会话' }}</span>
            </div>
            <FilePanel v-show="bottomTab === 'files'" :ssh-id="activeTab ? activeTab.sshId : ''" />
            <CommandPanel
              v-show="bottomTab === 'cmd'"
              :session-id="activeTab ? activeTab.id : ''"
              :session-title="activeTab ? activeTab.title : ''"
            />
          </div>
        </template>
      </div>

      <template v-if="showSide">
        <div class="splitter vertical" title="拖拽调整宽度" @pointerdown="startResize('side', $event)"></div>
        <div class="ws-side" :style="{ width: sideWidth + 'px' }">
          <SystemPanel
            :ssh-id="activeTab ? activeTab.sshId : ''"
            :enabled="!!(activeTab && activeTab.monitored)"
            @enabled="markMonitored"
          />
        </div>
      </template>
    </div>

    <div v-if="picker.open" class="modal-mask" @click.self="picker.open = false">
      <div class="modal">
        <div class="modal-head">
          <h3>新建 SSH 会话</h3>
          <button class="btn btn-sm btn-ghost" @click="picker.open = false">关闭</button>
        </div>
        <div class="modal-body">
          <input v-model="picker.search" placeholder="搜索主机" style="margin-bottom: 12px" />
          <div v-if="picker.loading" class="empty">加载中…</div>
          <div v-else-if="!filteredHosts.length" class="empty">没有可连接的主机</div>
          <table v-else>
            <tbody>
              <tr v-for="host in filteredHosts" :key="host.id">
                <td>
                  <div>{{ host.name }}</div>
                  <div class="muted mono" style="font-size: 11px">{{ host.username }}@{{ host.host }}:{{ host.port }}</div>
                </td>
                <td style="width: 90px; text-align: right">
                  <button class="btn btn-sm btn-primary" :disabled="picker.busy" @click="connectHost(host)">连接</button>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </div>

    <!-- 终端右键菜单：复制 / 粘贴 / 查找 / 清屏 -->
    <template v-if="termCtx.open">
      <div class="ctx-layer" @click="closeTermCtx" @contextmenu.prevent="closeTermCtx"></div>
      <div class="ctx-menu" :style="{ left: termCtx.x + 'px', top: termCtx.y + 'px' }">
        <button :disabled="!termCtx.hasSelection" @click="termCopy">复制</button>
        <button :disabled="!termCtx.ready" @click="termPaste">粘贴</button>
        <button :disabled="!termCtx.ready" @click="termFind">查找</button>
        <button :disabled="!termCtx.ready" @click="termClear">清屏</button>
      </div>
    </template>
  </div>
</template>

<script setup>
import { computed, nextTick, onActivated, onBeforeUnmount, onDeactivated, onMounted, reactive, ref, watch } from 'vue'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { WebLinksAddon } from '@xterm/addon-web-links'
import { SearchAddon } from '@xterm/addon-search'
import '@xterm/xterm/css/xterm.css'
import { sessionApi, sshApi } from '../api'
import { closeTab, openFileTransfer, openTab, state, toast, toastError } from '../store'
import { resolvedTheme, terminalTheme } from '../theme'
import { markRecent } from '../recent'
import FilePanel from '../components/FilePanel.vue'
import SystemPanel from '../components/SystemPanel.vue'
import CommandPanel from '../components/CommandPanel.vue'

// 供 Layout 的 <keep-alive :include> 命中：切到别的页面时保留会话与终端实例。
defineOptions({ name: 'TerminalView' })

const hosts = new Map()   // sessionId -> DOM 容器
const terms = new Map()   // sessionId -> { term, fit, ws, observer, retry, disposed }
const activeId = ref('')
const picker = reactive({ open: false, loading: false, busy: false, search: '', hosts: [] })

// 终端右键菜单（复制 / 粘贴 / 查找 / 清屏）与查找条。
const termCtx = reactive({ open: false, x: 0, y: 0, ready: false, hasSelection: false })
const finder = reactive({ open: false, keyword: '', index: 0, count: 0 })
const findInput = ref(null)

// 工作区布局：文件面板与系统信息默认收起，点工具条按需展开。
const showFiles = ref(false)
const showSide = ref(false)
const bottomTab = ref('files')
const fileHeight = ref(232)
const sideWidth = ref(288)
let resizing = null
let resizeFrame = 0
let pendingSize = null
let heartbeat = null
let sideAutoShown = false
let pageActive = true   // keep-alive 切走期间为 false，此时不做 fit

const latencyText = computed(() => (state.latency > 0 ? state.latency + ' ms' : '— ms'))

// 终端配色跟随面板主题，切换主题时同步刷新已打开的会话。
watch(resolvedTheme, () => {
  const theme = terminalTheme()
  terms.forEach((entry) => {
    if (entry.term) entry.term.options.theme = theme
  })
})

const activeTab = computed(() => state.tabs.find((tab) => tab.id === activeId.value) || null)

const filteredHosts = computed(() => {
  const term = picker.search.trim().toLowerCase()
  if (!term) return picker.hosts
  return picker.hosts.filter((host) =>
    [host.name, host.host, host.username, host.remark].some((v) => (v || '').toLowerCase().includes(term))
  )
})

watch(
  () => state.tabs.map((tab) => tab.id).join(','),
  async () => {
    if (!state.tabs.length) {
      activeId.value = ''
      state.latency = 0
      stopHeartbeat()
      return
    }
    if (!state.tabs.some((tab) => tab.id === activeId.value)) {
      activeId.value = state.tabs[state.tabs.length - 1].id
    }
    await nextTick()
    state.tabs.forEach((tab) => ensureTerminal(tab))
    fitActive()
    autoRevealSide()
  }
)
watch(activeId, async () => {
  await nextTick()
  fitActive()
  startHeartbeat()
})

onMounted(async () => {
  activeId.value = state.tabs.length ? state.tabs[state.tabs.length - 1].id : ''
  await nextTick()
  state.tabs.forEach((tab) => ensureTerminal(tab))
  fitActive()
  startHeartbeat()
  autoRevealSide()
})

// keep-alive：切到其它页面时保留会话与 xterm 实例（不重连、不重放历史输出），
// 切回来时重新测量一次尺寸并恢复心跳。
onActivated(async () => {
  pageActive = true
  await nextTick()
  fitActive()
  startHeartbeat()
})

onDeactivated(() => {
  pageActive = false
  stopHeartbeat()
})

onBeforeUnmount(() => {
  stopHeartbeat()
  terms.forEach((entry, id) => teardown(id, entry))
})

function setHost(id, el) {
  if (el) hosts.set(id, el)
  else hosts.delete(id)
}

function toggleFiles() {
  showFiles.value = !showFiles.value
  nextTick(() => fitActive())
}

function toggleSide() {
  showSide.value = !showSide.value
  nextTick(() => fitActive())
}

// 建立会话后自动展开系统信息（连接后才需要看 CPU / 内存 / 交换 / 磁盘），
// 用户手动收起后不再自动弹出。
function autoRevealSide() {
  if (sideAutoShown || showSide.value || !state.tabs.length) return
  sideAutoShown = true
  showSide.value = true
  nextTick(() => fitActive())
}

// 主机刚开启监控采集：立即让当前会话的监控面板开始轮询。
function markMonitored() {
  if (activeTab.value) activeTab.value.monitored = true
}

// 展开底部面板并切到命令标签页，供工具条快捷入口调用。
function openCommandPanel() {
  showFiles.value = true
  bottomTab.value = 'cmd'
  nextTick(() => fitActive())
}

function clearActive() {
  const entry = terms.get(activeId.value)
  if (entry && entry.term) entry.term.clear()
}

// ---------------------------------------------------------------- 终端右键菜单

// 只有已经建好 xterm 实例（且没被销毁）的会话才能操作。
function activeEntry() {
  const entry = terms.get(activeId.value)
  return entry && !entry.disposed ? entry : null
}

function focusTerm() {
  const entry = activeEntry()
  if (entry) entry.term.focus()
}

function openTermCtx(event) {
  const entry = activeEntry()
  termCtx.open = true
  termCtx.ready = !!entry
  termCtx.hasSelection = !!(entry && entry.term.hasSelection())
  // 菜单约 172×140，贴近窗口右下角时往回收，避免溢出。
  termCtx.x = Math.max(8, Math.min(event.clientX, window.innerWidth - 184))
  termCtx.y = Math.max(8, Math.min(event.clientY, window.innerHeight - 144))
}

function closeTermCtx() {
  termCtx.open = false
}

// 安全上下文（https / localhost）用异步剪贴板 API；http 直连 IP 时它不可用，
// 退回临时 textarea + execCommand，保证内网部署也能复制。
async function writeClipboard(text) {
  if (navigator.clipboard && window.isSecureContext) {
    await navigator.clipboard.writeText(text)
    return true
  }
  const area = document.createElement('textarea')
  area.value = text
  area.setAttribute('readonly', '')
  area.style.position = 'fixed'
  area.style.top = '-1000px'
  area.style.opacity = '0'
  document.body.appendChild(area)
  area.select()
  let ok = false
  try {
    ok = document.execCommand('copy')
  } catch (err) {
    ok = false
  }
  document.body.removeChild(area)
  return ok
}

async function termCopy() {
  const entry = activeEntry()
  const text = entry ? entry.term.getSelection() : ''
  closeTermCtx()
  if (!text) return
  let ok = false
  try {
    ok = await writeClipboard(text)
  } catch (err) {
    ok = false
  }
  if (ok) toast('已复制选中的内容')
  else toast('复制失败，请检查浏览器的剪贴板权限', 'error')
  focusTerm()
}

async function termPaste() {
  const entry = activeEntry()
  closeTermCtx()
  if (!entry) return
  if (!navigator.clipboard || !window.isSecureContext) {
    toast('当前环境无法读取剪贴板，请用 Ctrl+V 粘贴', 'warn')
    return
  }
  try {
    const text = await navigator.clipboard.readText()
    // 走 term.paste，换行与 bracketed paste 由 xterm 统一处理，避免多行命令被拆执行。
    if (text) entry.term.paste(text)
  } catch (err) {
    toast('浏览器未授权读取剪贴板，请用 Ctrl+V 粘贴', 'warn')
  }
  focusTerm()
}

function termClear() {
  closeTermCtx()
  clearActive()
  focusTerm()
}

// ---------------------------------------------------------------- 终端查找

// 命中高亮只支持 #RRGGBB，深浅色各配一套，保证高亮上的文字仍然可读。
const FIND_COLORS = {
  light: {
    matchBackground: '#ffe58f',
    matchBorder: '#ffd666',
    matchOverviewRuler: '#ffd666',
    activeMatchBackground: '#ffc53d',
    activeMatchBorder: '#ff9c00',
    activeMatchColorOverviewRuler: '#ff9c00'
  },
  dark: {
    matchBackground: '#5c4410',
    matchBorder: '#8a6a18',
    matchOverviewRuler: '#8a6a18',
    activeMatchBackground: '#8a6a18',
    activeMatchBorder: '#ffb454',
    activeMatchColorOverviewRuler: '#ffb454'
  }
}

function searchOptions(incremental) {
  return {
    caseSensitive: false,
    incremental,
    decorations: FIND_COLORS[resolvedTheme()] || FIND_COLORS.dark
  }
}

function resetFindCount() {
  finder.index = 0
  finder.count = 0
}

// 输入时做增量查找：命中位置不动，只是把当前词扩展成更长的词。
function runFind() {
  const entry = activeEntry()
  if (!entry) return
  if (!finder.keyword) {
    entry.search.clearDecorations()
    resetFindCount()
    return
  }
  entry.search.findNext(finder.keyword, searchOptions(true))
}

function stepFind(backward) {
  const entry = activeEntry()
  if (!entry || !finder.keyword) return
  const options = searchOptions(false)
  if (backward) entry.search.findPrevious(finder.keyword, options)
  else entry.search.findNext(finder.keyword, options)
}

function findNext() {
  stepFind(false)
}

function findPrev() {
  stepFind(true)
}

function termFind() {
  closeTermCtx()
  finder.open = true
  nextTick(() => {
    if (findInput.value) {
      findInput.value.focus()
      findInput.value.select()
    }
  })
  if (finder.keyword) runFind()
  else resetFindCount()
}

function closeFind() {
  finder.open = false
  resetFindCount()
  const entry = activeEntry()
  if (entry) entry.search.clearDecorations()
  focusTerm()
}

function onFindKey(event) {
  if (event.key === 'Escape') {
    event.preventDefault()
    closeFind()
  } else if (event.key === 'Enter') {
    event.preventDefault()
    stepFind(event.shiftKey)
  }
}

const findCountText = computed(() => {
  if (!finder.open || !finder.keyword) return ''
  if (!finder.count) return '无匹配'
  return `${finder.index >= 0 ? finder.index + 1 : '-'}/${finder.count}`
})

// 切换会话时把查找条挂到新的终端上。
watch(activeId, () => {
  if (!finder.open) return
  resetFindCount()
  nextTick(runFind)
})

// 文件传输弹窗入口（工具条）：默认定位到当前会话对应的主机。
function openTransfer() {
  if (!activeTab.value) return
  openFileTransfer(activeTab.value.sshId)
}

// 拖拽分隔条：底部文件面板改高度，右侧信息栏改宽度。
function startResize(kind, event) {
  resizing = {
    kind,
    startX: event.clientX,
    startY: event.clientY,
    height: fileHeight.value,
    width: sideWidth.value
  }
  event.preventDefault()
  window.addEventListener('pointermove', onResizeMove)
  window.addEventListener('pointerup', stopResize)
  window.addEventListener('pointercancel', stopResize)
  document.body.style.userSelect = 'none'
}

// 拖拽时 pointermove 触发频率远高于屏幕刷新，先记下目标值、按帧写回，
// 否则每个事件都会推动一次终端重排。
function onResizeMove(event) {
  if (!resizing) return
  if (resizing.kind === 'files') {
    const next = resizing.height - (event.clientY - resizing.startY)
    pendingSize = Math.round(clamp(next, 120, Math.max(160, window.innerHeight - 280)))
  } else {
    const next = resizing.width - (event.clientX - resizing.startX)
    pendingSize = Math.round(clamp(next, 220, 520))
  }
  if (resizeFrame) return
  resizeFrame = window.requestAnimationFrame(applyPendingSize)
}

function applyPendingSize() {
  resizeFrame = 0
  if (pendingSize === null) return
  if (resizing && resizing.kind === 'side') sideWidth.value = pendingSize
  else fileHeight.value = pendingSize
  pendingSize = null
}

function stopResize() {
  if (!resizing) return
  resizing = null
  if (resizeFrame) {
    window.cancelAnimationFrame(resizeFrame)
    applyPendingSize()
  }
  document.body.style.userSelect = ''
  window.removeEventListener('pointermove', onResizeMove)
  window.removeEventListener('pointerup', stopResize)
  window.removeEventListener('pointercancel', stopResize)
  fitActive()
}

function clamp(value, min, max) {
  return Math.min(Math.max(value, min), max)
}

// 心跳：仅测量当前会话的往返时延，供工具条与底部状态栏展示。
function startHeartbeat() {
  stopHeartbeat()
  heartbeat = window.setInterval(() => {
    const entry = terms.get(activeId.value)
    if (!entry || !entry.ws || entry.ws.readyState !== WebSocket.OPEN) {
      state.latency = 0
      return
    }
    entry.pingAt = performance.now()
    entry.ws.send(JSON.stringify({ type: 'ping' }))
  }, 5000)
}

function stopHeartbeat() {
  if (heartbeat) window.clearInterval(heartbeat)
  heartbeat = null
}

function ensureTerminal(tab, attempt = 0) {
  if (terms.has(tab.id)) return terms.get(tab.id)
  const el = hosts.get(tab.id)
  if (!el) {
    // 容器尚未渲染，下一帧重试；标签已被关闭或重试超限时放弃。
    if (attempt >= 60 || !state.tabs.some((item) => item.id === tab.id)) return null
    window.requestAnimationFrame(() => ensureTerminal(tab, attempt + 1))
    return null
  }

  const term = new Terminal({
    cursorBlink: true,
    fontSize: 13,
    fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace',
    scrollback: 5000,
    theme: terminalTheme()
  })
  const fit = new FitAddon()
  const search = new SearchAddon()
  term.loadAddon(fit)
  term.loadAddon(new WebLinksAddon())
  term.loadAddon(search)
  term.open(el)
  try {
    fit.fit()
  } catch (err) {
    // 容器尺寸为 0 时忽略。
  }

  const entry = {
    term,
    fit,
    search,
    ws: null,
    observer: null,
    retry: 0,
    disposed: false,
    closed: false,
    pending: [],      // 待写出的输出分片
    pendingBytes: 0,
    flushTimer: 0,
    fitTimer: 0,      // 尺寸合并帧
    lastCols: 0,      // 已同步给远端的列数，用于去重
    lastRows: 0
  }
  terms.set(tab.id, entry)

  tab.onClose = () => teardown(tab.id, entry)
  tab.status = tab.status || 'connecting'

  const observer = new ResizeObserver(() => scheduleFit(tab, entry))
  observer.observe(el)
  entry.observer = observer

  entry.disposable = term.onData((data) => {
    if (entry.ws && entry.ws.readyState === WebSocket.OPEN) {
      entry.ws.send(JSON.stringify({ type: 'input', data }))
    }
  })
  entry.disposableResize = term.onResize(({ cols, rows }) => {
    if (entry.ws && entry.ws.readyState === WebSocket.OPEN) {
      entry.ws.send(JSON.stringify({ type: 'resize', cols, rows }))
    }
  })
  // 查找结果计数只回写给当前激活的会话，避免后台标签页覆盖面板上的数字。
  entry.disposableResult = search.onDidChangeResults(({ resultIndex, resultCount }) => {
    if (activeId.value !== tab.id) return
    finder.index = resultIndex
    finder.count = resultCount
  })

  connect(tab, entry)
  return entry
}

function connect(tab, entry) {
  const proto = window.location.protocol === 'https:' ? 'wss' : 'ws'
  const url = `${proto}://${window.location.host}${tab.wsUrl || '/api/ws/terminal/' + tab.id}`
  const ws = new WebSocket(url)
  entry.ws = ws
  tab.status = 'connecting'

  ws.onopen = () => {
    entry.retry = 0
    // 新连接要把本地视口尺寸同步过去，先清掉去重记录。
    entry.lastCols = 0
    entry.lastRows = 0
    sendResize(tab, entry)
  }

  ws.onmessage = (event) => {
    let msg = null
    try {
      msg = JSON.parse(event.data)
    } catch (err) {
      return
    }
    switch (msg.type) {
      case 'ready':
        // 接入既有会话（或重连）后，以本地视口为准同步远端 PTY 尺寸。
        tab.status = 'ready'
        entry.retry = 0
        if (tab.id === activeId.value) fitActive()
        else sendResize(tab, entry)
        break
      case 'output':
        queueOutput(entry, msg.data)
        break
      case 'ping':
        break
      case 'pong':
        if (entry.pingAt) {
          state.latency = Math.round(performance.now() - entry.pingAt)
          entry.pingAt = 0
        }
        break
      case 'exit':
        tab.status = 'closed'
        writeNotice(entry, `\r\n\x1b[33m[会话结束] ${msg.reason || '连接已关闭'}\x1b[0m\r\n`)
        entry.closed = true
        break
      case 'closed':
        tab.status = 'closed'
        writeNotice(entry, `\r\n\x1b[33m[会话结束] ${msg.reason || '连接已关闭'}\x1b[0m\r\n`)
        entry.closed = true
        break
      case 'error':
        tab.status = 'error'
        writeNotice(entry, `\r\n\x1b[31m[错误] ${msg.reason || '连接异常'}\x1b[0m\r\n`)
        break
      default:
        break
    }
  }

  ws.onclose = () => {
    if (entry.disposed) return
    if (entry.closed) {
      tab.status = 'closed'
      return
    }
    if (entry.retry < 5) {
      entry.retry += 1
      tab.status = 'connecting'
      writeNotice(entry, `\r\n\x1b[33m[断开] 正在重连（第 ${entry.retry} 次）…\x1b[0m\r\n`)
      window.setTimeout(() => {
        if (!entry.disposed && state.tabs.some((item) => item.id === tab.id)) connect(tab, entry)
      }, 1500 * entry.retry)
      return
    }
    tab.status = 'closed'
    writeNotice(entry, '\r\n\x1b[31m[断开] 重连失败，请手动重新连接\x1b[0m\r\n')
  }

  ws.onerror = () => {
    if (!entry.disposed) tab.status = 'error'
  }
}

// 尺寸变化的合并入口：一帧内多次触发只做一次 fit + 一次 resize 帧。
function scheduleFit(tab, entry) {
  if (entry.disposed || entry.fitTimer || !pageActive) return
  entry.fitTimer = window.requestAnimationFrame(() => {
    entry.fitTimer = 0
    if (entry.disposed) return
    // 容器不可见（切走、隐藏标签页）时尺寸为 0，强行 fit 会把远端 PTY 改成极小的尺寸。
    const el = hosts.get(tab.id)
    if (!el || !el.clientWidth || !el.clientHeight) return
    try {
      entry.fit.fit()
    } catch (err) {
      return
    }
    sendResize(tab, entry)
  })
}

function sendResize(tab, entry) {
  if (!entry.ws || entry.ws.readyState !== WebSocket.OPEN) return
  const cols = entry.term.cols
  const rows = entry.term.rows
  // 尺寸没变就不打扰远端 PTY（拖拽与 ResizeObserver 会重复触发）。
  if (entry.lastCols === cols && entry.lastRows === rows) return
  entry.lastCols = cols
  entry.lastRows = rows
  entry.ws.send(JSON.stringify({ type: 'resize', cols, rows }))
}

// 输出积压：把这段时间收到的分片攒起来一次性交给 xterm，
// 高频输出（cat 大文件、tail -f、npm install）时避免每条消息都单独渲染一帧。
const FLUSH_INTERVAL_MS = 8
const FLUSH_BYTES = 32 * 1024

// base64 只还原成字节，UTF-8 解码交给 xterm 自己做：
// 既能跨消息正确拼接被切断的多字节字符，也省掉每条消息构造一次 TextDecoder。
function decodeBase64(data) {
  if (!data) return null
  const binary = window.atob(data)
  const bytes = new Uint8Array(binary.length)
  for (let i = 0; i < binary.length; i += 1) bytes[i] = binary.charCodeAt(i)
  return bytes
}

function queueOutput(entry, data) {
  const bytes = decodeBase64(data)
  if (!bytes || !bytes.length) return
  entry.pending.push(bytes)
  entry.pendingBytes += bytes.length
  if (entry.pendingBytes >= FLUSH_BYTES) {
    flushOutput(entry)
    return
  }
  if (!entry.flushTimer) {
    entry.flushTimer = window.setTimeout(() => flushOutput(entry), FLUSH_INTERVAL_MS)
  }
}

function flushOutput(entry) {
  if (entry.flushTimer) {
    window.clearTimeout(entry.flushTimer)
    entry.flushTimer = 0
  }
  const chunks = entry.pending
  if (!chunks.length || entry.disposed) return
  entry.pending = []
  entry.pendingBytes = 0
  let payload = chunks[0]
  if (chunks.length > 1) {
    const total = chunks.reduce((sum, chunk) => sum + chunk.length, 0)
    payload = new Uint8Array(total)
    let offset = 0
    chunks.forEach((chunk) => {
      payload.set(chunk, offset)
      offset += chunk.length
    })
  }
  entry.term.write(payload)
}

// 提示文本先冲掉积压输出，保证提示排在已收到的内容之后。
function writeNotice(entry, text) {
  flushOutput(entry)
  entry.term.write(text)
}

function teardown(id, entry) {
  if (!entry || entry.disposed) return
  entry.disposed = true
  if (entry.flushTimer) window.clearTimeout(entry.flushTimer)
  if (entry.fitTimer) window.cancelAnimationFrame(entry.fitTimer)
  entry.pending = []
  entry.pendingBytes = 0
  if (entry.observer) entry.observer.disconnect()
  if (entry.disposable) entry.disposable.dispose()
  if (entry.disposableResize) entry.disposableResize.dispose()
  if (entry.disposableResult) entry.disposableResult.dispose()
  if (entry.ws) {
    try {
      entry.ws.close()
    } catch (err) {
      // 忽略关闭异常。
    }
  }
  entry.term.dispose()
  terms.delete(id)
}

function activate(id) {
  activeId.value = id
}

function fitActive() {
  const entry = terms.get(activeId.value)
  if (!entry || entry.disposed) return
  const el = hosts.get(activeId.value)
  if (!el || !el.clientWidth || !el.clientHeight) return
  try {
    entry.fit.fit()
  } catch (err) {
    return
  }
  sendResize({ id: activeId.value }, entry)
}

async function closeTerminal(tab) {
  try {
    await sessionApi.close(tab.id)
  } catch (err) {
    // 会话可能已结束，忽略异常。
  }
  closeTab(tab.id)
}

async function reconnect(tab) {
  const entry = terms.get(tab.id)
  if (!entry) return
  if (entry.ws) {
    try {
      entry.ws.close()
    } catch (err) {
      // 忽略。
    }
  }
  entry.closed = false
  entry.retry = 0
  writeNotice(entry, '\r\n\x1b[36m[重连] 正在重新接入会话…\x1b[0m\r\n')
  connect(tab, entry)
}

async function openPicker() {
  picker.open = true
  picker.search = ''
  picker.loading = true
  try {
    const data = await sshApi.list({})
    picker.hosts = data.items || []
  } catch (err) {
    toastError(err)
    picker.hosts = []
  } finally {
    picker.loading = false
  }
}

async function connectHost(host) {
  picker.busy = true
  try {
    const data = await sshApi.connect(host.id, { cols: 120, rows: 32, title: host.name })
    markRecent(host.id)
    const tab = { id: data.session_id, wsUrl: data.ws_url, sshId: host.id, title: host.name, status: 'connecting', monitored: data.monitored }
    openTab(tab)
    picker.open = false
    activeId.value = tab.id
    await nextTick()
    ensureTerminal(tab)
    toast('会话已建立', 'success')
  } catch (err) {
    toastError(err)
  } finally {
    picker.busy = false
  }
}

function statusColor(status) {
  if (status === 'ready') return 'var(--success)'
  if (status === 'error') return 'var(--danger)'
  if (status === 'closed') return 'var(--text-dim)'
  return 'var(--warn)'
}

function statusText(tab) {
  if (tab.status === 'ready') return '已连接'
  if (tab.status === 'error') return '连接异常'
  if (tab.status === 'closed') return '会话已关闭'
  return '正在连接…'
}
</script>
