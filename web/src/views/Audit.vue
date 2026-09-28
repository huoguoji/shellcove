<template>
  <div>
    <div class="card">
      <div class="card-title">
        <h3>审计中心</h3>
        <div class="row">
          <button class="btn btn-sm" :class="{ 'btn-primary': tab === 'logs' }" @click="tab = 'logs'">操作日志</button>
          <button class="btn btn-sm" :class="{ 'btn-primary': tab === 'sessions' }" @click="tab = 'sessions'">会话录像</button>
        </div>
      </div>
    </div>

    <!-- 操作日志 -->
    <template v-if="tab === 'logs'">
      <div class="card">
        <div class="row wrap">
          <select v-model="logFilters.user_id" style="min-width: 170px">
            <option value="">全部用户</option>
            <option v-for="user in users" :key="user.id" :value="user.id">{{ user.username }}</option>
          </select>
          <select v-model="logFilters.action" style="min-width: 190px">
            <option value="">全部操作</option>
            <option v-for="action in actions" :key="action" :value="action">{{ actionLabel(action) }}</option>
          </select>
          <input v-model="logFilters.search" placeholder="关键字：目标 / 详情 / IP" style="width: 220px" @keyup.enter="loadLogs(1)" />
          <input v-model="logFilters.from" type="datetime-local" title="起始时间" />
          <input v-model="logFilters.to" type="datetime-local" title="结束时间" />
          <button class="btn btn-sm btn-primary" @click="loadLogs(1)">查询</button>
          <button class="btn btn-sm" @click="resetLogFilters">重置</button>
          <span class="spacer"></span>
          <button class="btn btn-sm btn-danger" @click="clearLogs">清空日志</button>
        </div>
      </div>

      <div class="card">
        <div v-if="logs.loading" class="empty">加载中…</div>
        <div v-else-if="!logs.items.length" class="empty">没有匹配的日志</div>
        <table v-else>
          <thead>
            <tr><th style="width: 165px">时间</th><th style="width: 130px">用户</th><th style="width: 150px">操作</th><th>目标</th><th>详情</th><th style="width: 130px">IP</th><th style="width: 70px">结果</th></tr>
          </thead>
          <tbody>
            <tr v-for="row in logs.items" :key="row.id">
              <td class="muted" style="font-size: 12px">{{ row.created_at }}</td>
              <td>{{ row.username || '-' }}</td>
              <td><span class="badge" :class="row.success ? '' : 'badge-danger'">{{ actionLabel(row.action) }}</span></td>
              <td class="mono" style="font-size: 12px">{{ row.target_name || row.target_id || '-' }}</td>
              <td class="muted" style="font-size: 12px">{{ row.detail || '-' }}</td>
              <td class="muted mono" style="font-size: 12px">{{ row.ip || '-' }}</td>
              <td><span class="badge" :class="row.success ? 'badge-success' : 'badge-danger'">{{ row.success ? '成功' : '失败' }}</span></td>
            </tr>
          </tbody>
        </table>
        <div class="pager">
          <button class="btn btn-sm" :disabled="logs.page <= 1" @click="loadLogs(logs.page - 1)">上一页</button>
          <span class="muted">第 {{ logs.page }} 页 / 共 {{ totalPages(logs) }} 页（{{ logs.total }} 条）</span>
          <button class="btn btn-sm" :disabled="logs.page >= totalPages(logs)" @click="loadLogs(logs.page + 1)">下一页</button>
        </div>
      </div>
    </template>

    <!-- 会话录像 -->
    <template v-else>
      <div class="card">
        <div class="row wrap">
          <select v-model="sessionFilters.user_id" style="min-width: 170px">
            <option value="">全部用户</option>
            <option v-for="user in users" :key="user.id" :value="user.id">{{ user.username }}</option>
          </select>
          <select v-model="sessionFilters.ssh_id" style="min-width: 180px">
            <option value="">全部主机</option>
            <option v-for="host in hosts" :key="host.id" :value="host.id">{{ host.name }}</option>
          </select>
          <select v-model="sessionFilters.status" style="min-width: 140px">
            <option value="">全部状态</option>
            <option value="active">进行中</option>
            <option value="closed">已结束</option>
          </select>
          <input v-model="sessionFilters.search" placeholder="关键字" style="width: 180px" @keyup.enter="loadSessions(1)" />
          <button class="btn btn-sm btn-primary" @click="loadSessions(1)">查询</button>
        </div>
      </div>

      <div class="card">
        <div v-if="sessions.loading" class="empty">加载中…</div>
        <div v-else-if="!sessions.items.length" class="empty">没有会话记录</div>
        <table v-else>
          <thead>
            <tr><th style="width: 150px">开始时间</th><th style="width: 120px">用户</th><th>主机 / 标题</th><th style="width: 90px">状态</th><th style="width: 150px">流量</th><th style="width: 110px">录像</th><th style="width: 210px"></th></tr>
          </thead>
          <tbody>
            <tr v-for="row in sessions.items" :key="row.id">
              <td class="muted" style="font-size: 12px">{{ row.started_at }}</td>
              <td>{{ row.username || '-' }}</td>
              <td>
                <div>{{ row.ssh_name || '-' }}</div>
                <div class="muted" style="font-size: 11px">{{ row.title || '-' }} · {{ row.client_ip }}</div>
              </td>
              <td><span class="badge" :class="row.status === 'active' ? 'badge-success' : ''">{{ row.status === 'active' ? '进行中' : '已结束' }}</span></td>
              <td class="muted mono" style="font-size: 11px">↑{{ bytesText(row.bytes_in) }} ↓{{ bytesText(row.bytes_out) }}</td>
              <td><span v-if="row.has_recording" class="badge badge-primary">{{ bytesText(row.recording_size) }}</span><span v-else class="muted" style="font-size: 12px">无</span></td>
              <td class="row wrap">
                <button class="btn btn-sm" @click="openDetail(row)">详情</button>
                <a v-if="row.has_recording" class="btn btn-sm" :href="auditApi.recordingUrl(row.id)" target="_blank" rel="noopener">下载录像</a>
                <button v-if="row.has_recording" class="btn btn-sm btn-danger" @click="removeRecording(row)">删除录像</button>
              </td>
            </tr>
          </tbody>
        </table>
        <div class="pager">
          <button class="btn btn-sm" :disabled="sessions.page <= 1" @click="loadSessions(sessions.page - 1)">上一页</button>
          <span class="muted">第 {{ sessions.page }} 页 / 共 {{ totalPages(sessions) }} 页（{{ sessions.total }} 条）</span>
          <button class="btn btn-sm" :disabled="sessions.page >= totalPages(sessions)" @click="loadSessions(sessions.page + 1)">下一页</button>
        </div>
      </div>
    </template>

    <!-- 会话详情 -->
    <div v-if="detail.open" class="modal-mask" @click.self="detail.open = false">
      <div class="modal modal-lg">
        <div class="modal-head">
          <h3>会话详情</h3>
          <button class="btn btn-sm btn-ghost" @click="detail.open = false">关闭</button>
        </div>
        <div class="modal-body">
          <div v-if="detail.loading" class="empty">加载中…</div>
          <template v-else-if="detail.session">
            <div class="grid" style="grid-template-columns: 1fr 1fr">
              <div class="kv"><span class="muted">会话 ID</span><span class="mono">{{ detail.session.id }}</span></div>
              <div class="kv"><span class="muted">用户</span><span>{{ detail.session.username }}</span></div>
              <div class="kv"><span class="muted">主机</span><span>{{ detail.session.ssh_name }}</span></div>
              <div class="kv"><span class="muted">客户端 IP</span><span class="mono">{{ detail.session.client_ip }}</span></div>
              <div class="kv"><span class="muted">开始时间</span><span>{{ detail.session.started_at }}</span></div>
              <div class="kv"><span class="muted">结束时间</span><span>{{ detail.session.ended_at || '-' }}</span></div>
              <div class="kv"><span class="muted">结束原因</span><span>{{ detail.session.close_reason || '-' }}</span></div>
              <div class="kv"><span class="muted">录像大小</span><span>{{ detail.session.has_recording ? bytesText(detail.session.recording_size) : '无' }}</span></div>
            </div>

            <h4 style="margin: 18px 0 8px">捕获的命令（{{ detail.commands.length }}）</h4>
            <div v-if="!detail.commands.length" class="muted" style="font-size: 12px">该会话未捕获到命令</div>
            <div v-else class="command-list">
              <div v-for="item in detail.commands" :key="item.id" class="command-item">
                <span class="muted" style="font-size: 11px">{{ item.created_at }}</span>
                <span class="mono">{{ item.command }}</span>
              </div>
            </div>
          </template>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { onMounted, reactive, ref } from 'vue'
