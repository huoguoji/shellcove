<template>
  <b class="mono">{{ text }}</b>
</template>

<script setup>
// 本地时间单独成组件：每秒只让这一小块重渲染，
// 不再牵动整个布局（侧栏 + 导航 + 状态栏）跟着刷新。
import { onBeforeUnmount, onMounted, ref } from 'vue'

const text = ref(format())
let timer = 0

function format() {
  const now = new Date()
  const pad = (n) => String(n).padStart(2, '0')
  return `${pad(now.getHours())}:${pad(now.getMinutes())}:${pad(now.getSeconds())}`
}

// 对齐到整秒边界，避免长时间运行后的累积漂移。
function tick() {
  text.value = format()
  timer = window.setTimeout(tick, 1000 - (Date.now() % 1000))
}

// 页面不可见时停表，省掉后台空转。
function onVisibility() {
  if (document.hidden) {
    window.clearTimeout(timer)
    timer = 0
    return
  }
  if (!timer) tick()
}

onMounted(() => {
  tick()
  document.addEventListener('visibilitychange', onVisibility)
})

onBeforeUnmount(() => {
  if (timer) window.clearTimeout(timer)
  document.removeEventListener('visibilitychange', onVisibility)
})
</script>
