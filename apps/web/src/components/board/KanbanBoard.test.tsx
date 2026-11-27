import type { ReactNode } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, render, screen, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { DragEndEvent, DragStartEvent } from '@dnd-kit/core'
import { KanbanBoard } from '@/components/board/KanbanBoard'
import { AuthContext } from '@/lib/auth-context'
import { FIXTURE_RUNS, listRuns, recordTransition, type Run } from '@/lib/tasks'
import type { RunStatus } from '@/lib/sdlc'

const harness = vi.hoisted(() => ({
  onDragStart: undefined as ((event: DragStartEvent) => void) | undefined,
  onDragEnd: undefined as ((event: DragEndEvent) => void) | undefined,
  onDragCancel: undefined as (() => void) | undefined,
  overId: null as string | null,
  original: {
    listRuns: undefined as ((token: string) => Promise<Run[]>) | undefined,
    recordTransition:
      undefined as
        | ((token: string, runId: string, to: RunStatus) => Promise<Run>)
        | undefined,
  },
}))

vi.mock('@dnd-kit/core', () => ({
  DndContext: (props: {
    children?: ReactNode
    onDragStart?: (event: DragStartEvent) => void
    onDragEnd?: (event: DragEndEvent) => void
    onDragCancel?: () => void
  }) => {
    harness.onDragStart = props.onDragStart
    harness.onDragEnd = props.onDragEnd
    harness.onDragCancel = props.onDragCancel
    return <div data-testid="dnd-context">{props.children}</div>
  },
  useDraggable: () => ({
    attributes: {},
    listeners: {},
    setNodeRef: () => undefined,
    transform: null,
    isDragging: false,
  }),
  useDroppable: ({ id }: { id: string }) => ({
    setNodeRef: () => undefined,
    isOver: harness.overId === id,
  }),
  PointerSensor: class PointerSensor {},
  useSensor: () => ({}),
  useSensors: (...sensors: unknown[]) => sensors,
}))

vi.mock('@/lib/tasks', async (importOriginal) => {
  const original = await importOriginal<typeof import('@/lib/tasks')>()
  harness.original.listRuns = original.listRuns
  harness.original.recordTransition = original.recordTransition
  return {
    ...original,
    listRuns: vi.fn(original.listRuns),
    recordTransition: vi.fn(original.recordTransition),
  }
})

function renderBoard() {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  })

  return render(
    <QueryClientProvider client={queryClient}>
      <AuthContext.Provider
        value={{
          token: 'test-token',
          user: { id: '1', email: 'admin@example.com', role: 'admin' },
          login: vi.fn(),
          logout: vi.fn(),
        }}
      >
        <KanbanBoard />
      </AuthContext.Provider>
    </QueryClientProvider>,
  )
}

function dragStart(id: string) {
  act(() => {
    harness.onDragStart?.({ active: { id } } as unknown as DragStartEvent)
  })
}

function drop(id: string, over: string | null) {
  act(() => {
    harness.onDragEnd?.({
      active: { id },
      over: over === null ? null : { id: over },
    } as unknown as DragEndEvent)
  })
}

beforeEach(() => {
  harness.onDragStart = undefined
  harness.onDragEnd = undefined
  harness.onDragCancel = undefined
  harness.overId = null
  vi.mocked(listRuns).mockImplementation(harness.original.listRuns!)
  vi.mocked(recordTransition).mockImplementation(harness.original.recordTransition!)
})

afterEach(() => {
  vi.clearAllMocks()
})