import { auditApi, sshApi, userApi } from '../api'
import { toast, toastError } from '../store'

const tab = ref('logs')
const actions = ref([])
const users = ref([])
const hosts = ref([])

const logFilters = reactive({ user_id: '', action: '', search: '', from: '', to: '' })
const sessionFilters = reactive({ user_id: '', ssh_id: '', status: '', search: '' })

const logs = reactive({ items: [], total: 0, page: 1, page_size: 50, loading: false })
const sessions = reactive({ items: [], total: 0, page: 1, page_size: 50, loading: false })
const detail = reactive({ open: false, loading: false, session: null, commands: [] })

onMounted(async () => {
  try {
    const [actionData, userData, hostData] = await Promise.all([
      auditApi.actions(),
      userApi.list(),
      sshApi.list({ include_deleted: 1 })
    ])
    actions.value = actionData.items || []
    users.value = userData.items || []
    hosts.value = hostData.items || []
  } catch (err) {
    // 部分接口可能无权限，忽略。
  }
  await Promise.all([loadLogs(1), loadSessions(1)])
})

function totalPages(list) {
  if (!list.total) return 1
  return Math.max(1, Math.ceil(list.total / list.page_size))
}

function actionLabel(action) {
  const map = {
    login: '登录', login_failed: '登录失败', logout: '退出',
    password_change: '修改密码', totp_enable: '启用两步验证', totp_disable: '关闭两步验证',
    totp_failed: '两步验证失败', recovery_used: '使用恢复码',
    ssh_create: '新建主机', ssh_update: '修改主机', ssh_delete: '删除主机',
    ssh_restore: '恢复主机', ssh_purge: '彻底删除主机', ssh_reveal: '查看凭证',
    ssh_reveal_alert: '凭证告警', ssh_connect: '建立连接', ssh_disconnect: '断开连接',
    folder_create: '新建文件夹', folder_update: '修改文件夹', folder_delete: '删除文件夹',
    user_create: '新建用户', user_update: '修改用户', user_delete: '删除用户',
    user_disable: '禁用用户', user_enable: '启用用户', permission_update: '更新授权',
    command_create: '新建命令', command_update: '修改命令', command_delete: '删除命令', command_send: '发送命令',
    sftp_upload: '上传文件', sftp_download: '下载文件', sftp_delete: '删除文件',
    sftp_mkdir: '新建目录', sftp_rename: '重命名', sftp_chmod: '修改权限',
    backup_export_preview: '导出预检', backup_export: '导出备份',
    backup_import_inspect: '导入预检', backup_import: '导入备份',
    settings_update: '更新设置', trash_empty: '清空回收站',
    session_kill: '强制断开会话', audit_clear: '清理审计日志', monitor_read: '读取监控'
  }
  return map[action] || action
}

