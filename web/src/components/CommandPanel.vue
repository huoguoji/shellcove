<template>
  <section class="cmd-pane">
    <div class="cmdp-bar">
      <span class="title">命令面板</span>
      <span class="badge" :class="targetOk ? 'badge-ok' : 'badge-warn'">{{ targetLabel }}</span>
      <span style="flex: 1"></span>
      <input v-model="search" placeholder="搜索命令" spellcheck="false" />
      <template v-if="isAdmin">
        <button class="btn btn-sm" @click="createGroup">新建文件夹</button>
        <button class="btn btn-sm btn-primary" @click="openForm(null)">新建命令</button>
      </template>
      <span v-else class="hint">只读 · 命令库由管理员维护</span>
    </div>

    <!-- 文件夹标签 + 命令名：点标签切文件夹，点命令直接下发到当前会话，右键可维护 -->
    <div class="cmdp-body" @contextmenu.prevent="openCtx($event, { type: 'blank' })">
      <div v-if="!tabs.length" class="sys-note">
        {{ keyword ? '没有匹配的命令' : '还没有常用命令，在此处右键即可新建命令或文件夹' }}
      </div>

      <template v-else>
        <!-- 文件夹标签栏：整体包在一条灰底分段控件里 -->
        <div class="cmdp-tabs">
          <button
            v-for="tab in tabs"
            :key="tab.key"
            class="cmdp-tab"
            :class="{ active: tab.key === activeKey }"
            :title="tab.name"
            @click="activeKey = tab.key"
            @contextmenu.prevent.stop="openCtx($event, tab.ctx)"
          >
            <svg v-if="tab.folder" class="cmdp-tab-icon" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linejoin="round">
              <path d="M1.9 4.2a1.4 1.4 0 0 1 1.4-1.4h2.5l1.2 1.4h5.2a1.4 1.4 0 0 1 1.4 1.4v6.2a1.4 1.4 0 0 1-1.4 1.4H3.3a1.4 1.4 0 0 1-1.4-1.4z" />
            </svg>
            <svg v-else class="cmdp-tab-icon" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linejoin="round">
              <path d="M2.2 9.4 3.6 4.7a1.2 1.2 0 0 1 1.15-.85h6.5A1.2 1.2 0 0 1 12.4 4.7l1.4 4.7v3.4a1.2 1.2 0 0 1-1.2 1.2H3.4a1.2 1.2 0 0 1-1.2-1.2z" />
              <path d="M2.2 9.4h3.3l.7 1.4h3.6l.7-1.4h3.3" />
            </svg>
            <span class="cmdp-tab-name">{{ tab.name }}</span>
            <span class="cmdp-tab-count">{{ tab.count }}</span>
          </button>
        </div>

        <!-- 命令：胶囊按钮，点击即执行 -->
        <div class="cmdp-chips">
          <button
            v-for="item in activeCommands"
            :key="item.id"
            class="cmdp-cmd"
            :class="{ busy: busy === item.id }"
            :title="item.content"
            @click="send(item)"
            @contextmenu.prevent.stop="openCtx($event, { type: 'command', item })"
          >
            <svg class="cmdp-cmd-icon" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">
              <path d="m5.8 3.8 4.2 4.2-4.2 4.2" />
            </svg>
            <span class="cmdp-cmd-name">{{ item.name }}</span>
          </button>
          <span v-if="!activeCommands.length" class="cmdp-empty">
            这个文件夹还没有命令，右键标签可在此新建
          </span>
        </div>
      </template>
    </div>

    <!-- 右键菜单 -->
    <template v-if="ctx.open">
      <div class="ctx-layer" @click="closeCtx" @contextmenu.prevent="closeCtx"></div>
      <div class="ctx-menu" :style="{ left: ctx.x + 'px', top: ctx.y + 'px' }">
        <template v-if="ctx.target.type === 'command'">
          <div class="ctx-title">{{ ctx.target.item.name }}</div>
          <button @click="runCtx(() => send(ctx.target.item))">发送到当前会话</button>
          <button @click="runCtx(() => copyContent(ctx.target.item))">复制命令内容</button>
          <template v-if="isAdmin">
            <div class="ctx-sep"></div>
            <button @click="runCtx(() => openForm(ctx.target.item))">编辑命令</button>
            <button class="danger" @click="runCtx(() => removeCommand(ctx.target.item))">删除命令</button>
          </template>
        </template>
        <template v-else-if="ctx.target.type === 'group'">
          <div class="ctx-title">{{ ctx.target.group.name }}</div>
          <template v-if="isAdmin">
            <button @click="runCtx(() => openForm(null, ctx.target.group.id))">在此新建命令</button>
            <div class="ctx-sep"></div>
            <button @click="runCtx(() => renameGroup(ctx.target.group))">重命名文件夹</button>
            <button class="danger" @click="runCtx(() => removeGroup(ctx.target.group))">删除文件夹</button>
          </template>
          <button @click="runCtx(load)">刷新列表</button>
        </template>
        <template v-else-if="ctx.target.type === 'ungrouped'">
          <div class="ctx-title">未分组</div>
          <template v-if="isAdmin">
            <button @click="runCtx(() => openForm(null, null))">新建命令</button>
            <div class="ctx-sep"></div>
          </template>
          <button @click="runCtx(load)">刷新列表</button>
        </template>
        <template v-else>
          <template v-if="isAdmin">
            <button @click="runCtx(() => openForm(null, null))">新建命令</button>
            <button @click="runCtx(createGroup)">新建文件夹</button>
            <div class="ctx-sep"></div>
          </template>
          <button @click="runCtx(load)">刷新列表</button>
        </template>
      </div>
    </template>

    <!-- 新建 / 编辑命令 -->
    <div v-if="form.open" class="modal-mask" @click.self="form.open = false">
      <div class="modal">
        <div class="modal-head">
          <h3>{{ form.id ? '编辑命令' : '新建命令' }}</h3>
          <button class="btn btn-sm btn-ghost" @click="form.open = false">关闭</button>
        </div>
        <div class="modal-body">
          <label class="field"><span>名称</span><input v-model="form.data.name" placeholder="查看磁盘占用" spellcheck="false" /></label>
          <label class="field"><span>命令内容</span><textarea v-model="form.data.content" class="mono" placeholder="df -h" spellcheck="false"></textarea></label>
          <div class="field-row">
            <label class="field"><span>所属文件夹</span>
              <select v-model="form.data.group_id">
                <option :value="null">未分组</option>
                <option v-for="group in groups" :key="group.id" :value="group.id">{{ group.name }}</option>
              </select>
            </label>
            <label class="field" style="max-width: 130px"><span>排序</span><input v-model.number="form.data.sort_order" type="number" /></label>
          </div>
          <label class="field"><span>备注</span><input v-model="form.data.remark" placeholder="可选" spellcheck="false" /></label>
          <p v-if="form.error" class="badge badge-danger" style="display: block; padding: 8px 10px">{{ form.error }}</p>
        </div>
        <div class="modal-foot">
          <button class="btn" @click="form.open = false">取消</button>
          <button class="btn btn-primary" :disabled="form.saving" @click="save">{{ form.saving ? '保存中…' : '保存' }}</button>
        </div>
      </div>
    </div>
  </section>
