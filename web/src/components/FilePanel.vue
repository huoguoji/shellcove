<template>
  <section class="file-pane">
    <div class="panel-bar">
      <button class="icon-btn" title="上级目录" :disabled="!listing.parent" @click="up()">
        <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
          <path d="M8 12.5V3.8" /><path d="M4 7.5 8 3.5l4 4" />
        </svg>
      </button>
      <button class="icon-btn" title="刷新" :disabled="loading" @click="reload()">
        <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round">
          <path d="M13.4 8a5.4 5.4 0 1 1-1.6-3.8" /><path d="M13.5 2.4v3.2h-3.2" />
        </svg>
      </button>
      <button class="icon-btn" title="新建目录" @click="mkdir()">
        <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round">
          <path d="M2.5 4.2h4l1.3 1.6h5.7v6.4h-11z" /><path d="M8 8.6v3.2M6.4 10.2h3.2" />
        </svg>
      </button>
      <button class="icon-btn" title="上传文件" :disabled="!sshId" @click="pickFiles()">
        <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round">
          <path d="M8 11V3.5" /><path d="M4.6 6.9 8 3.5l3.4 3.4" /><path d="M3 12.5h10" />
        </svg>
      </button>

      <span class="spacer"></span>

      <span v-if="uploading" class="path">上传中 {{ uploadPercent }}%</span>
      <input
        v-model="pathInput"
        class="mono"
        style="max-width: 420px"
        spellcheck="false"
        placeholder="/"
        @keyup.enter="go(pathInput)"
      />
    </div>

    <div v-if="error" class="sys-note">{{ error }}</div>

    <template v-else>
      <div class="file-list" @dragover.prevent="onDragOver" @dragleave.prevent="onDragLeave" @drop.prevent="onDrop">
        <div class="file-row head">
          <span>名称</span><span>大小</span><span>修改时间</span><span></span>
        </div>

        <div
          v-for="item in listing.items"
          :key="item.path"
          class="file-row"
          :class="{ dir: item.is_dir }"
          @dblclick="item.is_dir ? go(item.path) : download(item)"
        >
          <span class="fname">
            <svg v-if="item.is_dir" viewBox="0 0 16 16" fill="currentColor">
              <path d="M1.8 3.6h4.3l1.4 1.7h6.7v7.1H1.8z" />
            </svg>
            <svg v-else viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.3">
              <path d="M3.4 1.9h5.2l4 4v8.2H3.4z" /><path d="M8.4 1.9v4.2h4.2" />
            </svg>
            <span :class="{ mono: !item.is_dir }" :title="item.path">{{ item.name }}</span>
            <span v-if="item.is_symlink" class="badge">链接</span>
          </span>
          <span class="fsize">{{ item.is_dir ? '-' : bytesText(item.size) }}</span>
          <span class="ftime">{{ item.mod_time }}</span>
          <span class="ftools">
            <button v-if="!item.is_dir" class="icon-btn" style="width: 20px; height: 20px" title="下载" @click.stop="download(item)">
              <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round">
                <path d="M8 3v7.6" /><path d="M4.6 7.4 8 10.8l3.4-3.4" /><path d="M3 12.8h10" />
              </svg>
            </button>
            <button v-if="item.is_dir" class="icon-btn" style="width: 20px; height: 20px" title="重命名" @click.stop="rename(item)">
              <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round">
                <path d="M10.6 2.9l2.5 2.5-7 7H3.6v-2.5z" />
              </svg>
            </button>
            <button class="icon-btn" style="width: 20px; height: 20px" title="删除" @click.stop="remove(item)">
              <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round">
                <path d="M3.5 4.6h9" /><path d="M6.4 4.6V3.2h3.2v1.4" /><path d="M5 4.6l.6 8h4.8l.6-8" />
              </svg>
            </button>
          </span>
        </div>

        <div v-if="!loading && !listing.items.length" class="sys-note">当前目录为空，可直接拖拽文件到此处上传</div>
        <div v-else-if="loading" class="sys-note">读取中…</div>
      </div>

      <div v-if="dragging" class="dropzone" style="text-align: center">松开即上传到 {{ listing.path }}</div>

      <input ref="fileInput" type="file" multiple style="display: none" @change="onPick" />
    </template>
  </section>
</template>

<script setup>
import { computed, reactive, ref, watch } from 'vue'
import { ApiError, sftpApi } from '../api'
import { toast, toastError } from '../store'

const props = defineProps({ sshId: { type: [String, Number], default: '' } })

const SIMPLE_UPLOAD_LIMIT = 8 * 1024 * 1024

const listing = reactive({ path: '', parent: '', items: [] })
const pathInput = ref('')
const loading = ref(false)
const error = ref('')
const dragging = ref(false)
const fileInput = ref(null)
const tasks = ref([])

const uploading = computed(() => tasks.value.some((task) => task.percent < 100))
const uploadPercent = computed(() => {
  if (!tasks.value.length) return 0
  const total = tasks.value.reduce((sum, task) => sum + task.percent, 0)
  return Math.round(total / tasks.value.length)
})

watch(() => props.sshId, (id) => {
  tasks.value = []
  listing.items = []
  listing.path = ''
  if (id) reload('')
  else error.value = '建立会话后可浏览远端文件'
}, { immediate: true })

