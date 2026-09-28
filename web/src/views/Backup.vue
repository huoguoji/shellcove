<template>
  <div>
    <div class="card">
      <div class="card-title">
        <h3>备份与恢复</h3>
        <div class="row">
          <label class="check"><input v-model="includeAudit" type="checkbox" @change="loadPreview" /> 包含审计数据</label>
          <button class="btn btn-sm" @click="loadPreview">刷新概览</button>
        </div>
      </div>

      <div v-if="preview.loading" class="empty">统计中…</div>
      <template v-else-if="preview.data">
        <div class="grid" style="grid-template-columns: repeat(auto-fit, minmax(120px, 1fr)); gap: 10px">
          <div v-for="(count, name) in preview.data.counts" :key="name" class="stat">
            <div class="stat-value">{{ count }}</div>
            <div class="stat-label">{{ tableLabel(name) }}</div>
          </div>
        </div>
        <div class="row" style="margin-top: 12px">
          <span class="badge">应用版本 {{ preview.data.app_version }}</span>
          <span class="badge">格式版本 {{ preview.data.backup_format_version }}</span>
          <span class="badge mono">主密钥指纹 {{ preview.data.master_key_fingerprint }}</span>
          <span v-if="preview.data.nothing_to_export" class="badge badge-warn">暂无可导出的数据</span>
        </div>
      </template>
    </div>

    <!-- 导出 -->
    <div class="card">
      <div class="card-title"><h3>导出备份</h3></div>
      <p class="muted" style="font-size: 12px">
        导出前需要完成二次验证。备份文件使用 PBKDF2 + AES-GCM 文件级加密，请务必牢记备份密码，忘记后无法恢复。
      </p>

      <div v-if="!exportState.token" class="field-row">
        <label class="field"><span>登录密码</span><input v-model="exportState.password" type="password" /></label>
        <label class="field" style="max-width: 170px"><span>动态码（若已启用）</span><input v-model="exportState.code" maxlength="6" placeholder="000000" /></label>
        <label class="field" style="max-width: 150px"><span>&nbsp;</span>
          <button class="btn btn-primary" :disabled="exportState.busy" @click="requestExportToken">
            {{ exportState.busy ? '校验中…' : '验证并继续' }}
          </button>
        </label>
      </div>

      <template v-else>
        <div class="badge badge-success" style="display: block; padding: 8px 10px; margin-bottom: 12px">
          二次验证通过，令牌有效至 {{ exportState.expiresAt }}，请在有效期内完成导出。
        </div>
        <div class="field-row">
          <label class="field"><span>设置备份密码（至少 12 位，用于加密备份文件）</span><input v-model="exportState.backupPassword" type="password" /></label>
          <label class="field" style="max-width: 140px"><span>&nbsp;</span>
            <button class="btn btn-primary" :disabled="exportState.busy" @click="doExport">
              {{ exportState.busy ? '导出中…' : '下载备份文件' }}
            </button>
          </label>
        </div>
      </template>
      <p v-if="exportState.error" class="badge badge-danger" style="display: block; padding: 8px 10px">{{ exportState.error }}</p>
    </div>

    <!-- 导入 -->
    <div class="card">
      <div class="card-title"><h3>导入备份</h3></div>
      <p class="muted" style="font-size: 12px">
        导入前会先读取文件清单做版本预检；导入过程中若失败会自动回滚，并保留一份导入前的自动快照。
      </p>

      <div class="row">
        <label class="btn" :class="{ disabled: importState.busy }">
          选择备份文件
          <input type="file" accept=".enc,.json,application/octet-stream" style="display: none" @change="onPickBackup" />
        </label>
        <span v-if="importState.fileName" class="mono muted" style="font-size: 12px">{{ importState.fileName }}</span>
        <span v-if="importState.busy" class="muted" style="font-size: 12px">处理中…</span>
      </div>

      <template v-if="importState.inspect">
        <div class="grid" style="grid-template-columns: 1fr 1fr; margin-top: 14px">
          <div class="kv"><span class="muted">格式 / 版本</span><span>{{ importState.inspect.format }} · {{ importState.inspect.version }}</span></div>
          <div class="kv"><span class="muted">导出应用版本</span><span>{{ importState.inspect.app_version }}</span></div>
          <div class="kv"><span class="muted">导出时间</span><span>{{ importState.inspect.exported_at }}</span></div>
          <div class="kv"><span class="muted">加密算法</span><span>{{ importState.inspect.encrypted ? importState.inspect.alg + ' / ' + importState.inspect.kdf : '未加密' }}</span></div>
          <div class="kv"><span class="muted">数据量</span><span>{{ countsText(importState.inspect.counts) }}</span></div>
          <div class="kv"><span class="muted">密钥指纹一致性</span>
            <span class="badge" :class="importState.inspect.current_key_fingerprint === importState.inspect.master_key_fingerprint ? 'badge-success' : 'badge-warn'">
              {{ importState.inspect.current_key_fingerprint === importState.inspect.master_key_fingerprint ? '一致' : '不一致（需重加密）' }}
            </span>
          </div>
        </div>

        <div class="field-row" style="margin-top: 14px">
          <label class="field"><span>备份密码{{ importState.inspect.needs_backup_password ? '（必填）' : '（如未加密可留空）' }}</span>
            <input v-model="importState.backupPassword" type="password" />
          </label>
          <label class="field"><span>冲突处理策略</span>
            <select v-model="importState.strategy">
              <option value="merge">合并（merge）：相同 ID 用备份数据覆盖字段</option>
              <option value="skip">跳过（skip）：保留现有数据，忽略冲突项</option>
              <option value="overwrite">覆盖（overwrite）：删除冲突记录后写入备份数据</option>
            </select>
          </label>
        </div>
        <label class="field"><span>旧主密钥（十六进制，仅当需要重加密时填写）</span>
          <input v-model="importState.masterKey" placeholder="可选" />
        </label>
        <div class="row">
          <button class="btn btn-primary" :disabled="importState.busy" @click="doImport">
            {{ importState.busy ? '导入中…' : '开始导入' }}
          </button>
          <button class="btn" @click="resetImport">取消</button>
        </div>
      </template>

      <p v-if="importState.error" class="badge badge-danger" style="display: block; padding: 8px 10px">{{ importState.error }}</p>

      <div v-if="importState.result" style="margin-top: 14px">
        <div class="badge badge-success" style="display: block; padding: 8px 10px">
          导入完成：策略 {{ importState.result.strategy }}，凭证重加密 {{ importState.result.re_encrypted ? '已执行' : '未执行' }}
        </div>
        <div class="grid" style="grid-template-columns: 1fr 1fr; margin-top: 10px">
          <div>
            <h4 style="margin: 0 0 6px">写入</h4>
            <div v-for="(count, name) in importState.result.imported" :key="'in-' + name" class="kv">
              <span class="muted">{{ tableLabel(name) }}</span><span>{{ count }}</span>
            </div>
          </div>
          <div>
            <h4 style="margin: 0 0 6px">跳过</h4>
            <div v-for="(count, name) in importState.result.skipped" :key="'sk-' + name" class="kv">
              <span class="muted">{{ tableLabel(name) }}</span><span>{{ count }}</span>
            </div>
          </div>
        </div>
        <div v-if="importState.result.pre_import_backup" class="muted mono" style="font-size: 12px; margin-top: 8px">
          导入前快照：{{ importState.result.pre_import_backup }}
        </div>
      </div>
    </div>

    <!-- 快照文件 -->
    <div class="card">
      <div class="card-title">
        <h3>备份目录文件</h3>
        <div class="row">
          <span v-if="files.dir" class="badge mono">{{ files.dir }}</span>
          <button class="btn btn-sm" @click="loadFiles">刷新</button>
        </div>
      </div>
      <div v-if="!files.items.length" class="empty">暂无文件</div>
      <table v-else>
        <thead><tr><th>文件名</th><th style="width: 130px">大小</th><th style="width: 220px">修改时间</th></tr></thead>
        <tbody>
          <tr v-for="file in files.items" :key="file.name">
            <td class="mono">{{ file.name }}</td>
            <td class="muted">{{ bytesText(file.size) }}</td>
            <td class="muted" style="font-size: 12px">{{ file.modified }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

<script setup>
import { onMounted, reactive, ref } from 'vue'
import { backupApi, raw } from '../api'
import { toast, toastError } from '../store'

const includeAudit = ref(false)
const preview = reactive({ data: null, loading: false })
const files = reactive({ dir: '', items: [] })

const exportState = reactive({ password: '', code: '', token: '', expiresAt: '', backupPassword: '', busy: false, error: '' })
const importState = reactive({
  fileName: '', importToken: '', inspect: null, backupPassword: '', masterKey: '',
  strategy: 'merge', busy: false, error: '', result: null
})

onMounted(() => {
  loadPreview()
  loadFiles()
})

const tableLabels = {
  folders: '文件夹', ssh_infos: 'SSH 主机', permissions: '授权', users: '用户',
  command_groups: '命令分组', commands: '命令片段', audit_logs: '审计日志', sessions: '会话记录', session_commands: '会话命令'
}

function tableLabel(name) {
  return tableLabels[name] || name
}

function countsText(counts) {
  if (!counts) return '-'
  return Object.keys(counts).map((name) => `${tableLabel(name)} ${counts[name]}`).join(' · ')
}

async function loadPreview() {
  preview.loading = true
  try {
    preview.data = await backupApi.preview(includeAudit.value)
  } catch (err) {
    toastError(err)
  } finally {
    preview.loading = false
  }
}

async function loadFiles() {
  try {
    const data = await backupApi.files()
    files.dir = data.dir || ''
    files.items = data.items || []
  } catch (err) {
    files.items = []
  }
}

async function requestExportToken() {
  exportState.error = ''
  if (!exportState.password) {
    exportState.error = '请输入登录密码'
    return
  }
  exportState.busy = true
  try {
    const data = await backupApi.exportPreview({
      password: exportState.password,
      code: exportState.code.trim(),
      include_audit: includeAudit.value
    })
    exportState.token = data.export_token
    exportState.expiresAt = data.expires_at
    exportState.password = ''
    exportState.code = ''
    toast('二次验证通过', 'success')
  } catch (err) {
    exportState.error = err.message
  } finally {
    exportState.busy = false
  }
}

async function doExport() {
  exportState.error = ''
  if ((exportState.backupPassword || '').trim().length < 12) {
    exportState.error = '备份密码至少需要 12 个字符'
    return
  }
  exportState.busy = true
  try {
    const response = await raw.post('/api/backup/export', {
      export_token: exportState.token,
      backup_password: exportState.backupPassword,
      include_audit: includeAudit.value
    })
    const blob = await response.blob()
    const name = `shellcove-backup-${new Date().toISOString().replace(/[:.]/g, '-')}.json.enc`
    const url = window.URL.createObjectURL(blob)
    const link = document.createElement('a')
    link.href = url
    link.download = name
    document.body.appendChild(link)
    link.click()
    document.body.removeChild(link)
    window.URL.revokeObjectURL(url)
    toast('备份文件已开始下载', 'success')
    exportState.token = ''
    exportState.backupPassword = ''
    await loadFiles()
  } catch (err) {
    exportState.error = err.message
  } finally {
    exportState.busy = false
  }
}

async function onPickBackup(event) {
  const file = event.target.files && event.target.files[0]
  event.target.value = ''
  if (!file) return
  resetImport()
  importState.fileName = file.name
  importState.busy = true
  try {
    const form = new FormData()
    form.append('file', file)
    const data = await backupApi.inspect(form)
    importState.inspect = data.inspect
    importState.importToken = data.import_token
    if (data.inspect && !data.inspect.compatible) {
      importState.error = data.inspect.message || '该备份文件与当前版本不兼容'
    }
  } catch (err) {
    importState.error = err.message
  } finally {
    importState.busy = false
  }
}

async function doImport() {
  importState.error = ''
  if (!importState.importToken) {
    importState.error = '请先选择并预检备份文件'
    return
  }
  if (importState.inspect && importState.inspect.compatible === false) {
    importState.error = '备份文件与当前版本不兼容，已阻止导入'
    return
  }
  if (importState.strategy === 'overwrite' &&
    !window.confirm('「覆盖」策略会先删除冲突的现有记录再写入备份数据，确定继续？')) {
    return
  }
  importState.busy = true
  try {
    const data = await backupApi.doImport({
      import_token: importState.importToken,
      backup_password: importState.backupPassword,
      master_key: importState.masterKey.trim(),
      strategy: importState.strategy,
      include_audit: includeAudit.value
    })
    importState.result = data
    importState.importToken = ''
    toast('导入完成', 'success')
    await Promise.all([loadPreview(), loadFiles()])
  } catch (err) {
    importState.error = err.message
  } finally {
    importState.busy = false
  }
}

function resetImport() {
  importState.fileName = ''
  importState.importToken = ''
  importState.inspect = null
  importState.backupPassword = ''
  importState.masterKey = ''
  importState.strategy = 'merge'
  importState.error = ''
  importState.result = null
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
