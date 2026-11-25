import type { ReactNode } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { Board } from '@/pages/Board'
import { AuthContext } from '@/lib/auth-context'

vi.mock('@dnd-kit/core', () => ({
  DndContext: ({ children }: { children?: ReactNode }) => <div>{children}</div>,
  useDraggable: () => ({
    attributes: {},
    listeners: {},
    setNodeRef: () => undefined,
    transform: null,
    isDragging: false,
  }),
  useDroppable: () => ({ setNodeRef: () => undefined, isOver: false }),
  PointerSensor: class PointerSensor {},
  useSensor: () => ({}),
  useSensors: (...sensors: unknown[]) => sensors,
}))

describe('Board page', () => {
  it('renders the page shell, the dashboard link and the board', async () => {
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })

    render(
      <MemoryRouter>
        <QueryClientProvider client={queryClient}>
          <AuthContext.Provider
            value={{
              token: 'test-token',
              user: { id: '1', email: 'admin@example.com', role: 'admin' },
              login: vi.fn(),
              logout: vi.fn(),
            }}
          >
            <Board />
          </AuthContext.Provider>
        </QueryClientProvider>
      </MemoryRouter>,
    )

    expect(screen.getByRole('heading', { name: 'SDLC Board' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Dashboard' })).toHaveAttribute('href', '/')
    expect(await screen.findByTestId('column-in_dev')).toBeInTheDocument()
  })
})
