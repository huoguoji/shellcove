<template>
  <div>
    <div class="card">
      <div class="card-title">
        <h3>系统设置</h3>
        <div class="row">
          <button class="btn btn-sm" @click="load">刷新</button>
          <button class="btn btn-sm btn-primary" :disabled="saving || loading" @click="save">
            {{ saving ? '保存中…' : '保存修改' }}
          </button>
        </div>
      </div>

      <div v-if="loading" class="empty">加载中…</div>
      <template v-else>
        <div class="field-row">
          <label class="field"><span>SFTP 根目录</span>
            <input v-model="form.sftp_root" placeholder="/" />
          </label>
          <label class="field" style="max-width: 220px"><span>回收站保留天数（0-3650）</span>
            <input v-model.number="form.trash_retention_days" type="number" min="0" max="3650" />
          </label>
        </div>
        <label class="check">
          <input v-model="form.force_totp_all" type="checkbox" />
          强制所有用户启用两步验证（未绑定 2FA 的用户仅能访问「我的账号」页面完成绑定）
        </label>
        <p class="muted" style="font-size: 12px">
          SFTP 根目录限制文件管理功能的可见范围，普通用户无法越出该目录；修改后立即生效。
        </p>
      </template>

      <p v-if="error" class="badge badge-danger" style="display: block; padding: 8px 10px">{{ error }}</p>
    </div>

    <div class="card">
      <div class="card-title"><h3>数据统计</h3></div>
      <div class="grid" style="grid-template-columns: repeat(auto-fit, minmax(120px, 1fr)); gap: 10px">
        <div v-for="(count, name) in counts" :key="name" class="stat">
          <div class="stat-value">{{ count }}</div>
          <div class="stat-label">{{ countLabel(name) }}</div>
        </div>
      </div>
    </div>

    <div class="card">
      <div class="card-title"><h3>运行参数（只读）</h3></div>
      <div class="grid" style="grid-template-columns: repeat(auto-fit, minmax(280px, 1fr))">
        <div class="kv"><span class="muted">应用版本</span><span>{{ snapshot.app_version }}</span></div>
        <div class="kv"><span class="muted">会话空闲超时</span><span>{{ snapshot.session_idle_minutes }} 分钟</span></div>
        <div class="kv"><span class="muted">分片上传大小</span><span>{{ bytesText(snapshot.upload_chunk_size) }}</span></div>
        <div class="kv"><span class="muted">单文件上传上限</span><span>{{ snapshot.upload_max_file_mb }} MB</span></div>
        <div class="kv"><span class="muted">单会话录像上限</span><span>{{ snapshot.recording_max_mb }} MB</span></div>
        <div class="kv"><span class="muted">凭证查看告警阈值</span><span>{{ snapshot.reveal_warn_threshold }}</span></div>
        <div class="kv"><span class="muted">安全 Cookie</span><span>{{ snapshot.secure_cookie ? '已启用' : '未启用' }}</span></div>
        <div class="kv"><span class="muted">IP 白名单</span><span class="mono">{{ (snapshot.ip_whitelist || []).join(', ') || '未配置' }}</span></div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { computed, onMounted, reactive, ref } from 'vue'
import { settingsApi } from '../api'
import { toast } from '../store'

const snapshot = ref({})
const loading = ref(false)
const saving = ref(false)
const error = ref('')
const form = reactive({ sftp_root: '', trash_retention_days: 30, force_totp_all: false })

const counts = computed(() => snapshot.value.counts || {})

const countLabels = { ssh: 'SSH 主机', trash: '回收站', users: '用户', commands: '命令片段', audit: '审计日志' }

function countLabel(name) {
  return countLabels[name] || name
}

onMounted(load)

async function load() {
  loading.value = true
  error.value = ''
  try {
    const data = await settingsApi.get()
    snapshot.value = data
    form.sftp_root = data.sftp_root || '/'
    form.trash_retention_days = data.trash_retention_days ?? 30
    form.force_totp_all = !!data.force_totp_all
  } catch (err) {
    error.value = err.message
  } finally {
    loading.value = false
  }
}

async function save() {
  error.value = ''
  if (!form.sftp_root.trim()) {
    error.value = 'SFTP 根目录不能为空'
    return
  }
  saving.value = true
  try {
    const data = await settingsApi.update({
      sftp_root: form.sftp_root.trim(),
      trash_retention_days: Number(form.trash_retention_days) || 0,
      force_totp_all: !!form.force_totp_all
    })
    snapshot.value = data
    toast('设置已保存', 'success')
  } catch (err) {
    error.value = err.message
  } finally {
    saving.value = false
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