</template>

<script setup>
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { commandApi } from '../api'
import { isAdmin, toast, toastError } from '../store'

const props = defineProps({
  sessionId: { type: String, default: '' },
  sessionTitle: { type: String, default: '' }
})

const commands = ref([])
const groups = ref([])
const search = ref('')
const busy = ref('')
const ctx = reactive({ open: false, x: 0, y: 0, target: { type: 'blank' } })
const form = reactive({ open: false, id: '', saving: false, error: '', data: blankForm() })

const targetOk = computed(() => !!props.sessionId)
const targetLabel = computed(() =>
  props.sessionId ? `目标会话 · ${props.sessionTitle || String(props.sessionId).slice(0, 8)}` : '暂无活动会话'
)

const keyword = computed(() => search.value.trim().toLowerCase())
const matchedCommands = computed(() => {
  if (!keyword.value) return commands.value
  return commands.value.filter((item) =>
    [item.name, item.content, item.remark].some((value) => (value || '').toLowerCase().includes(keyword.value))
  )
})
const ungroupedCommands = computed(() => matchedCommands.value.filter((item) => !item.group_id))
// 搜索时只保留命中命令的文件夹，未分组命中单独展示。
const visibleGroups = computed(() => {
  if (!keyword.value) return groups.value
  return groups.value.filter((group) => commandsOf(group.id).length)
})

