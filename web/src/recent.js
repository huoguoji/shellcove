// 最近连接记录：只保存在浏览器本地，用于概览页按「最后一次连接」排序主机。
// 后端未存储主机的最近登录时间，本地记录可覆盖绝大多数单机使用场景。
const KEY = 'shellcove-recent-hosts'
const MAX = 500

export function recentMap() {
  try {
    const raw = window.localStorage.getItem(KEY)
    const data = raw ? JSON.parse(raw) : null
    return data && typeof data === 'object' ? data : {}
  } catch (err) {
    return {}
  }
}

export function recentAt(id) {
  if (!id) return 0
  return recentMap()[id] || 0
}

export function markRecent(id) {
  if (!id) return
  const map = recentMap()
  map[id] = Date.now()
  try {
    const entries = Object.entries(map)
      .sort((a, b) => b[1] - a[1])
      .slice(0, MAX)
    window.localStorage.setItem(KEY, JSON.stringify(Object.fromEntries(entries)))
  } catch (err) {
    // 忽略存储异常
  }
}

export function forgetRecent(id) {
  const map = recentMap()
  if (!(id in map)) return
  delete map[id]
  try {
    window.localStorage.setItem(KEY, JSON.stringify(map))
  } catch (err) {
    // 忽略存储异常
  }
}

export function clearRecent() {
  try {
    window.localStorage.removeItem(KEY)
  } catch (err) {
    // 忽略存储异常
  }
}

// 相对时间文案：刚刚 / n 分钟前 / n 小时前 / n 天前 / 具体日期。
export function relativeTime(ts) {
  if (!ts) return '从未连接'
  const diff = Date.now() - Number(ts)
  if (!Number.isFinite(diff) || diff < 0) return '刚刚'
  const minutes = Math.floor(diff / 60000)
  if (minutes < 1) return '刚刚'
  if (minutes < 60) return minutes + ' 分钟前'
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return hours + ' 小时前'
  const days = Math.floor(hours / 24)
  if (days < 30) return days + ' 天前'
  const date = new Date(Number(ts))
  const pad = (n) => String(n).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}`
}
