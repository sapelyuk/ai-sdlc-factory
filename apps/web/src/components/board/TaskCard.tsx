import { useDraggable } from '@dnd-kit/core'
import { stateLabel } from '@/lib/sdlc'
import type { Run } from '@/lib/tasks'
import { cn } from '@/lib/utils'

function formatUpdatedAt(value: string): string {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) {
    return value
  }
  return `${date.toISOString().replace('T', ' ').slice(0, 16)} UTC`
}

export function TaskCard({ run }: { run: Run }) {
  const { attributes, listeners, setNodeRef, transform, isDragging } = useDraggable({
    id: run.id,
  })

  return (
    <div
      ref={setNodeRef}
      data-testid={`task-${run.id}`}
      className={cn(
        'cursor-grab rounded-lg border bg-background p-2 text-sm shadow-sm',
        isDragging && 'opacity-50',
      )}
      style={
        transform
          ? { transform: `translate3d(${transform.x}px, ${transform.y}px, 0)` }
          : undefined
      }
      {...attributes}
      {...listeners}
    >
      <p className="font-medium leading-snug">{run.name}</p>
      <div className="mt-2 flex items-center justify-between gap-2">
        <span className="rounded-full bg-muted px-2 py-0.5 text-xs text-muted-foreground">
          {stateLabel(run.status)}
        </span>
        <time dateTime={run.updated_at} className="text-xs text-muted-foreground">
          {formatUpdatedAt(run.updated_at)}
        </time>
      </div>
    </div>
  )
}