describe('KanbanBoard', () => {
  it('places a run in the column matching its status (AC1)', async () => {
    renderBoard()

    const inDevColumn = await screen.findByTestId('column-in_dev')
    expect(within(inDevColumn).getByText('SDLC kanban board')).toBeInTheDocument()

    const codeReviewColumn = screen.getByTestId('column-code_review')
    expect(within(codeReviewColumn).getByText('Run state machine on Temporal')).toBeInTheDocument()
    expect(within(codeReviewColumn).queryByText('SDLC kanban board')).not.toBeInTheDocument()
  })

  it('renders all 15 columns with counts and an empty state', async () => {
    vi.mocked(listRuns).mockResolvedValue([{ ...FIXTURE_RUNS[0] }])
    renderBoard()

    await screen.findByTestId('column-idea')

    expect(screen.getAllByTestId(/^column-/)).toHaveLength(15)
    expect(screen.getByTestId('count-in_dev')).toHaveTextContent('1')
    expect(screen.getByTestId('count-idea')).toHaveTextContent('0')
    expect(within(screen.getByTestId('column-idea')).getByText('No runs')).toBeInTheDocument()
  })

  it('shows a skeleton while the board is loading', async () => {
    vi.mocked(listRuns).mockImplementation(() => new Promise(() => undefined))
    renderBoard()

    expect(await screen.findByTestId('board-loading')).toBeInTheDocument()
  })

  it('shows an error banner when the board fails to load', async () => {
    vi.mocked(listRuns).mockRejectedValue(new Error('HTTP 500'))
    renderBoard()

    expect(await screen.findByText('Failed to load the board')).toBeInTheDocument()
  })

  it('calls the transition seam and moves the card on a documented drop (AC2)', async () => {
    const inDevRun = { ...FIXTURE_RUNS[0] }
    const movedRun: Run = {
      ...inDevRun,
      status: 'code_review',
      updated_at: '2026-11-25T00:00:00Z',
    }
    vi.mocked(listRuns).mockResolvedValue([inDevRun])
    vi.mocked(recordTransition).mockResolvedValue(movedRun)
    renderBoard()

    await screen.findByTestId('column-in_dev')
    dragStart(inDevRun.id)
    drop(inDevRun.id, 'code_review')

    await waitFor(() =>
      expect(recordTransition).toHaveBeenCalledWith('test-token', inDevRun.id, 'code_review'),
    )
    await waitFor(() =>
      expect(
        within(screen.getByTestId('column-code_review')).getByText(inDevRun.name),
      ).toBeInTheDocument(),
    )
    expect(
      within(screen.getByTestId('column-in_dev')).queryByText(inDevRun.name),
    ).not.toBeInTheDocument()
    expect(screen.queryByText(/Transition rejected/)).not.toBeInTheDocument()
  })

  it('disables columns that are not a legal drop target for the dragged run', async () => {
    vi.mocked(listRuns).mockResolvedValue([{ ...FIXTURE_RUNS[0] }])
    renderBoard()

    await screen.findByTestId('column-in_dev')
    expect(screen.getByTestId('column-qa')).toHaveAttribute('aria-disabled', 'false')

    dragStart(FIXTURE_RUNS[0].id)

    expect(screen.getByTestId('column-code_review')).toHaveAttribute('aria-disabled', 'false')
    expect(screen.getByTestId('column-in_dev')).toHaveAttribute('aria-disabled', 'false')
    expect(screen.getByTestId('column-qa')).toHaveAttribute('aria-disabled', 'true')
    expect(screen.getByTestId('column-idea')).toHaveAttribute('aria-disabled', 'true')
  })

  it('enables the newly documented idea -> research transition', async () => {
    renderBoard()

    await screen.findByTestId('column-idea')
    dragStart('run-0006')

    expect(screen.getByTestId('column-research')).toHaveAttribute('aria-disabled', 'false')
    expect(screen.getByTestId('column-requirements')).toHaveAttribute('aria-disabled', 'true')
  })

  it('enables the deploy -> monitoring transition from the contract', async () => {
    const deployRun: Run = {
      ...FIXTURE_RUNS[0],
      id: 'run-deploy',
      status: 'deploy',
    }
    vi.mocked(listRuns).mockResolvedValue([deployRun])
    renderBoard()

    await screen.findByTestId('column-deploy')
    dragStart(deployRun.id)

    expect(screen.getByTestId('column-monitoring')).toHaveAttribute('aria-disabled', 'false')
    expect(screen.getByTestId('column-release')).toHaveAttribute('aria-disabled', 'true')
  })

  it('rejects an undocumented drop and keeps the card in place', async () => {
    // Uses the real fixture client: run-0001 is in_dev and in_dev -> qa is not
    // one of the 15 documented transitions.
    renderBoard()

    const inDevColumn = await screen.findByTestId('column-in_dev')
    expect(within(inDevColumn).getByText('SDLC kanban board')).toBeInTheDocument()

    dragStart(FIXTURE_RUNS[0].id)
    drop(FIXTURE_RUNS[0].id, 'qa')

    expect(await screen.findByText(/Transition rejected/)).toBeInTheDocument()
    expect(within(inDevColumn).getByText('SDLC kanban board')).toBeInTheDocument()
    expect(
      within(screen.getByTestId('column-qa')).queryByText('SDLC kanban board'),
    ).not.toBeInTheDocument()
  })

  it('highlights the hovered column and re-enables columns when the drag is cancelled', async () => {
    vi.mocked(listRuns).mockResolvedValue([{ ...FIXTURE_RUNS[0] }])
    harness.overId = 'code_review'
    renderBoard()

    await screen.findByTestId('column-in_dev')

    dragStart(FIXTURE_RUNS[0].id)
    expect(screen.getByTestId('column-code_review')).toHaveClass('ring-2')
    expect(screen.getByTestId('column-qa')).toHaveAttribute('aria-disabled', 'true')

    act(() => {
      harness.onDragCancel?.()
    })

    expect(screen.getByTestId('column-qa')).toHaveAttribute('aria-disabled', 'false')
    expect(recordTransition).not.toHaveBeenCalled()
  })

  it('ignores drops outside a column, on the source column or for unknown runs', async () => {
    vi.mocked(listRuns).mockResolvedValue([{ ...FIXTURE_RUNS[0] }])
    renderBoard()

    await screen.findByTestId('column-in_dev')

    dragStart(FIXTURE_RUNS[0].id)
    drop(FIXTURE_RUNS[0].id, null)
    drop(FIXTURE_RUNS[0].id, 'in_dev')
    drop('unknown-run', 'code_review')

    expect(recordTransition).not.toHaveBeenCalled()
  })
})
