<template>
  <div>
    <div class="card">
      <div class="card-title">
        <h3>用户管理</h3>
        <div class="row">
          <input v-model="search" placeholder="搜索用户名 / 备注" style="width: 200px" />
          <button class="btn btn-sm" @click="load">刷新</button>
          <button class="btn btn-sm btn-primary" @click="openCreate">新建用户</button>
        </div>
      </div>

      <div v-if="loading" class="empty">加载中…</div>
      <div v-else-if="!visible.length" class="empty">没有匹配的用户</div>
      <table v-else>
        <thead>
          <tr>
            <th>用户名</th><th style="width: 90px">角色</th><th style="width: 100px">状态</th>
            <th style="width: 110px">两步验证</th><th style="width: 110px">并发上限</th>
            <th>备注</th><th style="width: 200px">最近登录</th><th style="width: 340px"></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="user in visible" :key="user.id">
            <td>
              <div>{{ user.username }}</div>
              <div class="muted mono" style="font-size: 11px">{{ user.id }}</div>
            </td>
            <td><span class="badge" :class="user.role === 'admin' ? 'badge-primary' : ''">{{ user.role === 'admin' ? '管理员' : '用户' }}</span></td>
            <td><span class="badge" :class="user.disabled ? 'badge-danger' : 'badge-success'">{{ user.disabled ? '已禁用' : '正常' }}</span></td>
            <td>
              <span class="badge" :class="user.totp_enabled ? 'badge-success' : 'badge-warn'">{{ user.totp_enabled ? '已启用' : '未启用' }}</span>
              <span v-if="user.must_change_password" class="badge badge-warn">需改密</span>
            </td>
            <td class="muted">{{ user.max_sessions || '不限' }}</td>
            <td class="muted" style="font-size: 12px">{{ user.remark || '-' }}</td>
            <td class="muted" style="font-size: 12px">{{ user.last_login_at || '-' }}</td>
            <td class="row wrap">
              <button class="btn btn-sm" @click="openEdit(user)">资料</button>
              <button class="btn btn-sm" @click="openReset(user)">重置密码</button>
              <button class="btn btn-sm" @click="goPermissions(user)">授权</button>
              <button class="btn btn-sm" @click="toggleRole(user)">{{ user.role === 'admin' ? '降为用户' : '设为管理员' }}</button>
              <button class="btn btn-sm" @click="toggleStatus(user)">{{ user.disabled ? '启用' : '禁用' }}</button>
              <button class="btn btn-sm btn-danger" @click="remove(user)">删除</button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <!-- 新建用户 -->
    <div v-if="create.open" class="modal-mask" @click.self="create.open = false">
      <div class="modal">
        <div class="modal-head">
          <h3>新建用户</h3>
          <button class="btn btn-sm btn-ghost" @click="create.open = false">关闭</button>
        </div>
        <div class="modal-body">
          <div class="field-row">
            <label class="field"><span>用户名</span><input v-model="create.data.username" placeholder="字母、数字、下划线" /></label>
            <label class="field" style="max-width: 150px"><span>角色</span>
              <select v-model="create.data.role">
                <option value="user">普通用户</option>
                <option value="admin">管理员</option>
              </select>
            </label>
          </div>
          <label class="field"><span>初始密码</span><input v-model="create.data.password" type="password" /></label>
          <div class="field-row">
            <label class="field" style="max-width: 180px"><span>并发会话上限</span><input v-model.number="create.data.max_sessions" type="number" min="0" placeholder="0 表示不限" /></label>
            <label class="field"><span>&nbsp;</span>
              <label class="check"><input v-model="create.data.must_change_password" type="checkbox" /> 首次登录必须修改密码</label>
            </label>
          </div>
          <label class="field"><span>备注</span><input v-model="create.data.remark" /></label>
          <p v-if="create.error" class="badge badge-danger" style="display: block; padding: 8px 10px">{{ create.error }}</p>
        </div>
        <div class="modal-foot">
          <button class="btn" @click="create.open = false">取消</button>
          <button class="btn btn-primary" :disabled="create.busy" @click="submitCreate">{{ create.busy ? '创建中…' : '创建' }}</button>
        </div>
      </div>
    </div>

    <!-- 编辑资料 -->
    <div v-if="edit.open" class="modal-mask" @click.self="edit.open = false">
      <div class="modal">
        <div class="modal-head">
          <h3>编辑用户 · {{ edit.data.username }}</h3>
          <button class="btn btn-sm btn-ghost" @click="edit.open = false">关闭</button>
        </div>
        <div class="modal-body">
          <label class="field"><span>备注</span><input v-model="edit.data.remark" /></label>
          <label class="field" style="max-width: 200px">
            <span>并发会话上限（0 表示不限）</span>
            <input v-model.number="edit.data.max_sessions" type="number" min="0" />
          </label>
          <p class="muted" style="font-size: 12px">下调上限后，超出数量的会话会被自动断开。</p>
          <p v-if="edit.error" class="badge badge-danger" style="display: block; padding: 8px 10px">{{ edit.error }}</p>
        </div>
        <div class="modal-foot">
          <button class="btn" @click="edit.open = false">取消</button>
          <button class="btn btn-primary" :disabled="edit.busy" @click="submitEdit">{{ edit.busy ? '保存中…' : '保存' }}</button>
        </div>
      </div>
    </div>

    <!-- 重置密码 -->
    <div v-if="reset.open" class="modal-mask" @click.self="reset.open = false">
      <div class="modal">
        <div class="modal-head">
          <h3>重置密码 · {{ reset.username }}</h3>
          <button class="btn btn-sm btn-ghost" @click="reset.open = false">关闭</button>
        </div>
        <div class="modal-body">
          <label class="field"><span>新密码</span><input v-model="reset.password" type="password" /></label>
          <label class="check"><input v-model="reset.mustChange" type="checkbox" /> 要求该用户在下次登录时修改密码</label>
          <p v-if="reset.error" class="badge badge-danger" style="display: block; padding: 8px 10px; margin-top: 10px">{{ reset.error }}</p>
        </div>
        <div class="modal-foot">
          <button class="btn" @click="reset.open = false">取消</button>
          <button class="btn btn-primary" :disabled="reset.busy" @click="submitReset">{{ reset.busy ? '提交中…' : '重置' }}</button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { computed, onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { userApi } from '../api'
import { state, toast, toastError } from '../store'

const router = useRouter()
const users = ref([])
const loading = ref(false)
const search = ref('')

const create = reactive({ open: false, busy: false, error: '', data: blankCreate() })
const edit = reactive({ open: false, busy: false, error: '', id: '', data: { username: '', remark: '', max_sessions: 0 } })
const reset = reactive({ open: false, busy: false, error: '', id: '', username: '', password: '', mustChange: true })

const visible = computed(() => {
  const keyword = search.value.trim().toLowerCase()
  if (!keyword) return users.value
  return users.value.filter((user) =>
    [user.username, user.remark].some((value) => (value || '').toLowerCase().includes(keyword))
  )
})

function blankCreate() {
  return { username: '', password: '', role: 'user', remark: '', max_sessions: 0, must_change_password: true }
}

onMounted(load)

async function load() {
  loading.value = true
  try {
    const data = await userApi.list()
    users.value = data.items || []
  } catch (err) {
    toastError(err)
  } finally {
    loading.value = false
  }
}

function openCreate() {
  create.data = blankCreate()
  create.error = ''
  create.open = true
}

async function submitCreate() {
  create.error = ''
  if (!create.data.username.trim() || !create.data.password) {
    create.error = '用户名与初始密码均为必填项'
    return
  }
  create.busy = true
  try {
    await userApi.create({ ...create.data, username: create.data.username.trim() })
    toast('用户已创建', 'success')
    create.open = false
    await load()
  } catch (err) {
    create.error = err.message
  } finally {
    create.busy = false
  }
}

function openEdit(user) {
  edit.id = user.id
  edit.data = { username: user.username, remark: user.remark || '', max_sessions: user.max_sessions || 0 }
  edit.error = ''
  edit.open = true
}

async function submitEdit() {
  edit.error = ''
  edit.busy = true
  try {
    await userApi.update(edit.id, { remark: edit.data.remark, max_sessions: edit.data.max_sessions })
    toast('资料已更新', 'success')
    edit.open = false
    await load()
  } catch (err) {
    edit.error = err.message
  } finally {
    edit.busy = false
  }
}

function openReset(user) {
  reset.id = user.id
  reset.username = user.username
  reset.password = ''
  reset.mustChange = true
  reset.error = ''
  reset.open = true
}

async function submitReset() {
  reset.error = ''
  if (!reset.password) {
    reset.error = '请输入新密码'
    return
  }
  reset.busy = true
  try {
    await userApi.resetPassword(reset.id, { password: reset.password, must_change_password: reset.mustChange })
    toast('密码已重置', 'success')
    reset.open = false
    await load()
  } catch (err) {
    reset.error = err.message
  } finally {
    reset.busy = false
  }
}

async function toggleRole(user) {
  const next = user.role === 'admin' ? 'user' : 'admin'
  const tip = next === 'admin' ? `将「${user.username}」设为管理员？` : `将「${user.username}」降为普通用户？`
  if (!window.confirm(tip)) return
  try {
    await userApi.setRole(user.id, next)
    toast('角色已更新', 'success')
    await load()
  } catch (err) {
    toastError(err)
  }
}

async function toggleStatus(user) {
  const disabled = !user.disabled
  if (disabled && user.id === state.user?.id) {
    toast('不能禁用当前登录账号', 'error')
    return
  }
  if (!window.confirm(disabled ? `禁用「${user.username}」并断开其全部会话？` : `启用「${user.username}」？`)) return
  try {
    await userApi.setStatus(user.id, disabled)
    toast('状态已更新', 'success')
    await load()
  } catch (err) {
    toastError(err)
  }
}

async function remove(user) {
  if (!window.confirm(`确定删除用户「${user.username}」？该操作不可恢复。`)) return
  try {
    await userApi.remove(user.id)
    toast('用户已删除', 'success')
    await load()
  } catch (err) {
    toastError(err)
  }
}

function goPermissions(user) {
  router.push({ name: 'permissions', query: { user: user.id } })
}
</script>
