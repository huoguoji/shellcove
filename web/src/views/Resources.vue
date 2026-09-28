<template>
  <div class="grid res-grid">
    <!-- 资源目录：文件夹 — SSH 名称 的层级树 -->
    <div class="card res-tree">
      <div class="card-title">
        <h3>资源目录</h3>
        <span class="muted" style="font-size: 12px">右键主机或文件夹可操作 · 支持多级嵌套</span>
        <div class="row">
          <button v-if="isAdmin" class="btn btn-sm" @click="openFolderForm(null, folderId || null)">新建文件夹</button>
          <button v-if="isAdmin" class="btn btn-sm btn-primary" @click="openForm(null)">新建主机</button>
        </div>
      </div>

      <div class="res-tree-body">
        <div class="tree-row" :class="{ active: !folderId && !selectedHostId }" @click="selectFolder('')">
          <span class="tree-caret" @click.stop="rootOpen = !rootOpen">{{ rootOpen ? '▾' : '▸' }}</span>
          <svg class="tree-icon folder" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linejoin="round">
            <path d="M1.9 4.2a1.4 1.4 0 0 1 1.4-1.4h2.5l1.2 1.4h5.2a1.4 1.4 0 0 1 1.4 1.4v6.2a1.4 1.4 0 0 1-1.4 1.4H3.3a1.4 1.4 0 0 1-1.4-1.4z" />
          </svg>
          <span class="tree-label">全部主机</span>
          <span class="tree-count">{{ allHosts.length }}</span>
          <span class="spacer"></span>
        </div>

        <template v-if="rootOpen">
          <!-- 未分类主机直接挂在根节点下 -->
          <div
            v-for="host in ungroupedHosts"
            :key="host.id"
            class="tree-row host-row"
            :class="{ active: selectedHostId === host.id }"
            :title="hostTooltip(host)"
            @click="selectHost(host)"
            @dblclick="connect(host)"
            @contextmenu.prevent="openCtx($event, host)"
          >
            <span class="tree-caret"></span>
            <svg class="tree-icon host" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linejoin="round">
              <rect x="2.4" y="2.6" width="11.2" height="4.4" rx="1.2" /><rect x="2.4" y="9" width="11.2" height="4.4" rx="1.2" />
              <path d="M5 4.8h.01M5 11.2h.01" stroke-linecap="round" />
            </svg>
            <span class="tree-label">{{ host.name }}</span>
            <span class="tree-host">{{ host.username }}@{{ host.host }}</span>
            <button class="icon-btn tree-go" title="连接" @click.stop="connect(host)">
              <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
                <path d="M3.6 8h8.8" /><path d="M9 4.6 12.4 8 9 11.4" />
              </svg>
            </button>
          </div>

          <folder-node
            v-for="node in tree"
            :key="node.id"
            :node="node"
            :active="selectedHostId ? '' : folderId"
            :host-map="hostMap"
            :host-title="hostTooltip"
            :selected-host="selectedHostId"
            @select="selectFolder"
            @folder-action="promptFolder"
            @folder-ctx="openFolderCtx"
            @host-select="selectHost"
            @host-connect="connect"
            @host-ctx="onHostCtx"
          />
        </template>

        <div v-if="!tree.length && !ungroupedHosts.length" class="sys-note">暂无主机，点击右上角「新建主机」</div>
      </div>
    </div>

    <!-- 主机右键菜单：连接 / 编辑 / 重命名 / 凭证 / 删除 -->
    <template v-if="ctx.open">
      <div class="ctx-layer" @click="closeCtx" @contextmenu.prevent="closeCtx"></div>
      <div class="ctx-menu" :style="{ left: ctx.x + 'px', top: ctx.y + 'px' }">
        <div class="ctx-title">{{ ctx.host && ctx.host.name }}</div>
        <button @click="runCtx(() => connect(ctx.host))">连接</button>
        <template v-if="isAdmin">
          <button @click="runCtx(() => openForm(ctx.host))">编辑</button>
          <button @click="runCtx(() => renameHost(ctx.host))">重命名</button>
        </template>
        <button @click="runCtx(() => showReveal(ctx.host))">查看凭证</button>
        <template v-if="isAdmin">
          <div class="ctx-sep"></div>
          <button class="danger" @click="runCtx(() => removeHost(ctx.host))">删除</button>
        </template>
      </div>
    </template>

    <!-- 文件夹右键菜单：新建子文件夹 / 重命名 / 删除 -->
    <template v-if="folderCtx.open">
      <div class="ctx-layer" @click="closeFolderCtx" @contextmenu.prevent="closeFolderCtx"></div>
      <div class="ctx-menu" :style="{ left: folderCtx.x + 'px', top: folderCtx.y + 'px' }">
        <div class="ctx-title">{{ folderCtx.node && folderCtx.node.name }}</div>
        <button @click="runFolderCtx((node) => openFolderForm(null, node.id))">新建子文件夹</button>
        <button @click="runFolderCtx((node) => openFolderForm(node))">重命名 / 移动</button>
        <div class="ctx-sep"></div>
        <button class="danger" @click="runFolderCtx((node) => removeFolder(node))">删除</button>
      </div>
    </template>

    <!-- 新建 / 编辑文件夹：支持任意层级嵌套 -->
    <div v-if="folderForm.open" class="modal-mask" @click.self="folderForm.open = false">
      <div class="modal">
        <div class="modal-head">
          <h3>{{ folderForm.id ? '编辑文件夹' : '新建文件夹' }}</h3>
          <button class="btn btn-sm btn-ghost" @click="folderForm.open = false">关闭</button>
        </div>
        <div class="modal-body">
          <label class="field"><span>名称</span><input v-model="folderForm.name" placeholder="生产环境" @keyup.enter="saveFolder" /></label>
          <label class="field"><span>上级文件夹</span>
            <select v-model="folderForm.parent_id">
              <option :value="null">（根目录）</option>
              <option v-for="item in parentOptions" :key="item.id" :value="item.id">{{ item.label }}</option>
            </select>
          </label>
          <p class="muted" style="margin: 0">层级不限，例如 机房 / 业务 / 生产 / 主机。</p>
          <p v-if="folderForm.error" class="badge badge-danger" style="display: block; padding: 8px 10px">{{ folderForm.error }}</p>
        </div>
        <div class="modal-foot">
          <button class="btn" @click="folderForm.open = false">取消</button>
          <button class="btn btn-primary" :disabled="folderForm.saving" @click="saveFolder">{{ folderForm.saving ? '保存中…' : '保存' }}</button>
        </div>
      </div>
    </div>

    <!-- 新建 / 编辑 -->
    <div v-if="form.open" class="modal-mask" @click.self="form.open = false">
      <div class="modal modal-lg">
        <div class="modal-head">
          <h3>{{ form.id ? '编辑主机' : '新建主机' }}</h3>
          <button class="btn btn-sm btn-ghost" @click="form.open = false">关闭</button>
        </div>
        <div class="modal-body">
          <label class="field"><span>名称</span><input v-model="form.data.name" placeholder="生产环境 Web-01" /></label>
          <div class="field-row">
            <label class="field"><span>主机</span><input v-model="form.data.host" placeholder="192.168.1.10" /></label>
            <label class="field" style="max-width: 120px"><span>端口</span><input v-model.number="form.data.port" type="number" /></label>
          </div>
          <div class="field-row">
            <label class="field"><span>用户名</span><input v-model="form.data.username" placeholder="root" /></label>
            <label class="field"><span>认证方式</span>
              <select v-model="form.data.auth_type">
                <option value="password">密码</option>
                <option value="publickey">公钥</option>
                <option value="keyboard-interactive">键盘交互</option>
              </select>
            </label>
          </div>

          <label v-if="form.data.auth_type !== 'publickey'" class="field">
            <span>登录密码{{ form.id ? '（留空表示不修改）' : '' }}</span>
            <input v-model="form.data.password" type="password" autocomplete="new-password" placeholder="••••••" />
          </label>
          <label v-else class="field">
            <span>私钥内容{{ form.id ? '（留空表示不修改）' : '' }}</span>
            <textarea v-model="form.data.private_key" placeholder="-----BEGIN OPENSSH PRIVATE KEY-----"></textarea>
          </label>
          <label v-if="form.data.auth_type === 'publickey'" class="field">
            <span>私钥口令 passphrase{{ form.id ? '（留空表示不修改）' : '' }}</span>
            <input v-model="form.data.passphrase" type="password" autocomplete="new-password" />
          </label>

          <div class="field-row">
            <label class="field"><span>代理类型</span>
              <select v-model="form.data.proxy_type">
                <option value="">不使用代理</option>
                <option value="socks5">Socks5 代理</option>
                <option value="http">HTTP 代理</option>
              </select>
            </label>
            <label class="field"><span>跳板机</span>
              <select v-model="form.data.jump_ssh_id">
                <option :value="null">不使用跳板机</option>
                <option v-for="host in jumpCandidates" :key="host.id" :value="host.id">
                  {{ host.name }}（{{ host.host }}）
                </option>
              </select>
            </label>
          </div>
          <div v-if="form.data.proxy_type" class="field-row">
            <label class="field"><span>代理地址</span><input v-model="form.data.proxy_host" placeholder="127.0.0.1" /></label>
            <label class="field" style="max-width: 140px"><span>代理端口</span><input v-model.number="form.data.proxy_port" type="number" /></label>
          </div>

          <div class="field-row">
            <label class="field"><span>所属文件夹</span>
              <select v-model="form.data.folder_id">
                <option :value="null">未分类</option>
                <option v-for="item in flatFolders" :key="item.id" :value="item.id">{{ item.label }}</option>
              </select>
            </label>
            <label class="field"><span>备注</span><input v-model="form.data.remark" placeholder="用途说明" /></label>
          </div>

          <label class="row" style="gap: 8px">
            <input v-model="form.data.monitor_enabled" type="checkbox" style="width: auto" />
            <span>开启后终端可查看 CPU / 内存 / 交换区 / 磁盘 / 网络（建议保持开启）</span>
          </label>
          <p v-if="form.error" class="badge badge-danger" style="display: block; padding: 8px 10px">{{ form.error }}</p>
        </div>
        <div class="modal-foot">
          <button class="btn" @click="form.open = false">取消</button>
          <button class="btn btn-primary" :disabled="form.saving" @click="saveHost">{{ form.saving ? '保存中…' : '保存' }}</button>
        </div>
      </div>
    </div>

    <!-- 凭证二次验证 -->
    <div v-if="reveal.open" class="modal-mask" @click.self="closeReveal">
      <div class="modal">
        <div class="modal-head">
          <h3>查看凭证 · {{ reveal.name }}</h3>
          <button class="btn btn-sm btn-ghost" @click="closeReveal">关闭</button>
        </div>
        <div class="modal-body">
          <template v-if="!reveal.secret">
            <p class="muted" style="margin-top: 0">出于安全考虑，查看明文凭证需要再次验证身份，该操作会被记录审计日志。</p>
            <label class="field"><span>登录密码</span><input v-model="reveal.password" type="password" autocomplete="current-password" /></label>
            <label v-if="userTotpEnabled" class="field"><span>动态验证码 / 恢复码</span><input v-model="reveal.code" autocomplete="one-time-code" /></label>
            <p v-if="reveal.error" class="badge badge-danger" style="display: block; padding: 8px 10px">{{ reveal.error }}</p>
          </template>
          <template v-else>
            <div class="kv">
              <b>地址</b><span class="mono">{{ reveal.secret.username }}@{{ reveal.secret.host }}:{{ reveal.secret.port }}</span>
              <b>密码</b><span class="mono">{{ reveal.secret.password || '（未配置）' }}</span>
              <b>私钥</b>
              <span>
                <button class="btn btn-sm" @click="copy(reveal.secret.private_key)">复制私钥</button>
              </span>
              <b>私钥口令</b><span class="mono">{{ reveal.secret.passphrase || '（未配置）' }}</span>
            </div>
            <p class="muted" style="margin-bottom: 0">凭证不会写入浏览器缓存，关闭弹窗后即从界面移除。</p>
          </template>
        </div>
        <div class="modal-foot">
          <button class="btn" @click="closeReveal">关闭</button>
          <button v-if="!reveal.secret" class="btn btn-primary" :disabled="reveal.loading" @click="doReveal">
            {{ reveal.loading ? '验证中…' : '验证并查看' }}
          </button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { computed, onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import FolderNode from '../components/FolderNode.vue'
import { folderApi, sshApi } from '../api'
import { isAdmin, openTab, state, toast, toastError } from '../store'
import { markRecent } from '../recent'

const router = useRouter()
const allHosts = ref([])
const tree = ref([])
const folderId = ref('')
const selectedHostId = ref('')
const rootOpen = ref(true)

const form = reactive({ open: false, id: '', saving: false, error: '', data: blankForm() })
const reveal = reactive({ open: false, id: '', name: '', password: '', code: '', loading: false, error: '', secret: null })
// 资源目录中的主机右键菜单（连接 / 编辑 / 重命名 / 凭证 / 删除）
const ctx = reactive({ open: false, x: 0, y: 0, host: null })
// 文件夹右键菜单（新建子文件夹 / 重命名或移动 / 删除）
const folderCtx = reactive({ open: false, x: 0, y: 0, node: null })
// 文件夹表单：parent_id 决定层级，支持任意深度嵌套。
const folderForm = reactive({ open: false, id: '', name: '', parent_id: null, saving: false, error: '' })

const userTotpEnabled = computed(() => !!state.user && !!state.user.totp_enabled)
const jumpCandidates = computed(() => allHosts.value)
const flatFolders = computed(() => flatten(tree.value))

// 上级文件夹下拉：排除自身与后代，避免把文件夹移动到自己的子目录下。
const parentOptions = computed(() => {
  const self = folderForm.id ? findSubtree(tree.value, folderForm.id) : null
  const excluded = self ? collectIds(self, new Set()) : new Set()
  const out = []
  const push = (nodes, depth) => {
    (nodes || []).forEach((node) => {
      if (excluded.has(node.id)) return
      out.push({ id: node.id, label: '　'.repeat(depth) + node.name })
      push(node.children, depth + 1)
    })
  }
  push(tree.value, 0)
  return out
})

// 资源目录树：按文件夹聚合主机，未分类主机挂在根节点下。
const ungroupedHosts = computed(() => allHosts.value.filter((host) => !host.folder_id))
const hostMap = computed(() => {
  const map = {}
  allHosts.value.forEach((host) => {
    const key = host.folder_id || '__none__'
    if (!map[key]) map[key] = []
    map[key].push(host)
  })
  return map
})
function blankForm() {
  return {
    name: '', remark: '', host: '', port: 22, username: '', auth_type: 'password',
    password: '', private_key: '', passphrase: '', folder_id: null, monitor_enabled: true,
    proxy_type: '', proxy_host: '', proxy_port: 0, jump_ssh_id: null
  }
}

function flatten(nodes, depth = 0) {
  if (!nodes || !nodes.length) return []
  const out = []
  nodes.forEach((node) => {
    out.push({ id: node.id, label: '　'.repeat(depth) + node.name })
    out.push(...flatten(node.children, depth + 1))
  })
  return out
}

function findSubtree(nodes, id) {
  for (const node of nodes || []) {
    if (node.id === id) return node
    const hit = findSubtree(node.children, id)
    if (hit) return hit
  }
  return null
}

function collectIds(node, out) {
  if (!node) return out
  out.add(node.id)
  ;(node.children || []).forEach((child) => collectIds(child, out))
  return out
}

onMounted(async () => {
  await Promise.all([loadFolders(), loadAllHosts()])
})

async function loadFolders() {
  try {
    const data = await folderApi.tree()
    tree.value = data.items || []
  } catch (err) {
    toastError(err)
  }
}

// 资源目录一次性拉取全量主机（含未分类），树里按文件夹聚合展示。
async function loadAllHosts() {
  try {
    const data = await sshApi.list({})
    allHosts.value = data.items || []
  } catch (err) {
    toastError(err)
  }
}

function selectFolder(id) {
  folderId.value = id || ''
  selectedHostId.value = ''
}

// 在树里点选主机只做高亮，连接走双击 / 悬停箭头 / 右键菜单。
function selectHost(host) {
  selectedHostId.value = host.id
}

// 文件夹操作：行内 ⋯ 菜单与右键菜单共用同一套表单。
function promptFolder(node) {
  if (!isAdmin.value) return
  if (node && node.action === 'rename') {
    openFolderForm(findSubtree(tree.value, node.id) || { id: node.id, name: node.name })
    return
  }
  if (node && node.action === 'delete') {
    removeFolder(node)
    return
  }
  // action === 'create'：在该文件夹下新建子文件夹。
  openFolderForm(null, node && node.id ? node.id : null)
}

function openFolderForm(item, parentId = null) {
  folderForm.error = ''
  if (item) {
    folderForm.id = item.id
    folderForm.name = item.name
    folderForm.parent_id = item.parent_id || null
  } else {
    folderForm.id = ''
    folderForm.name = ''
    folderForm.parent_id = parentId || null
  }
  folderForm.open = true
}

async function saveFolder() {
  folderForm.error = ''
  const name = folderForm.name.trim()
  if (!name) {
    folderForm.error = '请填写文件夹名称'
    return
  }
  folderForm.saving = true
  try {
    if (folderForm.id) {
      // parent_id 传 null 表示移动到根目录。
      await folderApi.update(folderForm.id, { name, parent_id: folderForm.parent_id })
      toast('文件夹已更新', 'success')
    } else {
      await folderApi.create({ name, parent_id: folderForm.parent_id })
      toast('文件夹已创建', 'success')
    }
    folderForm.open = false
    await Promise.all([loadFolders(), loadAllHosts()])
  } catch (err) {
    folderForm.error = err.message
  } finally {
    folderForm.saving = false
  }
}

async function removeFolder(node) {
  if (!isAdmin.value || !node) return
  if (!window.confirm(`删除「${node.name}」会连同子文件夹一起删除，其下主机将移入回收站，确定继续？`)) return
  try {
    await folderApi.remove(node.id, true)
    if (folderId.value === node.id) selectFolder('')
    await Promise.all([loadFolders(), loadAllHosts()])
    toast('文件夹已删除', 'success')
  } catch (err) {
    toastError(err)
  }
}

function openForm(item) {
  form.error = ''
  if (item) {
    form.id = item.id
    form.data = {
      ...blankForm(),
      name: item.name, remark: item.remark, host: item.host, port: item.port,
      username: item.username, auth_type: item.auth_type,
      folder_id: item.folder_id || null, monitor_enabled: item.monitor_enabled,
      proxy_type: item.proxy_type || '', proxy_host: item.proxy_host || '',
      proxy_port: item.proxy_port || 0, jump_ssh_id: item.jump_ssh_id || null
    }
  } else {
    form.id = ''
    form.data = blankForm()
    form.data.folder_id = folderId.value || null
  }
  form.open = true
}

async function saveHost() {
  form.error = ''
  if (!form.data.name.trim() || !form.data.host.trim() || !form.data.username.trim()) {
    form.error = '名称、主机与用户名均为必填项'
    return
  }
  if (!form.id && form.data.auth_type !== 'publickey' && !form.data.password) {
    form.error = '请填写登录密码'
    return
  }
  if (!form.id && form.data.auth_type === 'publickey' && !form.data.private_key.trim()) {
    form.error = '请粘贴私钥内容'
    return
  }
  form.saving = true
  try {
    const payload = { ...form.data }
    payload.port = Number(payload.port) || 22
    if (form.id) {
      await sshApi.update(form.id, payload)
      toast('主机已更新', 'success')
    } else {
      await sshApi.create(payload)
      toast('主机已创建', 'success')
    }
    form.open = false
    await loadAllHosts()
  } catch (err) {
    form.error = err.message
  } finally {
    form.saving = false
  }
}

async function removeHost(item) {
  if (!window.confirm(`确定将「${item.name}」移入回收站？`)) return
  try {
    await sshApi.remove(item.id)
    toast('已移入回收站', 'success')
    if (selectedHostId.value === item.id) selectedHostId.value = ''
    await loadAllHosts()
  } catch (err) {
    toastError(err)
  }
}

// 树里直接改主机名：敏感字段留空，避免覆盖已有凭证。
async function renameHost(item) {
  const name = window.prompt('主机名称', item.name)
  if (!name || name === item.name) return
  try {
    await sshApi.update(item.id, hostPayload(item, name))
    await loadAllHosts()
    toast('主机已重命名', 'success')
  } catch (err) {
    toastError(err)
  }
}

function hostPayload(item, name) {
  return {
    name, remark: item.remark || '', host: item.host, port: item.port, username: item.username,
    auth_type: item.auth_type, password: '', private_key: '', passphrase: '',
    folder_id: item.folder_id || null, monitor_enabled: item.monitor_enabled,
    proxy_type: item.proxy_type || '', proxy_host: item.proxy_host || '',
    proxy_port: item.proxy_port || 0, jump_ssh_id: item.jump_ssh_id || null
  }
}

async function connect(item) {
  try {
    const data = await sshApi.connect(item.id, { cols: 120, rows: 32, title: item.name })
    markRecent(item.id)
    selectedHostId.value = item.id
    openTab({ id: data.session_id, sshId: item.id, title: item.name, status: 'attached', monitored: data.monitored })
    router.push('/terminal')
  } catch (err) {
    toastError(err)
  }
}

async function showReveal(item) {
  reveal.open = true
  reveal.id = item.id
  reveal.name = item.name
  reveal.password = ''
  reveal.code = ''
  reveal.error = ''
  reveal.secret = null
}

function closeReveal() {
  reveal.open = false
  reveal.secret = null
  reveal.password = ''
  reveal.code = ''
}

async function doReveal() {
  reveal.error = ''
  reveal.loading = true
  try {
    reveal.secret = await sshApi.reveal(reveal.id, { password: reveal.password, code: reveal.code })
  } catch (err) {
    reveal.error = err.code === 'AUTH_FAILED' ? '密码或验证码错误' : err.message
  } finally {
    reveal.loading = false
  }
}

function authLabel(type) {
  return { password: '密码', publickey: '公钥', 'keyboard-interactive': '键盘交互' }[type] || type
}

function connectLabel(item) {
  if (item.jump_name) return '跳板机 · ' + item.jump_name
  if (item.proxy_type === 'socks5') return 'Socks5 代理'
  if (item.proxy_type === 'http') return 'HTTP 代理'
  return '直连'
}

// 树里空间有限，认证方式与连接方式放到悬停提示里。
function hostTooltip(item) {
  if (!item) return ''
  return `${item.username}@${item.host}:${item.port} · ${authLabel(item.auth_type)} · ${connectLabel(item)}`
}

// ---------------------------------------------------------------- 右键菜单

function openCtx(event, host) {
  // 右键即选中：与左键点选保持一致，菜单动作不会作用在别的选中项上。
  if (host) selectHost(host)
  ctx.host = host
  ctx.x = Math.max(8, Math.min(event.clientX, window.innerWidth - 196))
  ctx.y = Math.max(8, Math.min(event.clientY, window.innerHeight - 208))
  ctx.open = true
  window.removeEventListener('keydown', onKeydown)
  window.addEventListener('keydown', onKeydown)
}

// FolderNode 内部的主机行把事件透传上来，统一走同一个菜单。
function onHostCtx(payload) {
  openCtx(payload.event, payload.host)
}

function closeCtx() {
  ctx.open = false
  ctx.host = null
  window.removeEventListener('keydown', onKeydown)
}

function onKeydown(event) {
  if (event.key !== 'Escape') return
  ctx.open = false
  folderCtx.open = false
  window.removeEventListener('keydown', onKeydown)
}

// FolderNode 内部的文件夹行把右键事件透传上来。
function openFolderCtx(payload) {
  if (!payload || !payload.node) return
  // 右键即选中：与左键点选保持一致，后续菜单动作（新建子文件夹 / 重命名 / 删除）目标明确。
  selectFolder(payload.node.id)
  if (!isAdmin.value) return
  folderCtx.node = payload.node
  folderCtx.x = Math.max(8, Math.min(payload.event.clientX, window.innerWidth - 196))
  folderCtx.y = Math.max(8, Math.min(payload.event.clientY, window.innerHeight - 160))
  folderCtx.open = true
  window.removeEventListener('keydown', onKeydown)
  window.addEventListener('keydown', onKeydown)
}

function closeFolderCtx() {
  folderCtx.open = false
  folderCtx.node = null
  window.removeEventListener('keydown', onKeydown)
}

// 先关菜单再执行动作，node 由闭包传入，避免菜单关闭后取不到数据。
function runFolderCtx(action) {
  const node = folderCtx.node
  folderCtx.open = false
  window.removeEventListener('keydown', onKeydown)
  if (!node) return
  action(node)
}

// 先关菜单再执行动作；保留 ctx.host 供菜单项里的闭包取用。
function runCtx(action) {
  const host = ctx.host
  ctx.open = false
  window.removeEventListener('keydown', onKeydown)
  if (!host) return
  action()
}

function copy(text) {
  if (!text) return
  navigator.clipboard.writeText(text)
  toast('已复制到剪贴板', 'success')
}

</script>
