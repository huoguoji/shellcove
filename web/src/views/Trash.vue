<template>
  <div>
    <div class="card">
      <div class="card-title">
        <h3>回收站</h3>
        <div class="row">
          <span class="badge">保留 {{ retentionDays }} 天</span>
          <span class="badge">{{ items.length }} 条记录</span>
          <button class="btn btn-sm" @click="load">刷新</button>
          <button class="btn btn-sm btn-danger" :disabled="!items.length" @click="empty">清空回收站</button>
        </div>
      </div>
      <p class="muted" style="font-size: 12px">
        删除的主机会暂存于此，超过保留期限后由后台任务自动彻底删除。恢复后主机信息与凭证保持不变。
      </p>

      <div v-if="loading" class="empty">加载中…</div>
      <div v-else-if="!items.length" class="empty">回收站为空</div>
      <table v-else>
        <thead>
          <tr>
            <th>名称</th><th>地址</th><th style="width: 100px">认证方式</th>
            <th style="width: 175px">删除时间</th><th style="width: 150px">操作人</th><th style="width: 220px"></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="item in items" :key="item.id">
            <td>
              <div>{{ item.name }}</div>
              <div class="muted" style="font-size: 11px">{{ item.remark || '-' }}</div>
            </td>
            <td class="mono" style="font-size: 12px">{{ item.username }}@{{ item.host }}:{{ item.port }}</td>
            <td><span class="badge">{{ item.auth_type === 'password' ? '密码' : '密钥' }}</span></td>
            <td class="muted" style="font-size: 12px">{{ item.deleted_at || '-' }}</td>
            <td class="muted" style="font-size: 12px">{{ item.updated_at || '-' }}</td>
            <td class="row wrap">
              <button class="btn btn-sm btn-primary" @click="restore(item)">恢复</button>
              <button class="btn btn-sm btn-danger" @click="purge(item)">彻底删除</button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

<script setup>
import { computed, onMounted, ref } from 'vue'
import { trashApi } from '../api'
import { toast, toastError } from '../store'

const items = ref([])
const loading = ref(false)
const retention = ref(0)
const retentionDays = computed(() => retention.value || 30)

onMounted(load)

async function load() {
  loading.value = true
  try {
    const data = await trashApi.list()
    items.value = data.items || []
    retention.value = data.retention_days || 0
  } catch (err) {
    toastError(err)
  } finally {
    loading.value = false
  }
}

async function restore(item) {
  if (!window.confirm(`恢复主机「${item.name}」？`)) return
  try {
    await trashApi.restore(item.id)
    toast('已恢复', 'success')
    await load()
  } catch (err) {
    toastError(err)
  }
}

async function purge(item) {
  if (!window.confirm(`彻底删除「${item.name}」？该操作不可恢复，相关凭证与授权将一并清理。`)) return
  try {
    await trashApi.purge(item.id)
    toast('已彻底删除', 'success')
    await load()
  } catch (err) {
    toastError(err)
  }
}

async function empty() {
  if (!window.confirm('清空回收站将彻底删除其中的全部主机，确定继续？')) return
  try {
    const data = await trashApi.empty()
    toast(`已清理 ${data.deleted} 条记录`, 'success')
    await load()
  } catch (err) {
    toastError(err)
  }
}
</script>
