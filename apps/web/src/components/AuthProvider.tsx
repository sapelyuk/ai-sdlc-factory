import type { ReactNode } from 'react'
import { AuthContext } from '@/lib/auth-context'
import type { AuthUser } from '@/lib/auth-context'
import { useAuthStore } from '@/store/auth'

export function AuthProvider({ children }: { children: ReactNode }) {
  const store = useAuthStore()

  const login = (token: string, user: AuthUser) => {
    store.setAuth(token, user)
  }

  const logout = () => {
    store.clearAuth()
  }

  return (
    <AuthContext.Provider value={{ token: store.token, user: store.user, login, logout }}>
      {children}
    </AuthContext.Provider>
  )
}
