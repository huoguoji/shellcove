// 统一 HTTP 客户端：负责 JSON 编解码、错误码转换与凭证类请求的缓存控制。
const JSON_HEADERS = { 'Content-Type': 'application/json' }

export class ApiError extends Error {
  constructor(code, message, status) {
    super(message || '请求失败')
    this.code = code || 'UNKNOWN'
    this.status = status || 0
  }
}

async function request(method, path, body, options = {}) {
  const init = {
    method,
    credentials: 'include',
    cache: 'no-store',
    headers: { ...(options.headers || {}) }
  }
  if (body instanceof FormData) {
    init.body = body
  } else if (body !== undefined && body !== null) {
    init.headers = { ...init.headers, ...JSON_HEADERS }
    init.body = JSON.stringify(body)
  }

  let response
  try {
    response = await fetch(path, init)
  } catch (err) {
    throw new ApiError('NETWORK_ERROR', '网络异常，请检查连接', 0)
  }

  if (options.raw) {
    if (!response.ok) {
      throw new ApiError('DOWNLOAD_FAILED', '下载失败', response.status)
    }
    return response
  }

  const text = await response.text()
  let payload = null
  if (text) {
    try {
      payload = JSON.parse(text)
    } catch (err) {
      payload = null
    }
  }

  if (!response.ok) {
    const code = payload && payload.code ? payload.code : 'HTTP_' + response.status
    const message = payload && payload.message ? payload.message : '请求失败（' + response.status + '）'
    throw new ApiError(code, message, response.status)
  }
  return payload
}

// 分片上传需要直接拿到底层 fetch 的响应体，这里额外暴露原始请求方法。
export const raw = {
  get: (path) => request('GET', path, null, { raw: true }),
  post: (path, body) => request('POST', path, body, { raw: true })
}

export const api = {
  get: (path) => request('GET', path),
  post: (path, body) => request('POST', path, body === undefined ? {} : body),
  put: (path, body) => request('PUT', path, body === undefined ? {} : body),
  del: (path) => request('DELETE', path),
  upload: (path, formData) => request('POST', path, formData)
}

export const authApi = {
  login: (payload) => api.post('/api/auth/login', payload),
  logout: () => api.post('/api/auth/logout'),
  me: () => api.get('/api/auth/me'),
  verify: (payload) => api.post('/api/auth/verify', payload),
  changePassword: (payload) => api.post('/api/auth/change-password', payload),
  totpSetup: () => api.post('/api/auth/2fa/setup'),
  totpEnable: (code) => api.post('/api/auth/2fa/enable', { code }),
  totpDisable: (payload) => api.post('/api/auth/2fa/disable', payload),
  recoveryStatus: () => api.get('/api/auth/2fa/recovery'),
  recoveryLogin: (payload) => api.post('/api/auth/2fa/recovery', payload),
  recoveryRegenerate: (password) => api.post('/api/auth/2fa/recovery/regenerate', { password })
}

export const sshApi = {
  list: (params = {}) => api.get('/api/ssh?' + query(params)),
  get: (id) => api.get('/api/ssh/' + id),
  create: (payload) => api.post('/api/ssh', payload),
  update: (id, payload) => api.put('/api/ssh/' + id, payload),
  remove: (id) => api.del('/api/ssh/' + id),
  reveal: (id, payload) => api.post('/api/ssh/' + id + '/reveal', payload),
  connect: (id, payload) => api.post('/api/ssh/' + id + '/connect', payload),
  monitor: (id) => api.get('/api/ssh/' + id + '/monitor')
}

export const folderApi = {
  tree: () => api.get('/api/folders'),
  list: () => api.get('/api/folders/list'),
  create: (payload) => api.post('/api/folders', payload),
  update: (id, payload) => api.put('/api/folders/' + id, payload),
  remove: (id, cascade) => api.del('/api/folders/' + id + (cascade ? '?cascade=1' : ''))
}

export const trashApi = {
  list: () => api.get('/api/trash'),
  restore: (id) => api.post('/api/trash/' + id + '/restore'),
  purge: (id) => api.del('/api/trash/' + id),
  empty: () => api.del('/api/trash')
}

export const commandApi = {
  list: (search) => api.get('/api/commands' + (search ? '?search=' + encodeURIComponent(search) : '')),
  create: (payload) => api.post('/api/commands', payload),
  update: (id, payload) => api.put('/api/commands/' + id, payload),
  remove: (id) => api.del('/api/commands/' + id),
  send: (id, sessionId) => api.post('/api/commands/' + id + '/send', { session_id: sessionId }),
  sendRaw: (payload) => api.post('/api/commands/send', payload),
  createGroup: (payload) => api.post('/api/command-groups', payload),
  updateGroup: (id, payload) => api.put('/api/command-groups/' + id, payload),
  removeGroup: (id) => api.del('/api/command-groups/' + id)
}

