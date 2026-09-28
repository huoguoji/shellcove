<template>
  <div class="cmd-page">
    <!-- 上：命令输入框 -->
    <section class="card">
      <div class="card-title">
        <h3>命令输入</h3>
        <span class="muted" style="font-size: 12px">另存位置：{{ groupId ? groupTitle : '未分组' }}</span>
      </div>

      <textarea
        v-model="quick.content"
        class="cmd-input"
        placeholder="输入要执行的命令，例如：df -h"
        spellcheck="false"
        @keydown.ctrl.enter.prevent="sendQuick"
        @keydown.meta.enter.prevent="sendQuick"
      ></textarea>

      <div class="row-between wrap" style="margin-top: 12px">
        <div class="row wrap">
          <button class="btn btn-primary" :disabled="!canSend" @click="sendQuick">
            {{ sender.busy ? '发送中…' : '发送到会话' }}
          </button>
          <button class="btn" :disabled="!quick.content.trim()" @click="saveAsCommand">另存为命令</button>
          <button class="btn btn-ghost" :disabled="!quick.content.trim()" @click="quick.content = ''">清空</button>
        </div>
        <span class="muted" style="font-size: 12px">Ctrl / ⌘ + Enter 发送 · 命令会自动补回车执行</span>
      </div>

      <div v-if="history.length" class="chip-row">
        <span class="muted" style="font-size: 12px">最近发送</span>
        <button v-for="item in history" :key="item" class="chip mono" :title="item" @click="quick.content = item">{{ item }}</button>
        <button class="chip" @click="clearHistory">清除</button>
      </div>
    </section>

    <!-- 下：命令文件夹 -->
    <section class="card">
      <div class="card-title">
        <div class="row wrap">
          <h3>命令文件夹</h3>
          <nav class="breadcrumb">
            <a @click="selectGroup('')">全部</a>
            <template v-if="groupId">
              <span>/</span>
              <b>{{ groupTitle }}</b>
            </template>
          </nav>
        </div>
        <div class="row wrap">
          <input v-model="search" placeholder="搜索名称 / 内容 / 备注" style="width: 220px" @keyup.enter="load" />
          <button class="btn btn-sm" @click="load">搜索</button>
          <button v-if="isAdmin" class="btn btn-sm" @click="promptGroup(null)">新建文件夹</button>
          <button v-if="isAdmin" class="btn btn-sm btn-primary" @click="openForm(null)">新建命令</button>
        </div>
      </div>

      <!-- 根目录：以文件夹形式展示分组 -->
      <template v-if="!searching && groupId === ''">
        <div v-if="!groups.length && !items.length" class="cmd-empty-hint">
          还没有命令片段，点击右上角「新建命令」开始整理常用命令。
        </div>
        <div v-else class="folder-grid">
          <div class="folder-card" role="button" tabindex="0" @click="selectGroup('__none__')" @keyup.enter="selectGroup('__none__')">
            <span class="folder-icon">
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linejoin="round">
                <path d="M3 7a2 2 0 0 1 2-2h3.2a2 2 0 0 1 1.6.8l.8 1.1a2 2 0 0 0 1.6.8H19a2 2 0 0 1 2 2V17a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2Z" />
              </svg>
            </span>
            <span class="folder-meta">
              <span class="folder-name">未分组</span>
              <span class="folder-sub">{{ countOf(null) }} 条命令</span>
            </span>
          </div>

          <div
            v-for="group in groups"
            :key="group.id"
            class="folder-card"
            role="button"
            tabindex="0"
            @click="selectGroup(group.id)"
            @keyup.enter="selectGroup(group.id)"
          >
            <span class="folder-icon">
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linejoin="round">
                <path d="M3 7a2 2 0 0 1 2-2h3.2a2 2 0 0 1 1.6.8l.8 1.1a2 2 0 0 0 1.6.8H19a2 2 0 0 1 2 2V17a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2Z" />
              </svg>
            </span>
            <span class="folder-meta">
              <span class="folder-name">{{ group.name }}</span>
              <span class="folder-sub">{{ countOf(group.id) }} 条命令</span>
            </span>
            <span class="folder-tools">
              <button v-if="isAdmin" class="btn btn-sm btn-ghost" title="重命名文件夹" @click.stop="promptGroup(group)">改</button>
              <button v-if="isAdmin" class="btn btn-sm btn-ghost" title="删除文件夹" @click.stop="removeGroup(group)">删</button>
            </span>
          </div>
        </div>
      </template>

      <!-- 文件夹内 / 搜索结果：命令列表 -->
      <template v-else>
        <div v-if="!visibleCommands.length" class="cmd-empty-hint">
          {{ searching ? '没有匹配的命令' : '该文件夹还没有命令' }}
        </div>
        <div v-else>
          <div v-for="item in visibleCommands" :key="item.id" class="cmd-row">
            <span class="cmd-icon">
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round">
                <path d="m5 16 5-5-5-5M13 18h7" />
              </svg>
            </span>
            <div class="cmd-main">
              <div class="cmd-name">{{ item.name }}</div>
              <div class="cmd-content mono" :title="item.content">{{ item.content }}</div>
            </div>
            <span v-if="searching" class="badge">{{ groupName(item.group_id) }}</span>
            <span v-if="item.remark" class="cmd-remark">{{ item.remark }}</span>
            <div class="cmd-actions">
              <button class="btn btn-sm btn-primary" @click="openSend(item)">发送</button>
              <button class="btn btn-sm" @click="fillInput(item)">填入输入框</button>
              <button v-if="isAdmin" class="btn btn-sm btn-ghost" @click="openForm(item)">编辑</button>
              <button v-if="isAdmin" class="btn btn-sm btn-ghost" @click="removeCommand(item)">删除</button>
            </div>
          </div>
        </div>
      </template>
    </section>

    <!-- 命令表单 -->
    <div v-if="form.open" class="modal-mask" @click.self="form.open = false">
      <div class="modal">
        <div class="modal-head">
          <h3>{{ form.id ? '编辑命令' : '新建命令' }}</h3>
          <button class="btn btn-sm btn-ghost" @click="form.open = false">关闭</button>
        </div>
        <div class="modal-body">
          <label class="field"><span>名称</span><input v-model="form.data.name" placeholder="查看磁盘占用" /></label>
          <label class="field"><span>命令内容</span><textarea v-model="form.data.content" placeholder="df -h"></textarea></label>
          <div class="field-row">
            <label class="field"><span>文件夹</span>
              <select v-model="form.data.group_id">
                <option :value="null">未分组</option>
                <option v-for="group in groups" :key="group.id" :value="group.id">{{ group.name }}</option>
              </select>
            </label>
            <label class="field" style="max-width: 130px"><span>排序</span><input v-model.number="form.data.sort_order" type="number" /></label>
          </div>
          <label class="field"><span>备注</span><input v-model="form.data.remark" placeholder="用途说明" /></label>
          <p v-if="form.error" class="badge badge-danger" style="display: block; padding: 10px 12px">{{ form.error }}</p>
        </div>
        <div class="modal-foot">
          <button class="btn" @click="form.open = false">取消</button>
          <button class="btn btn-primary" :disabled="form.saving" @click="save">{{ form.saving ? '保存中…' : '保存' }}</button>
        </div>
      </div>
    </div>

    <!-- 选择会话 -->
    <div v-if="sender.open" class="modal-mask" @click.self="sender.open = false">
      <div class="modal">
        <div class="modal-head">
          <h3>选择目标会话</h3>
          <button class="btn btn-sm btn-ghost" @click="sender.open = false">关闭</button>
        </div>
        <div class="modal-body">
          <p class="muted" style="margin-top: 0">
            将发送：<span class="mono">{{ sendContent }}</span>
          </p>
          <div v-if="sender.loading" class="empty">加载中…</div>
          <div v-else-if="!sender.sessions.length" class="empty">
            没有活动会话，请先在「终端」页面建立 SSH 连接
          </div>
          <table v-else>
            <tbody>
              <tr v-for="session in sender.sessions" :key="session.id">
                <td>
                  <div>{{ session.title || session.ssh_name }}</div>
                  <div class="muted" style="font-size: 11px">
                    {{ session.username }} · {{ session.cols }}×{{ session.rows }} · {{ session.started_at }}
                  </div>
                </td>
                <td style="width: 90px; text-align: right">
                  <button class="btn btn-sm btn-primary" :disabled="sender.busy" @click="doSend(session)">发送</button>
                </td>
              </tr>
            </tbody>
          </table>
          <p v-if="sender.error" class="badge badge-danger" style="display: block; padding: 10px 12px; margin-bottom: 0">{{ sender.error }}</p>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { computed, onMounted, reactive, ref } from 'vue'
