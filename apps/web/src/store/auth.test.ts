import { beforeEach, describe, expect, it } from 'vitest'
import { useAuthStore } from '@/store/auth'

describe('auth store', () => {
  beforeEach(() => {
    useAuthStore.getState().clearAuth()
    localStorage.clear()
  })

  it('keeps credentials in memory without persisting them to localStorage', () => {
    useAuthStore.getState().setAuth('secret-token', {
      id: '1',
      email: 'admin@example.com',
      role: 'admin',
    })

    expect(useAuthStore.getState().token).toBe('secret-token')
    expect(localStorage.length).toBe(0)
  })
})