export const userApi = {
  list: () => api.get('/api/users'),
  create: (payload) => api.post('/api/users', payload),
  update: (id, payload) => api.put('/api/users/' + id, payload),
  remove: (id) => api.del('/api/users/' + id),
  resetPassword: (id, payload) => api.post('/api/users/' + id + '/password', payload),
  setRole: (id, role) => api.post('/api/users/' + id + '/role', { role }),
  setStatus: (id, disabled) => api.post('/api/users/' + id + '/status', { disabled }),
  permissions: (id) => api.get('/api/users/' + id + '/permissions'),
  setPermissions: (id, grants) => api.put('/api/users/' + id + '/permissions', { grants })
}

export const sftpApi = {
  root: () => api.get('/api/sftp/root'),
  setRoot: (root) => api.put('/api/sftp/root', { root }),
  list: (id, path) => api.get(`/api/sftp/${id}/list?path=${encodeURIComponent(path || '')}`),
  stat: (id, path) => api.get(`/api/sftp/${id}/stat?path=${encodeURIComponent(path)}`),
  mkdir: (id, path) => api.post(`/api/sftp/${id}/mkdir`, { path }),
  rename: (id, from, to) => api.post(`/api/sftp/${id}/rename`, { from, to }),
  chmod: (id, path, mode) => api.post(`/api/sftp/${id}/chmod`, { path, mode }),
  remove: (id, path, recursive) => api.post(`/api/sftp/${id}/delete`, { path, recursive }),
  read: (id, path) => api.get(`/api/sftp/${id}/read?path=${encodeURIComponent(path)}`),
  write: (id, payload) => api.post(`/api/sftp/${id}/write`, payload),
  downloadUrl: (id, path) => `/api/sftp/${id}/download?path=${encodeURIComponent(path)}`,
  uploadSimple: (id, formData) => api.upload(`/api/sftp/${id}/upload-simple`, formData),
  initUpload: (id, payload) => api.post(`/api/sftp/${id}/upload/init`, payload),
  chunkUrl: (id, uploadId, offset) => `/api/sftp/${id}/upload/${uploadId}/chunk?offset=${offset}`,
  completeUpload: (id, uploadId) => api.post(`/api/sftp/${id}/upload/${uploadId}/complete`),
  abortUpload: (id, uploadId) => api.del(`/api/sftp/${id}/upload/${uploadId}`),
  uploads: (id) => api.get(`/api/sftp/${id}/uploads`)
}

export const auditApi = {
  logs: (params = {}) => api.get('/api/audit/logs?' + query(params)),
  actions: () => api.get('/api/audit/actions'),
  clear: (before) => api.del('/api/audit/logs' + (before ? '?before=' + encodeURIComponent(before) : '')),
  sessions: (params = {}) => api.get('/api/audit/sessions?' + query(params)),
  session: (id) => api.get('/api/audit/sessions/' + id),
  commands: (id) => api.get('/api/audit/sessions/' + id + '/commands'),
  recordingUrl: (id) => '/api/audit/sessions/' + id + '/recording',
  removeRecording: (id) => api.del('/api/audit/sessions/' + id + '/recording')
}

export const sessionApi = {
  list: () => api.get('/api/sessions'),
  close: (id) => api.post('/api/sessions/' + id + '/close')
}

export const backupApi = {
  preview: (includeAudit) => api.get('/api/backup/preview?include_audit=' + (includeAudit ? '1' : '0')),
  exportPreview: (payload) => api.post('/api/backup/export/preview', payload),
  export: (payload) => api.post('/api/backup/export', payload),
  inspect: (formData) => api.upload('/api/backup/import/inspect', formData),
  doImport: (payload) => api.post('/api/backup/import', payload),
  files: () => api.get('/api/backup/files')
}

export const settingsApi = {
  get: () => api.get('/api/settings'),
  update: (payload) => api.put('/api/settings', payload),
  systemInfo: () => api.get('/api/system/info')
}

function query(params) {
  const search = new URLSearchParams()
  Object.keys(params || {}).forEach((key) => {
    const value = params[key]
    if (value === undefined || value === null || value === '') return
    search.append(key, value)
  })
  return search.toString()
}
