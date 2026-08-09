const TOKEN_KEY = 'nodex_token'

export function getToken() { return localStorage.getItem(TOKEN_KEY) }

export function setToken(t) { localStorage.setItem(TOKEN_KEY, t) }

export function clearToken() { localStorage.removeItem(TOKEN_KEY) }

async function req(method, path, body) {
  const res = await fetch(path, {
    method,
    headers: {
      'Content-Type': 'application/json',
      'X-Auth-Token': getToken() || ''
    },
    body: body ? JSON.stringify(body) : undefined
  })
  let data = null
  try { data = await res.json() } catch (e) {}
  if (res.status === 401) {
    clearToken()
    location.hash = '#/login'
    throw new Error('未登录')
  }
  if (!res.ok) {
    throw new Error((data && data.error) || `请求失败 (${res.status})`)
  }
  return data
}

export const api = {
  get: (p) => req('GET', p),
  post: (p, b) => req('POST', p, b),
  put: (p, b) => req('PUT', p, b)
}

export function fmtBytes(n) {
  if (!n) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let i = 0
  while (n >= 1024 && i < units.length - 1) { n /= 1024; i++ }
  return n.toFixed(i === 0 ? 0 : 1) + ' ' + units[i]
}