async function reload(path) {
  if (!props.sshId) return
  loading.value = true
  try {
    const data = await sftpApi.list(props.sshId, path === undefined ? listing.path : path)
    listing.path = data.path || '/'
    listing.parent = data.parent || ''
    listing.items = data.items || []
    pathInput.value = listing.path
    error.value = ''
  } catch (err) {
    error.value = err && err.message ? err.message : '读取目录失败'
  } finally {
    loading.value = false
  }
}

function go(path) {
  if (path === undefined || path === null) return
  reload(path)
}

function up() {
  if (listing.parent) go(listing.parent)
}

async function mkdir() {
  const name = window.prompt('新目录名称')
  if (!name) return
  try {
    await sftpApi.mkdir(props.sshId, joinPath(listing.path, name))
    toast('目录已创建', 'success')
    await reload()
  } catch (err) {
    toastError(err)
  }
}

async function rename(item) {
  const name = window.prompt('新的名称', item.name)
  if (!name || name === item.name) return
  try {
    await sftpApi.rename(props.sshId, item.path, joinPath(parentOf(item.path), name))
    toast('已重命名', 'success')
    await reload()
  } catch (err) {
    toastError(err)
  }
}

async function remove(item) {
  const tip = item.is_dir ? `确定递归删除目录「${item.name}」及其内容？` : `确定删除文件「${item.name}」？`
  if (!window.confirm(tip)) return
  try {
    await sftpApi.remove(props.sshId, item.path, item.is_dir)
    toast('已删除', 'success')
    await reload()
  } catch (err) {
    toastError(err)
  }
}

function download(item) {
  if (!props.sshId || item.is_dir) return
  const link = document.createElement('a')
  link.href = sftpApi.downloadUrl(props.sshId, item.path)
  link.download = item.name
  document.body.appendChild(link)
  link.click()
  link.remove()
}

function pickFiles() {
  if (fileInput.value) fileInput.value.click()
}

function onPick(event) {
  const files = Array.from(event.target.files || [])
  event.target.value = ''
  upload(files)
}

function onDragOver() {
  dragging.value = true
}

function onDragLeave() {
  dragging.value = false
}

function onDrop(event) {
  dragging.value = false
  upload(Array.from(event.dataTransfer.files || []))
}

async function upload(files) {
  for (const file of files) {
    if (!file || !props.sshId) continue
    const task = reactive({ name: file.name, percent: 0 })
    tasks.value = [...tasks.value, task]
    try {
      await uploadOne(file, task)
      task.percent = 100
      toast(`${file.name} 上传完成`, 'success')
    } catch (err) {
      toastError(err)
    }
  }
  tasks.value = tasks.value.filter((task) => task.percent < 100)
  await reload()
}

async function uploadOne(file, task) {
  const dir = listing.path || '/'
  if (file.size <= SIMPLE_UPLOAD_LIMIT) {
    const form = new FormData()
    form.append('dir', dir)
    form.append('file', file)
    await sftpApi.uploadSimple(props.sshId, form)
    task.percent = 100
    return
  }

  // 大文件走分片续传，避免单次请求体过大。
  const init = await sftpApi.initUpload(props.sshId, { dir, filename: file.name, size: file.size, file_hash: '' })
  const upload = init.upload
  const chunkSize = init.chunk_size || upload.chunk_size || 2 * 1024 * 1024
  let offset = upload.received_size || 0

  while (offset < file.size) {
    const end = Math.min(offset + chunkSize, file.size)
    const info = await putChunk(upload.id, offset, file.slice(offset, end))
    offset = Number(info.received_size) || end
    task.percent = Math.min(100, Math.round((offset / file.size) * 100))
  }
  await sftpApi.completeUpload(props.sshId, upload.id)
  task.percent = 100
}

function putChunk(uploadId, offset, blob) {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest()
    xhr.open('PUT', sftpApi.chunkUrl(props.sshId, uploadId, offset))
    xhr.withCredentials = true
    xhr.setRequestHeader('Content-Type', 'application/octet-stream')
    xhr.onload = () => {
      if (xhr.status >= 200 && xhr.status < 300) {
        try {
          resolve(JSON.parse(xhr.responseText || '{}'))
        } catch (err) {
          reject(new ApiError('INVALID_RESPONSE', '服务端返回内容不合法', xhr.status))
        }
        return
      }
      let message = `分片上传失败（${xhr.status}）`
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

function joinPath(dir, name) {
  const base = dir && dir !== '/' ? dir.replace(/\/+$/, '') : ''
  return `${base}/${name}`
}

function parentOf(path) {
  const index = (path || '').lastIndexOf('/')
  return index <= 0 ? '/' : path.slice(0, index)
}

function bytesText(value) {
  const size = Number(value) || 0
  if (size >= 1024 * 1024 * 1024) return (size / 1024 / 1024 / 1024).toFixed(2) + ' GB'
  if (size >= 1024 * 1024) return (size / 1024 / 1024).toFixed(1) + ' MB'
  if (size >= 1024) return (size / 1024).toFixed(1) + ' KB'
  return size + ' B'
}
</script>
