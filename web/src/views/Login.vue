<template>
  <div class="login-page">
    <div class="login-corner">
      <ThemeSwitch />
    </div>
    <div class="card login-card">
      <div class="brand">
        <div class="brand-logo">WS</div>
        <div>
          <div class="brand-title">ShellCove 运维面板</div>
          <div class="brand-sub">轻量自托管 · 浏览器即用</div>
        </div>
      </div>

      <form @submit.prevent="submit">
        <template v-if="step === 'totp'">
          <p class="muted" style="margin-top: 0">请输入身份验证器中的 6 位动态码。</p>
          <label class="field">
            <span>动态验证码</span>
            <input v-model="totpCode" inputmode="numeric" autocomplete="one-time-code" maxlength="6" placeholder="000000" autofocus />
          </label>
        </template>

        <template v-else-if="step === 'recovery'">
          <p class="muted" style="margin-top: 0">使用一次性恢复码登录，登录后请尽快重新生成恢复码。</p>
          <label class="field">
            <span>恢复码</span>
            <input v-model="recoveryCode" autocomplete="off" placeholder="XXXX-XXXX" autofocus />
          </label>
        </template>

        <template v-else>
          <label class="field">
            <span>用户名</span>
            <input v-model="username" autocomplete="username" placeholder="admin" autofocus />
          </label>
          <label class="field">
            <span>登录密码</span>
            <input v-model="password" type="password" autocomplete="current-password" placeholder="请输入密码" />
          </label>
        </template>

        <p v-if="error" class="badge badge-danger" style="display: block; padding: 8px 10px">{{ error }}</p>

        <button class="btn btn-primary" style="width: 100%; margin-top: 6px" :disabled="loading">
          {{ loading ? '处理中…' : step === 'password' ? '登录' : '验证' }}
        </button>

        <div class="row-between" style="margin-top: 12px">
          <a v-if="step === 'password' && totpHint" @click.prevent="step = 'totp'">已启用两步验证？直接输入动态码</a>
          <span v-else></span>
          <a v-if="step !== 'password'" @click.prevent="backToPassword">返回上一步</a>
          <a v-else @click.prevent="step = 'recovery'">使用恢复码</a>
        </div>
      </form>
    </div>
  </div>
</template>

<script setup>
import { ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { authApi } from '../api'
import { loadSession, state } from '../store'
import ThemeSwitch from '../components/ThemeSwitch.vue'

const router = useRouter()
const route = useRoute()

const step = ref('password')
const username = ref('')
const password = ref('')
const totpCode = ref('')
const recoveryCode = ref('')
const loading = ref(false)
const error = ref('')
const totpHint = ref(false)

function backToPassword() {
  step.value = 'password'
  totpCode.value = ''
  recoveryCode.value = ''
  error.value = ''
}

async function submit() {
  error.value = ''
  if (!username.value.trim()) {
    error.value = '请输入用户名'
    return
  }
  if (step.value !== 'recovery' && !password.value) {
    error.value = '请输入登录密码'
    return
  }

  loading.value = true
  try {
    let data
    if (step.value === 'recovery') {
      data = await authApi.recoveryLogin({
        username: username.value.trim(),
        password: password.value,
        recovery_code: recoveryCode.value.trim()
      })
    } else {
      data = await authApi.login({
        username: username.value.trim(),
        password: password.value,
        totp_code: totpCode.value.trim()
      })
    }

    if (data && data.need_totp) {
      step.value = 'totp'
      totpHint.value = false
      error.value = data.must_setup_totp ? '管理员要求启用两步验证，登录后请立即绑定' : ''
      return
    }

    await loadSession()
    const redirect = typeof route.query.redirect === 'string' ? route.query.redirect : '/'
    if (state.mustChangePassword) {
      await router.replace({ name: 'account', query: { force: '1' } })
      return
    }
    await router.replace(redirect)
  } catch (err) {
    if (err.code === 'TOTP_REQUIRED') {
      step.value = 'totp'
      return
    }
    if (err.code === 'TOTP_INVALID' || err.code === 'AUTH_FAILED') {
      error.value = err.message
      totpHint.value = err.code === 'TOTP_REQUIRED'
      return
    }
    error.value = err.message || '登录失败'
  } finally {
    loading.value = false
  }
}
</script>
