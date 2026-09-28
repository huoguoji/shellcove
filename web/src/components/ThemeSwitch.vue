<template>
  <div class="theme-switch" role="group" aria-label="主题切换">
    <button
      v-for="mode in THEME_MODES"
      :key="mode.value"
      type="button"
      :class="{ active: themeState.mode === mode.value }"
      :aria-pressed="themeState.mode === mode.value"
      :title="mode.value === 'auto' ? `跟随系统（当前${resolvedTheme() === 'dark' ? '暗色' : '亮色'}）` : mode.label"
      @click="setThemeMode(mode.value)"
    >
      <svg v-if="mode.value === 'light'" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round">
        <circle cx="12" cy="12" r="4" />
        <path d="M12 2v2M12 20v2M4.93 4.93l1.41 1.41M17.66 17.66l1.41 1.41M2 12h2M20 12h2M6.34 17.66l-1.41 1.41M19.07 4.93l-1.41 1.41" />
      </svg>
      <svg v-else-if="mode.value === 'dark'" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round">
        <path d="M12 3a6 6 0 0 0 9 9 9 9 0 1 1-9-9Z" />
      </svg>
      <svg v-else viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round">
        <rect x="2" y="4" width="20" height="12" rx="2" />
        <path d="M8 20h8M12 16v4" />
      </svg>
    </button>
  </div>
</template>

<script setup>
import { THEME_MODES, resolvedTheme, setThemeMode, themeState } from '../theme'
</script>
