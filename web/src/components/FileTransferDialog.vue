<template>
  <div v-if="state.fileDialog.open" class="modal-mask" @click.self="close">
    <div class="modal modal-xl ft-dialog">
      <div class="modal-head">
        <h3>文件传输</h3>
        <span class="ft-head-sub">{{ hostLabel }}</span>
        <span style="flex: 1"></span>
        <button class="btn btn-sm btn-ghost" @click="close">关闭</button>
      </div>

      <div class="modal-body ft-body">
        <!-- 可传输主机 -->
        <aside class="ft-hosts">
          <div class="ft-hosts-title">可传输主机（{{ hosts.length }}）</div>
          <button
            v-for="item in hosts"
            :key="item.id"
            type="button"
            class="ft-host"
            :class="{ active: item.id === hostId }"
            :title="`${item.username}@${item.host}:${item.port}`"
            @click="selectHost(item.id)"
          >
            <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linejoin="round">
              <rect x="2.4" y="2.6" width="11.2" height="4.4" rx="1.2" />
              <rect x="2.4" y="9" width="11.2" height="4.4" rx="1.2" />
              <path d="M5 4.8h.01M5 11.2h.01" stroke-linecap="round" />
            </svg>
            <span>{{ item.name }}</span>
          </button>
          <div v-if="!hosts.length" class="muted" style="font-size: 12px; padding: 8px">暂无已连接的会话</div>
        </aside>

        <div class="ft-main">
          <!-- 路径与操作 -->
          <div class="file-toolbar">
            <button class="btn btn-sm" :disabled="!hostId" @click="go(listing.parent)">上级</button>
            <button class="btn btn-sm" :disabled="!hostId" @click="reload">刷新</button>
            <div class="crumbs" style="flex: 1">
              <template v-for="(crumb, index) in crumbs" :key="crumb.path">
                <span v-if="index" class="muted">/</span>
                <span :class="{ current: index === crumbs.length - 1 }" @click="go(crumb.path)">{{ crumb.label }}</span>
              </template>
            </div>
            <span class="badge mono">根目录 {{ listing.root || '/' }}</span>
          </div>

          <div class="file-toolbar">
            <button class="btn btn-sm" :disabled="!hostId" @click="promptMkdir">新建目录</button>
            <label class="btn btn-sm" :class="{ disabled: !hostId }">
              上传文件
              <input type="file" multiple style="display: none" :disabled="!hostId" @change="onPickFiles" />
            </label>
            <button class="btn btn-sm" :disabled="!hostId" @click="loadUploads">续传任务（{{ uploads.length }}）</button>
            <span style="flex: 1"></span>
            <span class="muted" style="font-size: 12px">{{ listing.items.length }} 个项目</span>
          </div>

          <div class="ft-scroll">
            <div
              class="dropzone"
              :class="{ active: dragging }"
              @dragover.prevent="dragging = true"
              @dragleave.prevent="dragging = false"
              @drop.prevent="onDrop"
            >
              拖拽文件到此处上传{{ hostId ? '' : '（请先选择主机）' }}
            </div>

            <!-- 上传进度 -->
            <div v-if="tasks.length" class="card">
              <div class="card-title">
                <h3>传输任务</h3>
                <button class="btn btn-sm btn-ghost" @click="tasks = tasks.filter((t) => t.status === 'uploading')">清理已完成</button>
              </div>
              <div v-for="task in tasks" :key="task.id" style="margin-bottom: 10px">
                <div class="row-between" style="font-size: 12px">
                  <span class="mono">{{ task.name }}</span>
                  <span :class="task.status === 'error' ? 'badge badge-danger' : 'muted'">
                    {{ task.status === 'error' ? task.error : task.status === 'done' ? '已完成' : task.percent.toFixed(1) + '%' }}
                  </span>
                </div>
                <div class="progress"><i :style="{ width: Math.max(2, task.percent) + '%', background: task.status === 'error' ? 'var(--danger)' : 'var(--primary)' }"></i></div>
                <div v-if="task.status === 'error'" class="row" style="margin-top: 6px">
                  <button class="btn btn-sm" @click="retryTask(task)">重试</button>
                  <button class="btn btn-sm btn-ghost" @click="tasks = tasks.filter((t) => t.id !== task.id)">移除</button>
                </div>
              </div>
            </div>

            <!-- 续传任务 -->
            <div v-if="uploads.length" class="card">
              <div class="card-title"><h3>可续传的任务</h3></div>
              <table>
                <thead><tr><th>文件</th><th>远端路径</th><th>进度</th><th style="width: 180px"></th></tr></thead>
                <tbody>
                  <tr v-for="item in uploads" :key="item.id">
                    <td class="mono">{{ item.filename }}</td>
                    <td class="mono muted" style="font-size: 12px">{{ item.remote_path }}</td>
                    <td>
                      <div class="row-between" style="font-size: 12px">
                        <span>{{ bytesText(item.received_size) }} / {{ bytesText(item.total_size) }}</span>
                        <span>{{ progressOf(item).toFixed(1) }}%</span>
                      </div>
                      <div class="progress"><i :style="{ width: Math.max(2, progressOf(item)) + '%' }"></i></div>
                    </td>
                    <td class="row">
                      <button class="btn btn-sm" @click="resumeUpload(item)">选择文件续传</button>
                      <button class="btn btn-sm btn-danger" @click="abortUpload(item)">取消</button>
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>

            <!-- 文件列表 -->
            <div class="card" style="flex: 1; min-height: 0">
              <div v-if="!hostId" class="empty">请先在终端建立 SSH 会话，再回来传输文件</div>
              <div v-else-if="loading" class="empty">正在读取目录…</div>
              <div v-else-if="!listing.items.length" class="empty">目录为空</div>
              <table v-else>
                <thead>
                  <tr>
                    <th>名称</th>
                    <th style="width: 110px">大小</th>
                    <th style="width: 100px">权限</th>
                    <th style="width: 170px">修改时间</th>
                    <th style="width: 250px"></th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="item in listing.items" :key="item.path">
                    <td>
                      <span class="file-name" @click="item.is_dir ? go(item.path) : null">
                        <span>{{ item.is_dir ? '📁' : '📄' }}</span>
                        <span :class="{ mono: !item.is_dir }">{{ item.name }}</span>
                        <span v-if="item.is_symlink" class="badge">链接</span>
                      </span>
                    </td>
                    <td class="muted">{{ item.is_dir ? '-' : bytesText(item.size) }}</td>
                    <td class="mono muted">{{ item.mode }}</td>
                    <td class="muted" style="font-size: 12px">{{ item.mod_time }}</td>
                    <td class="row wrap">
                      <button v-if="!item.is_dir" class="btn btn-sm" @click="download(item)">下载</button>
                      <button v-if="!item.is_dir" class="btn btn-sm" @click="openEditor(item)">编辑</button>
                      <button class="btn btn-sm" @click="promptChmod(item)">权限</button>
                      <button class="btn btn-sm" @click="promptRename(item)">重命名</button>
                      <button class="btn btn-sm btn-danger" @click="removeEntry(item)">删除</button>
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>

  <!-- 文本编辑（叠加在弹窗之上） -->
  <div v-if="state.fileDialog.open && editor.open" class="modal-mask" @click.self="editor.open = false">
    <div class="modal modal-lg">
      <div class="modal-head">
        <h3>编辑文件 · {{ editor.name }}</h3>
        <button class="btn btn-sm btn-ghost" @click="editor.open = false">关闭</button>
      </div>
      <div class="modal-body">
        <p v-if="editor.error" class="badge badge-danger" style="display: block; padding: 8px 10px">{{ editor.error }}</p>
        <div v-if="editor.loading" class="empty">正在读取…</div>
        <textarea v-else v-model="editor.content" style="min-height: 380px" spellcheck="false"></textarea>
      </div>
      <div class="modal-foot">
        <button class="btn btn-sm" @click="downloadPath(editor.path)">下载原始文件</button>
        <button class="btn" @click="editor.open = false">取消</button>
        <button class="btn btn-primary" :disabled="editor.saving || editor.loading" @click="saveEditor">
          {{ editor.saving ? '保存中…' : '保存' }}
        </button>
      </div>
    </div>
  </div>
