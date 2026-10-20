import { useAuthStore } from '@/store/auth'

export async function login(
  email: string,
  password: string,
): Promise<{ token: string; user: { id: string; email: string; role: string } }> {
  const API_BASE = import.meta.env.VITE_API_URL || '/api'
  const response = await fetch(`${API_BASE}/v1/auth/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ email, password }),
  })

  if (!response.ok) {
    const text = await response.text()
    throw new Error(text || `HTTP ${response.status}`)
  }

  return response.json()
}

export async function fetchRoles(token: string): Promise<unknown[]> {
  const API_BASE = import.meta.env.VITE_API_URL || '/api'
  const response = await fetch(`${API_BASE}/v1/registry/roles`, {
    headers: { Authorization: `Bearer ${token}` },
  })

  if (!response.ok) {
    if (response.status === 401) {
      useAuthStore.getState().clearAuth()
    }
    throw new Error(`HTTP ${response.status}`)
  }

  return response.json()
}
