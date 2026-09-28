<template>
  <router-view />
  <div class="toast-stack">
    <div v-for="item in state.toasts" :key="item.id" class="toast" :class="'toast-' + item.type">
      {{ item.message }}
    </div>
  </div>
</template>

<script setup>
import { state } from './store'
</script>