</template>

<script setup>
import { computed, reactive, ref, watch } from 'vue'
import { ApiError, sftpApi, sshApi } from '../api'
import { canSftp, closeFileTransfer, state, toast, toastError } from '../store'

const SIMPLE_UPLOAD_LIMIT = 2 * 1024 * 1024

const hosts = ref([])
const hostId = ref('')
const loading = ref(false)
const dragging = ref(false)
const uploads = ref([])
const tasks = ref([])
const listing = reactive({ path: '', parent: '/', root: '/', home: '', items: [] })
const editor = reactive({ open: false, path: '', name: '', content: '', loading: false, saving: false, error: '' })

const currentHost = computed(() => hosts.value.find((item) => item.id === hostId.value) || null)
const hostLabel = computed(() =>
  currentHost.value
    ? `${currentHost.value.name} · ${currentHost.value.username}@${currentHost.value.host}`
    : '请选择左侧主机'
)

const crumbs = computed(() => {
  const current = listing.path || '/'
  const root = listing.root || '/'
  const parts = current.split('/').filter(Boolean)
  const out = [{ label: '/', path: '/' }]
  let acc = ''
  parts.forEach((part) => {
    acc += '/' + part
    out.push({ label: part, path: acc })
  })
  if (root !== '/' && !current.startsWith(root)) return out
  return out
})

