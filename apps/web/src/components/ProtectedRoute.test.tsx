import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { ProtectedRoute } from '@/components/ProtectedRoute'
import { useAuthStore } from '@/store/auth'

vi.mock('@/store/auth', () => ({
  useAuthStore: vi.fn(),
}))

const ProtectedContent = () => <div data-testid="protected">Protected Content</div>
const LoginRoute = () => <div data-testid="login">Login Page</div>

describe('ProtectedRoute', () => {
  it('shows children when token exists', () => {
    (useAuthStore as unknown as ReturnType<typeof vi.fn>).mockImplementation((selector) =>
      selector({ token: 'some-token', user: { id: '1', email: 'test@example.com', role: 'admin' } }),
    )

    render(
      <MemoryRouter initialEntries={['/protected']}>
        <Routes>
          <Route path="/protected" element={<ProtectedRoute><ProtectedContent /></ProtectedRoute>} />
          <Route path="/login" element={<LoginRoute />} />
        </Routes>
      </MemoryRouter>,
    )

    expect(screen.getByTestId('protected')).toBeInTheDocument()
  })

  it('redirects to login when no token', () => {
    (useAuthStore as unknown as ReturnType<typeof vi.fn>).mockImplementation((selector) =>
      selector({ token: null, user: null }),
    )

    render(
      <MemoryRouter initialEntries={['/protected']}>
        <Routes>
          <Route path="/protected" element={<ProtectedRoute><ProtectedContent /></ProtectedRoute>} />
          <Route path="/login" element={<LoginRoute />} />
        </Routes>
      </MemoryRouter>,
    )

    expect(screen.getByTestId('login')).toBeInTheDocument()
    expect(screen.queryByTestId('protected')).not.toBeInTheDocument()
  })
})
