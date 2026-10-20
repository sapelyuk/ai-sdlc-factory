import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { Login } from '@/pages/Login'
import { useAuth } from '@/lib/auth-context'
import { useAuthStore } from '@/store/auth'

// Mock dependencies
vi.mock('@/lib/api', () => ({
  login: vi.fn(),
  fetchRoles: vi.fn(),
}))

vi.mock('@/lib/auth-context', () => ({
  useAuth: vi.fn(),
}))

vi.mock('@/store/auth', () => ({
  useAuthStore: vi.fn(),
}))

describe('Login Page', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    const mockLoginFn = vi.fn()
    vi.mocked(useAuth).mockReturnValue({
      token: null,
      user: null,
      login: mockLoginFn,
      logout: vi.fn(),
    } as ReturnType<typeof useAuth>)
    vi.mocked(useAuthStore).mockReturnValue({
      setAuth: vi.fn(),
      clearAuth: vi.fn(),
      token: null,
      user: null,
    })
  })

  it('renders login form', () => {
    render(<Login />, { wrapper: MemoryRouter })
    expect(screen.getByLabelText(/email/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/пароль/i)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /войти/i })).toBeInTheDocument()
  })

  it('shows loading state on submit', async () => {
    const { login } = await import('@/lib/api')
    vi.mocked(login).mockReturnValue(new Promise(() => {}))

    render(<Login />, { wrapper: MemoryRouter })

    const emailInput = screen.getByLabelText(/email/i)
    const passwordInput = screen.getByLabelText(/пароль/i)
    const submitButton = screen.getByRole('button', { name: /войти/i })

    fireEvent.change(emailInput, { target: { value: 'admin@example.com' } })
    fireEvent.change(passwordInput, { target: { value: 'admin' } })
    fireEvent.click(submitButton)

    expect(screen.getByRole('button', { name: /вход\.\.\./i })).toBeInTheDocument()
  })
})