watch(
  () => [state.fileDialog.open, state.tabs.length],
  async (value, previous) => {
    const open = value[0]
    if (!open) {
      editor.open = false
      return
    }
    const reopened = !previous || !previous[0]
    const wanted = state.fileDialog.hostId
    await loadHosts()
    const target = hosts.value.find((item) => item.id === wanted) || hosts.value[0]
    if (!target) {
      hostId.value = ''
      return
    }
    // 会话数量变化时若当前主机仍在线，只刷新目录，不重置选择。
    if (!reopened && target.id === hostId.value) {
      await reload()
      return
    }
    await selectHost(target.id)
  }
)

// 只有建立了 SSH 会话的主机才能传文件，避免未连接时就能浏览远端目录。
async function loadHosts() {
  const sessionIds = state.tabs.map((tab) => tab.sshId).filter(Boolean)
  if (!sessionIds.length) {
    hosts.value = []
    hostId.value = ''
    listing.path = ''
    listing.items = []
    uploads.value = []
    return
  }
  try {
    const data = await sshApi.list({})
    const allowed = new Set(sessionIds)
    hosts.value = (data.items || []).filter((host) => allowed.has(host.id) && canSftp(host.id))
  } catch (err) {
    toastError(err)
  }
}

async function selectHost(id) {
  hostId.value = id
  editor.open = false
  tasks.value = []
  try {
    await Promise.all([go(''), loadUploads()])
  } catch (err) {
    toastError(err)
  }
}

function close() {
  closeFileTransfer()
}

async function go(path) {
  if (!hostId.value) return
  loading.value = true
  try {
    const data = await sftpApi.list(hostId.value, path || '')
    listing.path = data.path
    listing.parent = data.parent
    listing.root = data.root
    listing.home = data.home
    listing.items = data.items || []
  } catch (err) {
    toastError(err)
  } finally {
    loading.value = false
  }
}

function reload() {
  return go(listing.path)
}

async function loadUploads() {
  if (!hostId.value) return
  try {
    const data = await sftpApi.uploads(hostId.value)
    uploads.value = data.items || []
  } catch (err) {
    uploads.value = []
  }
}

// ---------------------------------------------------------------- 文件操作

function download(item) {
  downloadPath(item.path)
}

