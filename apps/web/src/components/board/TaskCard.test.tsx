import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { TaskCard } from '@/components/board/TaskCard'
import type { Run } from '@/lib/tasks'

const draggable = vi.hoisted(() => ({
  transform: null as { x: number; y: number } | null,
  isDragging: false,
}))

vi.mock('@dnd-kit/core', () => ({
  useDraggable: () => ({
    attributes: {},
    listeners: {},
    setNodeRef: () => undefined,
    transform: draggable.transform,
    isDragging: draggable.isDragging,
  }),
}))

const run: Run = {
  id: 'run-0001',
  name: 'SDLC kanban board',
  status: 'in_dev',
  updated_at: '2026-11-24T10:00:00Z',
}

describe('TaskCard', () => {
  it('renders the run name, the state badge and updated_at', () => {
    render(<TaskCard run={run} />)

    expect(screen.getByText('SDLC kanban board')).toBeInTheDocument()
    expect(screen.getByText('InDev')).toBeInTheDocument()

    const timestamp = screen.getByText('2026-11-24 10:00 UTC')
    expect(timestamp).toBeInTheDocument()
    expect(timestamp).toHaveAttribute('datetime', run.updated_at)
  })

  it('keeps the raw value when updated_at is not a valid date', () => {
    render(<TaskCard run={{ ...run, updated_at: 'not-a-date' }} />)

    expect(screen.getByText('not-a-date')).toBeInTheDocument()
  })

  it('offsets the card while it is being dragged', () => {
    draggable.transform = { x: 12, y: 40 }
    draggable.isDragging = true

    render(<TaskCard run={run} />)

    expect(screen.getByTestId('task-run-0001')).toHaveStyle('transform: translate3d(12px, 40px, 0)')

    draggable.transform = null
    draggable.isDragging = false
  })
})