import { commandApi, sessionApi } from '../api'
import { isAdmin, toast, toastError } from '../store'

const HISTORY_KEY = 'shellcove-cmd-history'

const groups = ref([])
const items = ref([])
const groupId = ref('')
const search = ref('')
const history = ref(readHistory())

const form = reactive({ open: false, id: '', saving: false, error: '', data: blankForm() })
const sender = reactive({ open: false, loading: false, busy: false, sessions: [], error: '', commandId: '', raw: '' })
const quick = reactive({ content: '' })

const searching = computed(() => search.value.trim() !== '')
const currentGroup = computed(() => groups.value.find((item) => item.id === groupId.value) || null)
const groupTitle = computed(() => (groupId.value === '__none__' ? '未分组' : currentGroup.value ? currentGroup.value.name : ''))
const canSend = computed(() => !!quick.content.trim() && !sender.busy)
const sendContent = computed(() => sender.raw || contentOf(sender.commandId))

const visibleCommands = computed(() => {
  if (searching.value) return items.value
  if (groupId.value === '__none__') return items.value.filter((item) => !item.group_id)
  if (groupId.value) return items.value.filter((item) => item.group_id === groupId.value)
  return items.value
})

function blankForm() {
  return { group_id: null, name: '', content: '', remark: '', sort_order: 0 }
}

