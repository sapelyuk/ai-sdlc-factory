import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { fetchRoles } from '@/lib/api'
import { useAuthStore } from '@/store/auth'

describe('API authentication', () => {
  beforeEach(() => {
    useAuthStore.getState().setAuth('expired-token', {
      id: '1',
      email: 'admin@example.com',
      role: 'admin',
    })
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    useAuthStore.getState().clearAuth()
  })

  it('clears auth state when the API responds with 401', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(null, { status: 401 })))

    await expect(fetchRoles('expired-token')).rejects.toThrow('HTTP 401')
    expect(useAuthStore.getState().token).toBeNull()
    expect(useAuthStore.getState().user).toBeNull()
  })
})
