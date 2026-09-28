<template>
  <div class="tree-node">
    <div
      class="tree-row"
      :class="{ active: active === node.id }"
      title="右键：新建子文件夹 / 重命名 / 删除"
      @click="emit('select', node.id)"
      @contextmenu.prevent="emit('folder-ctx', { node, event: $event })"
    >
      <span class="tree-caret" @click.stop="toggle">{{ open ? '▾' : '▸' }}</span>
      <svg class="tree-icon folder" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linejoin="round">
        <path d="M1.9 4.2a1.4 1.4 0 0 1 1.4-1.4h2.5l1.2 1.4h5.2a1.4 1.4 0 0 1 1.4 1.4v6.2a1.4 1.4 0 0 1-1.4 1.4H3.3a1.4 1.4 0 0 1-1.4-1.4z" />
      </svg>
      <span class="tree-label">{{ node.name }}</span>
      <span class="tree-count">{{ folderHosts.length }}</span>
      <span class="spacer"></span>
      <template v-if="isAdmin && menuOpen">
        <button class="icon-btn tree-tool" title="新建子文件夹" @click.stop="fire('create')">＋</button>
        <button class="icon-btn tree-tool" title="重命名" @click.stop="fire('rename')">改</button>
        <button class="icon-btn tree-tool" title="删除" @click.stop="fire('delete')">删</button>
      </template>
      <button v-else-if="isAdmin" class="icon-btn tree-tool" title="更多" @click.stop="menuOpen = !menuOpen">⋯</button>
    </div>

    <template v-if="open">
      <!-- 该文件夹下的主机：文件夹 — SSH 名称 的层级结构 -->
      <div
        v-for="host in folderHosts"
        :key="host.id"
        class="tree-row host-row"
        :class="{ active: selectedHost === host.id }"
        :title="titleOf(host)"
        @click="emit('host-select', host)"
        @dblclick="emit('host-connect', host)"
        @contextmenu.prevent="emit('host-ctx', { host, event: $event })"
      >
        <span class="tree-caret"></span>
        <svg class="tree-icon host" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linejoin="round">
          <rect x="2.4" y="2.6" width="11.2" height="4.4" rx="1.2" /><rect x="2.4" y="9" width="11.2" height="4.4" rx="1.2" />
          <path d="M5 4.8h.01M5 11.2h.01" stroke-linecap="round" />
        </svg>
        <span class="tree-label">{{ host.name }}</span>
        <span class="tree-host">{{ host.username }}@{{ host.host }}</span>
        <button class="icon-btn tree-go" title="连接" @click.stop="emit('host-connect', host)">
          <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
            <path d="M3.6 8h8.8" /><path d="M9 4.6 12.4 8 9 11.4" />
          </svg>
        </button>
      </div>

      <folder-node
        v-for="child in node.children || []"
        :key="child.id"
        :node="child"
        :active="active"
        :host-map="hostMap"
        :host-title="hostTitle"
        :selected-host="selectedHost"
        @select="emit('select', $event)"
        @folder-action="emit('folder-action', $event)"
        @folder-ctx="emit('folder-ctx', $event)"
        @host-select="emit('host-select', $event)"
        @host-connect="emit('host-connect', $event)"
        @host-ctx="emit('host-ctx', $event)"
      />
    </template>
  </div>
</template>

<script setup>
import { computed, ref, watch } from 'vue'
import { isAdmin } from '../store'

const props = defineProps({
  node: { type: Object, required: true },
  active: { type: String, default: '' },
  hostMap: { type: Object, default: () => ({}) },
  selectedHost: { type: String, default: '' },
  // 悬停提示由父组件统一生成，保证根节点与文件夹内的主机信息一致。
  hostTitle: { type: Function, default: null }
})
const emit = defineEmits(['select', 'folder-action', 'folder-ctx', 'host-select', 'host-connect', 'host-ctx'])

const open = ref(true)
const menuOpen = ref(false)

const folderHosts = computed(() => props.hostMap[props.node.id] || [])

function titleOf(host) {
  if (props.hostTitle) return props.hostTitle(host)
  return `${host.username}@${host.host}:${host.port}`
}

watch(() => props.active, (value) => {
  // 选中子节点时自动展开自身，便于定位。
  if (!open.value && descendantHasActive(props.node, value)) open.value = true
})

watch(() => props.selectedHost, (value) => {
  // 选中任意层级的主机时自动展开所在文件夹。
  if (!value || open.value) return
  if (subtreeHasHost(props.node, value)) open.value = true
})

function descendantHasActive(node, value) {
  if (!value) return false
  return (node.children || []).some((child) => child.id === value || descendantHasActive(child, value))
}

function subtreeHasHost(node, hostId) {
  const own = props.hostMap[node.id] || []
  if (own.some((host) => host.id === hostId)) return true
  return (node.children || []).some((child) => subtreeHasHost(child, hostId))
}

function toggle() {
  open.value = !open.value
}

function fire(action) {
  menuOpen.value = false
  emit('folder-action', { action, id: props.node.id, name: props.node.name })
}
</script>