function downloadPath(path) {
  if (!path || !hostId.value) return
  const link = document.createElement('a')
  link.href = sftpApi.downloadUrl(hostId.value, path)
  link.download = ''
  document.body.appendChild(link)
  link.click()
  document.body.removeChild(link)
}

async function promptMkdir() {
  const name = window.prompt('新目录名称')
  if (!name) return
  try {
    await sftpApi.mkdir(hostId.value, joinPath(listing.path, name))
    toast('目录已创建', 'success')
    await reload()
  } catch (err) {
    toastError(err)
  }
}

async function promptRename(item) {
  const name = window.prompt('新的名称', item.name)
  if (!name || name === item.name) return
  try {
    await sftpApi.rename(hostId.value, item.path, joinPath(parentOf(item.path), name))
    toast('已重命名', 'success')
    await reload()
  } catch (err) {
    toastError(err)
  }
}

async function promptChmod(item) {
  const mode = window.prompt('权限（八进制，例如 0644）', item.mode)
  if (!mode) return
  try {
    await sftpApi.chmod(hostId.value, item.path, mode)
    toast('权限已更新', 'success')
    await reload()
  } catch (err) {
    toastError(err)
  }
}

async function removeEntry(item) {
  const recursive = item.is_dir
  const tip = recursive ? `确定递归删除目录「${item.name}」及其全部内容？` : `确定删除文件「${item.name}」？`
  if (!window.confirm(tip)) return
  try {
    await sftpApi.remove(hostId.value, item.path, recursive)
    toast('已删除', 'success')
    await reload()
  } catch (err) {
    toastError(err)
  }
}

async function openEditor(item) {
  editor.open = true
  editor.path = item.path
  editor.name = item.name
  editor.content = ''
  editor.error = ''
  editor.loading = true
  try {
    const data = await sftpApi.read(hostId.value, item.path)
    editor.content = data.content || ''
  } catch (err) {
    editor.error = err.message
  } finally {
    editor.loading = false
  }
}

async function saveEditor() {
  editor.saving = true
  editor.error = ''
  try {
    await sftpApi.write(hostId.value, { path: editor.path, content: editor.content, base64: false })
    toast('文件已保存', 'success')
    editor.open = false
    await reload()
  } catch (err) {
    editor.error = err.message
  } finally {
    editor.saving = false
  }
}

// ---------------------------------------------------------------- 上传

function onPickFiles(event) {
  const files = Array.from(event.target.files || [])
  event.target.value = ''
  uploadFiles(files)
}

function onDrop(event) {
  dragging.value = false
  if (!hostId.value) {
    toast('请先选择主机', 'error')
    return
  }
  uploadFiles(Array.from(event.dataTransfer.files || []))
}

async function uploadFiles(files) {
  for (const file of files) {
    await uploadFile(file)
  }
  await Promise.all([reload(), loadUploads()])
}

async function uploadFile(file) {
  const task = reactive({ id: `${Date.now()}-${file.name}-${Math.random()}`, name: file.name, size: file.size, percent: 0, status: 'uploading', error: '', file })
  tasks.value.unshift(task)
  try {
    await performUpload(file, task, 0)
  } catch (err) {
    task.status = 'error'
    task.error = err.message
  }
}

async function performUpload(file, task, startOffset) {
  if (file.size === 0 || file.size <= SIMPLE_UPLOAD_LIMIT) {
    const form = new FormData()
    form.append('dir', listing.path || '/')
    form.append('file', file)
    await sftpApi.uploadSimple(hostId.value, form)
    task.percent = 100
    task.status = 'done'
    toast(`${file.name} 上传完成`, 'success')
    return
  }

  const init = await sftpApi.initUpload(hostId.value, {
    dir: listing.path || '/',
    filename: file.name,
    size: file.size,
    file_hash: ''
  })
  const upload = init.upload
  const chunkSize = init.chunk_size || upload.chunk_size || 2 * 1024 * 1024
  let offset = Math.max(startOffset, upload.received_size || 0)
  task.percent = (offset / file.size) * 100

  while (offset < file.size) {
    const end = Math.min(offset + chunkSize, file.size)
    const blob = file.slice(offset, end)
    const info = await putChunk(hostId.value, upload.id, offset, blob, (loaded) => {
      task.percent = Math.min(100, ((offset + loaded) / file.size) * 100)
    })
    offset = info.received_size
    task.percent = (offset / file.size) * 100
  }

  await sftpApi.completeUpload(hostId.value, upload.id)
  task.percent = 100
  task.status = 'done'
  toast(`${file.name} 上传完成`, 'success')
}