onMounted(load)

async function load() {
  try {
    const data = await commandApi.list(search.value)
    groups.value = data.groups || []
    items.value = data.items || []
  } catch (err) {
    toastError(err)
  }
}

function selectGroup(id) {
  if (id !== '') search.value = ''
  groupId.value = id
}

function countOf(target) {
  return items.value.filter((item) => (item.group_id || null) === target).length
}

function groupName(id) {
  if (!id) return '未分组'
  const group = groups.value.find((item) => item.id === id)
  return group ? group.name : '未分组'
}

function contentOf(id) {
  const item = items.value.find((entry) => entry.id === id)
  return item ? item.content : ''
}

// ---------------------------------------------------------------- 输入框
function fillInput(item) {
  quick.content = item.content
  toast('已填入命令输入框', 'success')
}

function sendQuick() {
  if (!canSend.value) {
    toast('请先输入命令内容', 'error')
    return
  }
  openSend(null)
}

function saveAsCommand() {
  const content = quick.content.trim()
  if (!content) return
  form.error = ''
  form.id = ''
  form.data = blankForm()
  form.data.content = content
  form.data.name = content.length > 24 ? content.slice(0, 24) : content
  form.data.group_id = groupId.value && groupId.value !== '__none__' ? groupId.value : null
  form.open = true
}

function readHistory() {
  try {
    const raw = window.localStorage.getItem(HISTORY_KEY)
    const list = raw ? JSON.parse(raw) : []
    return Array.isArray(list) ? list.slice(0, 8) : []
  } catch (err) {
    return []
  }
}

