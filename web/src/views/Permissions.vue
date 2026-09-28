<template>
  <div>
    <div class="card">
      <div class="card-title">
        <h3>资源授权</h3>
        <div class="row">
          <select v-model="userId" style="min-width: 220px" @change="selectUser(userId)">
            <option value="">请选择用户</option>
            <option v-for="user in users" :key="user.id" :value="user.id">
              {{ user.username }}{{ user.role === 'admin' ? '（管理员）' : '' }}
            </option>
          </select>
          <button class="btn btn-sm" :disabled="!userId" @click="load">刷新</button>
          <button class="btn btn-sm btn-primary" :disabled="!userId || saving" @click="save">
            {{ saving ? '保存中…' : '保存授权' }}
          </button>
        </div>
      </div>

      <div v-if="target && target.role === 'admin'" class="badge badge-primary" style="display: block; padding: 8px 10px">
        该用户是管理员，默认拥有全部资源权限，此处配置不会限制其访问。
      </div>
      <div v-else-if="userId" class="muted" style="font-size: 12px">
        文件夹授权会递归作用于其下所有主机；主机单独授权可覆盖同级的文件夹授权（取并集）。
      </div>
    </div>

    <template v-if="userId">
      <div class="card">
        <div class="card-title"><h3>全部资源</h3></div>
        <div class="check">
          <input id="grant-all" v-model="allGrant" type="checkbox" />
          <label for="grant-all">授予全部资源（可访问系统内所有主机，新增主机自动包含）</label>
        </div>
      </div>

      <div v-if="loading" class="card empty">加载中…</div>

      <div v-else class="grid" style="grid-template-columns: 1fr 1fr; align-items: start">
        <div class="card">
          <div class="card-title"><h3>文件夹</h3></div>
          <div v-if="!flatFolders.length" class="empty">暂无文件夹</div>
          <table v-else>
            <thead><tr><th>名称</th><th style="width: 90px">授权</th><th style="width: 80px">SFTP</th><th style="width: 80px">监控</th></tr></thead>
            <tbody>
              <tr v-for="row in flatFolders" :key="row.id">
                <td :style="{ paddingLeft: 12 + row.depth * 18 + 'px' }">{{ row.name }}</td>
                <td><input type="checkbox" :checked="has('folder', row.id)" @change="toggle('folder', row.id)" /></td>
                <td><input type="checkbox" :checked="flag('folder', row.id, 'can_sftp')" :disabled="!has('folder', row.id)" @change="toggleFlag('folder', row.id, 'can_sftp')" /></td>
                <td><input type="checkbox" :checked="flag('folder', row.id, 'can_monitor')" :disabled="!has('folder', row.id)" @change="toggleFlag('folder', row.id, 'can_monitor')" /></td>
              </tr>
            </tbody>
          </table>
        </div>

        <div class="card">
          <div class="card-title"><h3>SSH 主机</h3></div>
          <div v-if="!hosts.length" class="empty">暂无主机</div>
          <table v-else>
            <thead><tr><th>名称</th><th style="width: 90px">授权</th><th style="width: 80px">SFTP</th><th style="width: 80px">监控</th></tr></thead>
            <tbody>
              <tr v-for="host in hosts" :key="host.id">
                <td>
                  <div>{{ host.name }}</div>
                  <div class="muted mono" style="font-size: 11px">{{ host.username }}@{{ host.host }}:{{ host.port }}</div>
                </td>
                <td><input type="checkbox" :checked="has('ssh', host.id)" @change="toggle('ssh', host.id)" /></td>
                <td><input type="checkbox" :checked="flag('ssh', host.id, 'can_sftp')" :disabled="!has('ssh', host.id)" @change="toggleFlag('ssh', host.id, 'can_sftp')" /></td>
                <td><input type="checkbox" :checked="flag('ssh', host.id, 'can_monitor')" :disabled="!has('ssh', host.id)" @change="toggleFlag('ssh', host.id, 'can_monitor')" /></td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </template>
  </div>
</template>

<script setup>
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { userApi } from '../api'
import { toast, toastError } from '../store'

const route = useRoute()
const router = useRouter()

const users = ref([])
const userId = ref('')
const loading = ref(false)
const saving = ref(false)
const allGrant = ref(false)
const grants = ref({})           // key: type|id -> { can_sftp, can_monitor }
const folders = ref([])
const hosts = ref([])
const target = ref(null)

const flatFolders = computed(() => {
  const out = []
  const walk = (nodes, depth) => {
    ;(nodes || []).forEach((node) => {
      out.push({ id: node.id, name: node.name, depth })
      walk(node.children, depth + 1)
    })
  }
  walk(folders.value, 0)
  return out
})

onMounted(async () => {
  try {
    const data = await userApi.list()
    users.value = data.items || []
  } catch (err) {
    toastError(err)
  }
  if (route.query.user) {
    userId.value = String(route.query.user)
    await load()
  }
})

watch(() => route.query.user, (value) => {
  if (value && String(value) !== userId.value) {
    userId.value = String(value)
    load()
  }
})

function key(type, id) {
  return `${type}|${id}`
}

function has(type, id) {
  return Object.prototype.hasOwnProperty.call(grants.value, key(type, id))
}

function flag(type, id, field) {
  const entry = grants.value[key(type, id)]
  return !!(entry && entry[field])
}

function toggle(type, id) {
  const k = key(type, id)
  const next = { ...grants.value }
  if (next[k]) delete next[k]
  else next[k] = { can_sftp: false, can_monitor: false }
  grants.value = next
}

function toggleFlag(type, id, field) {
  const k = key(type, id)
  const entry = grants.value[k]
  if (!entry) return
  grants.value = { ...grants.value, [k]: { ...entry, [field]: !entry[field] } }
}

async function load() {
  if (!userId.value) return
  loading.value = true
  try {
    const data = await userApi.permissions(userId.value)
    folders.value = data.folders || []
    hosts.value = data.ssh || []
    target.value = users.value.find((user) => user.id === userId.value) || null

    const map = {}
    allGrant.value = false
    ;(data.grants || []).forEach((grant) => {
      if (grant.resource_type === 'all') {
        allGrant.value = true
        return
      }
      if (!grant.resource_id) return
      map[key(grant.resource_type, grant.resource_id)] = {
        can_sftp: !!grant.can_sftp,
        can_monitor: !!grant.can_monitor
      }
    })
    grants.value = map
  } catch (err) {
    toastError(err)
  } finally {
    loading.value = false
  }
}

async function save() {
  saving.value = true
  try {
    const payload = []
    if (allGrant.value) {
      payload.push({ resource_type: 'all', resource_id: null, can_sftp: true, can_monitor: true })
    }
    Object.keys(grants.value).forEach((k) => {
      const [type, id] = k.split('|')
      const entry = grants.value[k]
      payload.push({ resource_type: type, resource_id: id, can_sftp: entry.can_sftp, can_monitor: entry.can_monitor })
    })
    await userApi.setPermissions(userId.value, payload)
    toast('授权已保存', 'success')
    await load()
  } catch (err) {
    toastError(err)
  } finally {
    saving.value = false
  }
}

function selectUser(id) {
  userId.value = id
  if (id) router.replace({ query: { ...route.query, user: id } })
  else router.replace({ query: {} })
  load()
}
</script>