function putChunk(id, uploadId, offset, blob, onProgress) {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest()
    xhr.open('PUT', sftpApi.chunkUrl(id, uploadId, offset))
    xhr.withCredentials = true
    xhr.setRequestHeader('Content-Type', 'application/octet-stream')
    xhr.upload.onprogress = (event) => {
      if (event.lengthComputable && onProgress) onProgress(event.loaded)
    }
    xhr.onload = () => {
      if (xhr.status >= 200 && xhr.status < 300) {
        try {
          resolve(JSON.parse(xhr.responseText || '{}'))
        } catch (err) {
          reject(new ApiError('INVALID_RESPONSE', '服务端返回内容不合法', xhr.status))
        }
        return
      }
      let message = `上传失败（${xhr.status}）`
      try {
        const payload = JSON.parse(xhr.responseText || '{}')
        if (payload.message) message = payload.message
      } catch (err) {
        // 保留默认错误信息。
      }
      reject(new ApiError('CHUNK_FAILED', message, xhr.status))
    }
    xhr.onerror = () => reject(new ApiError('NETWORK_ERROR', '网络异常，分片上传失败', 0))
    xhr.send(blob)
  })
}

async function retryTask(task) {
  if (!task.file) {
    toast('请重新选择文件后上传', 'error')
    return
  }
  task.status = 'uploading'
  task.error = ''
  task.percent = 0
  try {
    await performUpload(task.file, task, 0)
    await Promise.all([reload(), loadUploads()])
  } catch (err) {
    task.status = 'error'
    task.error = err.message
  }
}

async function resumeUpload(item) {
  const input = document.createElement('input')
  input.type = 'file'
  input.onchange = async () => {
    const file = input.files && input.files[0]
    if (!file) return
    if (file.size !== item.total_size) {
      toast('所选文件大小与任务不一致，无法续传', 'error')
      return
    }
    const task = reactive({ id: `${Date.now()}-resume`, name: file.name, size: file.size, percent: progressOf(item), status: 'uploading', error: '', file })
    tasks.value.unshift(task)
    try {
      await performUpload(file, task, item.received_size)
      await Promise.all([reload(), loadUploads()])
    } catch (err) {
      task.status = 'error'
      task.error = err.message
    }
  }
  input.click()
}

async function abortUpload(item) {
  if (!window.confirm(`取消上传任务「${item.filename}」？`)) return
  try {
    await sftpApi.abortUpload(hostId.value, item.id)
    toast('任务已取消', 'success')
    await loadUploads()
  } catch (err) {
    toastError(err)
  }
}

// ---------------------------------------------------------------- 工具

function progressOf(item) {
  if (!item.total_size) return 0
  return Math.min(100, (item.received_size / item.total_size) * 100)
}

function joinPath(dir, name) {
  const base = dir || '/'
  return base === '/' ? '/' + name : base.replace(/\/+$/, '') + '/' + name
}

function parentOf(p) {
  const parts = (p || '').split('/').filter(Boolean)
  parts.pop()
  return '/' + parts.join('/')
}

function bytesText(bytes) {
  if (!bytes) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let value = bytes
  let index = 0
  while (value >= 1024 && index < units.length - 1) {
    value /= 1024
    index += 1
  }
  return value.toFixed(index === 0 ? 0 : 2) + ' ' + units[index]
}
</script>