async function loadLogs(page) {
  logs.loading = true
  try {
    const data = await auditApi.logs({ ...logFilters, page, page_size: logs.page_size })
    logs.items = data.items || []
    logs.total = data.total || 0
    logs.page = data.page || page
    logs.page_size = data.page_size || logs.page_size
  } catch (err) {
    toastError(err)
  } finally {
    logs.loading = false
  }
}

function resetLogFilters() {
  logFilters.user_id = ''
  logFilters.action = ''
  logFilters.search = ''
  logFilters.from = ''
  logFilters.to = ''
  loadLogs(1)
}

async function clearLogs() {
  const before = window.prompt('仅清理该时间之前的日志（留空表示全部清空）\n格式示例：2026-01-01 00:00:00', '')
  if (before === null) return
  if (!window.confirm(before ? `确定清理 ${before} 之前的日志？` : '确定清空全部审计日志？')) return
  try {
    const data = await auditApi.clear(before || '')
    toast(`已清理 ${data.deleted} 条日志`, 'success')
    await loadLogs(1)
  } catch (err) {
    toastError(err)
  }
}

async function loadSessions(page) {
  sessions.loading = true
  try {
    const data = await auditApi.sessions({ ...sessionFilters, page, page_size: sessions.page_size })
    sessions.items = data.items || []
    sessions.total = data.total || 0
    sessions.page = data.page || page
    sessions.page_size = data.page_size || sessions.page_size
  } catch (err) {
    toastError(err)
  } finally {
    sessions.loading = false
  }
}

async function openDetail(row) {
  detail.open = true
  detail.loading = true
  detail.session = null
  detail.commands = []
  try {
    const data = await auditApi.session(row.id)
    detail.session = data.session
    detail.commands = data.commands || []
  } catch (err) {
    toastError(err)
  } finally {
    detail.loading = false
  }
}

async function removeRecording(row) {
  if (!window.confirm(`删除会话「${row.ssh_name || row.id}」的录像文件？`)) return
  try {
    await auditApi.removeRecording(row.id)
    toast('录像已删除', 'success')
    await loadSessions(sessions.page)
  } catch (err) {
    toastError(err)
  }
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
  return value.toFixed(index === 0 ? 0 : 1) + units[index]
}
</script>