onMounted(load)
onBeforeUnmount(() => window.removeEventListener('keydown', onKeydown))

async function load() {
  try {
    const data = await commandApi.list()
    commands.value = data.items || []
    groups.value = data.groups || []
  } catch (err) {
    toastError(err)
  }
}

// 按分组建一次索引：模板里每个分组要取 2~3 次（计数 / 列表 / 空态判断），
// 原来每次都全量 filter 一遍，命令多时搜索输入会明显迟滞。
const commandsByGroup = computed(() => {
  const map = new Map()
  matchedCommands.value.forEach((item) => {
    const key = item.group_id || ''
    const list = map.get(key)
    if (list) list.push(item)
    else map.set(key, [item])
  })
  return map
})

const EMPTY_COMMANDS = []

function commandsOf(groupId) {
  return commandsByGroup.value.get(groupId) || EMPTY_COMMANDS
}

// 文件夹即标签：一次只展示一个文件夹里的命令，未分组用空 key 表示。
const activeKey = ref('')
const tabs = computed(() => {
  const list = []
  if (ungroupedCommands.value.length) {
    list.push({ key: '', name: '未分组', count: ungroupedCommands.value.length, folder: false, ctx: { type: 'ungrouped' } })
  }
  visibleGroups.value.forEach((group) => {
    list.push({
      key: group.id,
      name: group.name,
      count: commandsOf(group.id).length,
      folder: true,
      ctx: { type: 'group', group }
    })
  })
  return list
})
const activeCommands = computed(() => commandsOf(activeKey.value))

// 当前标签被删除、或被搜索过滤掉时落到第一个可用标签，避免停在空白页。
watch(tabs, (list) => {
  if (!list.length) {
    activeKey.value = ''
    return
  }
  if (!list.some((tab) => tab.key === activeKey.value)) activeKey.value = list[0].key
})

// ---------------------------------------------------------------- 右键菜单

function openCtx(event, target) {
  ctx.target = target
  ctx.x = Math.max(8, Math.min(event.clientX, window.innerWidth - 196))
  ctx.y = Math.max(8, Math.min(event.clientY, window.innerHeight - (target.type === 'command' ? 208 : 160)))
  ctx.open = true
  window.removeEventListener('keydown', onKeydown)
  window.addEventListener('keydown', onKeydown)
}

function closeCtx() {
  ctx.open = false
  window.removeEventListener('keydown', onKeydown)
}

function onKeydown(event) {
  if (event.key === 'Escape') closeCtx()
}

function runCtx(action) {
  closeCtx()
  action()
}

// ---------------------------------------------------------------- 发送

async function send(item) {
  if (!item) return
  if (!targetOk.value) {
    toast('当前没有可发送的会话', 'error')
    return
  }
  busy.value = item.id
  try {
    await commandApi.send(item.id, props.sessionId)
    toast(`已发送：${item.name}`, 'success')
  } catch (err) {
    toastError(err)
  } finally {
    busy.value = ''
  }
}

function copyContent(item) {
  navigator.clipboard.writeText(item.content || '')
  toast('命令内容已复制', 'success')
}

// ---------------------------------------------------------------- 命令维护

function blankForm() {
  return { group_id: null, name: '', content: '', remark: '', sort_order: 0 }
}

function openForm(item, groupId) {
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
    if (groupId !== undefined && groupId !== '') form.data.group_id = groupId
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
      await commandApi.update(form.id, { ...form.data })
      toast('命令已更新', 'success')
    } else {
      await commandApi.create({ ...form.data, sort_order: Number(form.data.sort_order) || commandsOf(form.data.group_id).length })
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

async function createGroup() {
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

async function renameGroup(group) {
  const name = window.prompt('文件夹名称', group.name)
  if (!name || name === group.name) return
  try {
    await commandApi.updateGroup(group.id, { name, sort_order: group.sort_order || 0 })
    toast('文件夹已更新', 'success')
    await load()
  } catch (err) {
    toastError(err)
  }
}

async function removeGroup(group) {
  if (!window.confirm(`删除文件夹「${group.name}」？其中的命令会移到未分组。`)) return
  try {
    await commandApi.removeGroup(group.id)
    toast('文件夹已删除', 'success')
    await load()
  } catch (err) {
    toastError(err)
  }
}
</script>