function pushHistory(command) {
  const content = (command || '').trim()
  if (!content) return
  history.value = [content, ...history.value.filter((item) => item !== content)].slice(0, 8)
  try {
    window.localStorage.setItem(HISTORY_KEY, JSON.stringify(history.value))
  } catch (err) {
    // 忽略存储异常
  }
}

function clearHistory() {
  history.value = []
  try {
    window.localStorage.removeItem(HISTORY_KEY)
  } catch (err) {
    // 忽略存储异常
  }
}

// ---------------------------------------------------------------- 命令片段
function openForm(item) {
  form.error = ''
  if (item) {
    form.id = item.id
    form.data = {
      group_id: item.group_id || null,
      name: item.name,
      content: item.content,
      remark: item.remark || '',
      sort_order: item.sort_order || 0
    }
  } else {
    form.id = ''
    form.data = blankForm()
    form.data.group_id = groupId.value && groupId.value !== '__none__' ? groupId.value : null
  }
  form.open = true
}

async function save() {
  form.error = ''
  if (!form.data.name.trim() || !form.data.content.trim()) {
    form.error = '名称与命令内容均为必填项'
    return
  }
  form.saving = true
  try {
    if (form.id) {
      await commandApi.update(form.id, form.data)
      toast('命令已更新', 'success')
    } else {
      await commandApi.create(form.data)
      toast('命令已创建', 'success')
    }
    form.open = false
    await load()
  } catch (err) {
    form.error = err.message
  } finally {
    form.saving = false
  }
}

async function removeCommand(item) {
  if (!window.confirm(`确定删除命令「${item.name}」？`)) return
  try {
    await commandApi.remove(item.id)
    toast('命令已删除', 'success')
    await load()
  } catch (err) {
    toastError(err)
  }
}

async function promptGroup(group) {
  if (group) {
    const name = window.prompt('文件夹名称', group.name)
    if (!name) return
    try {
      await commandApi.updateGroup(group.id, { name, sort_order: group.sort_order || 0 })
      toast('文件夹已更新', 'success')
      await load()
    } catch (err) {
      toastError(err)
    }
    return
  }
  const name = window.prompt('新文件夹名称')
  if (!name) return
  try {
    await commandApi.createGroup({ name, sort_order: groups.value.length })
    toast('文件夹已创建', 'success')
    await load()
  } catch (err) {
    toastError(err)
  }
}

async function removeGroup(group) {
  if (!window.confirm(`删除文件夹「${group.name}」？组内命令会变为未分组。`)) return
  try {
    await commandApi.removeGroup(group.id)
    toast('文件夹已删除', 'success')
    if (groupId.value === group.id) groupId.value = ''
    await load()
  } catch (err) {
    toastError(err)
  }
}

// ---------------------------------------------------------------- 发送
async function openSend(item) {
  if (!item && !quick.content.trim()) {
    toast('请先填写命令内容', 'error')
    return
  }
  sender.error = ''
  sender.commandId = item ? item.id : ''
  sender.raw = item ? '' : quick.content.trim()
  sender.open = true
  sender.loading = true
  try {
    const data = await sessionApi.list()
    sender.sessions = data.items || []
  } catch (err) {
    sender.error = err.message
    sender.sessions = []
  } finally {
    sender.loading = false
  }
}

async function doSend(session) {
  sender.busy = true
  sender.error = ''
  try {
    if (sender.commandId) {
      await commandApi.send(sender.commandId, session.id)
      pushHistory(contentOf(sender.commandId))
    } else {
      await commandApi.sendRaw({ session_id: session.id, content: sender.raw, name: sender.raw })
      pushHistory(sender.raw)
    }
    toast('命令已发送', 'success')
    sender.open = false
    quick.content = ''
  } catch (err) {
    sender.error = err.message
  } finally {
    sender.busy = false
  }
}
</script>
