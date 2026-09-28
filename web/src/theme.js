import { reactive } from 'vue'

// 主题偏好：light / dark / auto（跟随系统），持久化到 localStorage。
// 页面首屏由 index.html 中的内联脚本提前设置 data-theme，避免白屏闪烁。
const STORAGE_KEY = 'shellcove-theme'
const media = window.matchMedia('(prefers-color-scheme: dark)')

function readMode() {
  try {
    const saved = window.localStorage.getItem(STORAGE_KEY)
    if (saved === 'light' || saved === 'dark' || saved === 'auto') return saved
  } catch (err) {
    // 隐私模式或禁用存储时忽略，退回跟随系统。
  }
  return 'auto'
}

export const themeState = reactive({
  mode: readMode(),
  system: media.matches ? 'dark' : 'light'
})

export const THEME_MODES = [
  { value: 'light', label: '亮色' },
  { value: 'dark', label: '暗色' },
  { value: 'auto', label: '跟随系统' }
]

export function resolvedTheme() {
  return themeState.mode === 'auto' ? themeState.system : themeState.mode
}

export function applyTheme() {
  const theme = resolvedTheme()
  const root = document.documentElement
  root.dataset.theme = theme
  root.dataset.themeMode = themeState.mode
  root.style.colorScheme = theme
}

export function setThemeMode(mode) {
  themeState.mode = mode === 'light' || mode === 'dark' ? mode : 'auto'
  try {
    window.localStorage.setItem(STORAGE_KEY, themeState.mode)
  } catch (err) {
    // 存储不可用时仅当前会话生效。
  }
  applyTheme()
}

const TERMINAL_THEMES = {
  light: {
    background: '#ffffff',
    foreground: '#1f2328',
    cursor: '#2b7de9',
    cursorAccent: '#ffffff',
    selectionBackground: 'rgba(43, 125, 233, 0.22)',
    black: '#24292f',
    red: '#cf222e',
    green: '#116329',
    yellow: '#7d4e00',
    blue: '#0969da',
    magenta: '#8250df',
    cyan: '#1b7c83',
    white: '#6e7781',
    brightBlack: '#57606a',
    brightRed: '#a40e26',
    brightGreen: '#1a7f37',
    brightYellow: '#9a6700',
    brightBlue: '#218bff',
    brightMagenta: '#a475f9',
    brightCyan: '#3192aa',
    brightWhite: '#8c959f'
  },
  dark: {
    background: '#0e1117',
    foreground: '#d7dbe2',
    cursor: '#4f9cf9',
    cursorAccent: '#0e1117',
    selectionBackground: 'rgba(79, 156, 249, 0.28)',
    black: '#484f58',
    red: '#ff7b72',
    green: '#3fb950',
    yellow: '#d29922',
    blue: '#58a6ff',
    magenta: '#bc8cff',
    cyan: '#39c5cf',
    white: '#b1bac4',
    brightBlack: '#6e7681',
    brightRed: '#ffa198',
    brightGreen: '#56d364',
    brightYellow: '#e3b341',
    brightBlue: '#79c0ff',
    brightMagenta: '#d2a8ff',
    brightCyan: '#56d4dd',
    brightWhite: '#f0f6fc'
  }
}

// 终端配色跟随面板主题，避免白底页面里嵌着一块纯黑终端。
export function terminalTheme() {
  return TERMINAL_THEMES[resolvedTheme()] || TERMINAL_THEMES.dark
}

function onSystemChange(event) {
  themeState.system = event.matches ? 'dark' : 'light'
  if (themeState.mode === 'auto') applyTheme()
}

if (typeof media.addEventListener === 'function') media.addEventListener('change', onSystemChange)
else if (typeof media.addListener === 'function') media.addListener(onSystemChange)

applyTheme()
