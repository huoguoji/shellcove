<template>
  <div class="grid" style="grid-template-columns: repeat(auto-fit, minmax(360px, 1fr)); align-items: start">
    <div class="card">
      <div class="card-title"><h3>账号信息</h3></div>
      <div class="kv"><span class="muted">用户名</span><span>{{ state.user?.username }}</span></div>
      <div class="kv"><span class="muted">角色</span><span>{{ state.user?.role === 'admin' ? '管理员' : '普通用户' }}</span></div>
      <div class="kv"><span class="muted">状态</span><span>{{ state.user?.disabled ? '已禁用' : '正常' }}</span></div>
      <div class="kv"><span class="muted">两步验证</span><span>{{ state.user?.totp_enabled ? '已启用' : '未启用' }}</span></div>
      <div class="kv"><span class="muted">最近登录</span><span class="muted">{{ state.user?.last_login_at || '-' }}</span></div>
      <p v-if="state.user?.must_change_password" class="badge badge-warn" style="display: block; padding: 8px 10px; margin-top: 12px">
        当前账号被要求修改密码，请尽快完成。
      </p>
    </div>

    <div class="card">
      <div class="card-title"><h3>修改密码</h3></div>
      <label class="field"><span>当前密码</span><input v-model="pwd.old_password" type="password" autocomplete="current-password" /></label>
      <label class="field"><span>新密码</span><input v-model="pwd.new_password" type="password" autocomplete="new-password" /></label>
      <label class="field"><span>确认新密码</span><input v-model="pwd.confirm" type="password" autocomplete="new-password" /></label>
      <p v-if="pwd.error" class="badge badge-danger" style="display: block; padding: 8px 10px">{{ pwd.error }}</p>
      <button class="btn btn-primary" :disabled="pwd.saving" @click="changePassword">{{ pwd.saving ? '提交中…' : '修改密码' }}</button>
    </div>

    <div class="card">
      <div class="card-title">
        <h3>两步验证</h3>
        <span class="badge" :class="state.user?.totp_enabled ? 'badge-success' : 'badge-warn'">
          {{ state.user?.totp_enabled ? '已启用' : '未启用' }}
        </span>
      </div>

      <template v-if="!state.user?.totp_enabled">
        <p class="muted" style="font-size: 12px">
          使用任意 TOTP 应用（Google Authenticator、1Password 等）扫描二维码，然后输入 6 位动态码完成绑定。
        </p>
        <div v-if="!totp.setup" class="row">
          <button class="btn btn-primary" :disabled="totp.busy" @click="startSetup">开始配置</button>
        </div>
        <template v-else>
          <div class="row" style="align-items: flex-start; gap: 16px">
            <img :src="totp.setup.qr_code_png" alt="TOTP 二维码" style="width: 168px; height: 168px; border-radius: 10px; background: #fff; padding: 6px" />
            <div style="flex: 1; min-width: 180px">
              <div class="muted" style="font-size: 12px">若无法扫码，可手动输入密钥：</div>
              <div class="mono" style="word-break: break-all; margin: 6px 0 12px">{{ totp.setup.secret }}</div>
              <label class="field"><span>6 位动态码</span><input v-model="totp.code" maxlength="6" placeholder="000000" /></label>
              <button class="btn btn-primary" :disabled="totp.busy" @click="enableTOTP">{{ totp.busy ? '校验中…' : '启用两步验证' }}</button>
            </div>
          </div>
        </template>
      </template>

      <template v-else>
        <label class="field"><span>登录密码</span><input v-model="disable.password" type="password" /></label>
        <label class="field"><span>动态码（或恢复码）</span><input v-model="disable.code" placeholder="000000" /></label>
        <button class="btn btn-danger" :disabled="disable.busy" @click="disableTOTP">{{ disable.busy ? '处理中…' : '关闭两步验证' }}</button>
      </template>

      <p v-if="totp.error" class="badge badge-danger" style="display: block; padding: 8px 10px; margin-top: 10px">{{ totp.error }}</p>
    </div>

    <div class="card">
      <div class="card-title">
        <h3>恢复码</h3>
        <span class="badge" v-if="recovery.status">剩余 {{ recovery.status.remaining }} / {{ recovery.status.total }}</span>
      </div>
      <p class="muted" style="font-size: 12px">
        恢复码用于在无法提供动态码时登录，每个码仅可使用一次。重新生成会使旧恢复码全部失效。
      </p>
      <label class="field"><span>登录密码</span><input v-model="recovery.password" type="password" /></label>
      <div class="row">
        <button class="btn" :disabled="recovery.busy" @click="refreshStatus">刷新状态</button>
        <button class="btn btn-primary" :disabled="recovery.busy" @click="regenerate">
          {{ recovery.busy ? '生成中…' : '重新生成恢复码' }}
        </button>
      </div>
      <div v-if="recovery.codes.length" style="margin-top: 14px">
        <div class="muted" style="font-size: 12px; margin-bottom: 6px">请立即保存以下恢复码，页面关闭后不再显示：</div>
        <div class="code-grid">
          <span v-for="code in recovery.codes" :key="code" class="mono">{{ code }}</span>
        </div>
        <button class="btn btn-sm" style="margin-top: 10px" @click="copyCodes">复制全部</button>
      </div>
      <p v-if="recovery.error" class="badge badge-danger" style="display: block; padding: 8px 10px; margin-top: 10px">{{ recovery.error }}</p>
    </div>
  </div>
</template>

<script setup>
import { onMounted, reactive } from 'vue'
import { authApi } from '../api'
import { loadSession, state, toast, toastError } from '../store'

const pwd = reactive({ old_password: '', new_password: '', confirm: '', saving: false, error: '' })
const totp = reactive({ setup: null, code: '', busy: false, error: '' })
const disable = reactive({ password: '', code: '', busy: false })
const recovery = reactive({ status: null, codes: [], password: '', busy: false, error: '' })

onMounted(refreshStatus)

async function refreshStatus() {
  recovery.error = ''
  try {
    recovery.status = await authApi.recoveryStatus()
  } catch (err) {
    recovery.error = err.message
  }
}

async function changePassword() {
  pwd.error = ''
  if (!pwd.old_password || !pwd.new_password) {
    pwd.error = '请填写当前密码与新密码'
    return
  }
  if (pwd.new_password !== pwd.confirm) {
    pwd.error = '两次输入的新密码不一致'
    return
  }
  pwd.saving = true
  try {
    await authApi.changePassword({ old_password: pwd.old_password, new_password: pwd.new_password })
    pwd.old_password = ''
    pwd.new_password = ''
    pwd.confirm = ''
    toast('密码已修改', 'success')
    await loadSession()
  } catch (err) {
    pwd.error = err.message
  } finally {
    pwd.saving = false
  }
}

async function startSetup() {
  totp.error = ''
  totp.busy = true
  try {
    totp.setup = await authApi.totpSetup()
  } catch (err) {
    totp.error = err.message
  } finally {
    totp.busy = false
  }
}

async function enableTOTP() {
  totp.error = ''
  if (!/^\d{6}$/.test(totp.code.trim())) {
    totp.error = '请输入 6 位动态码'
    return
  }
  totp.busy = true
  try {
    const data = await authApi.totpEnable(totp.code.trim())
    recovery.codes = data.recovery_codes || []
    totp.setup = null
    totp.code = ''
    toast('两步验证已启用', 'success')
    await Promise.all([loadSession(), refreshStatus()])
  } catch (err) {
    totp.error = err.message
  } finally {
    totp.busy = false
  }
}

async function disableTOTP() {
  totp.error = ''
  if (!disable.password) {
    totp.error = '请输入登录密码'
    return
  }
  disable.busy = true
  try {
    await authApi.totpDisable({ password: disable.password, code: disable.code.trim() })
    disable.password = ''
    disable.code = ''
    toast('两步验证已关闭', 'success')
    await Promise.all([loadSession(), refreshStatus()])
  } catch (err) {
    totp.error = err.message
  } finally {
    disable.busy = false
  }
}

async function regenerate() {
  recovery.error = ''
  if (!state.user?.totp_enabled) {
    recovery.error = '请先启用两步验证'
    return
  }
  if (!recovery.password) {
    recovery.error = '请输入登录密码'
    return
  }
  recovery.busy = true
  try {
    const data = await authApi.recoveryRegenerate(recovery.password)
    recovery.codes = data.recovery_codes || []
    recovery.password = ''
    toast('恢复码已重新生成', 'success')
    await refreshStatus()
  } catch (err) {
    recovery.error = err.message
  } finally {
    recovery.busy = false
  }
}

async function copyCodes() {
  const text = recovery.codes.join('\n')
  try {
    await navigator.clipboard.writeText(text)
    toast('已复制到剪贴板', 'success')
  } catch (err) {
    toastError(err)
  }
}
</script>
