import { Link } from 'react-router-dom'
import { KanbanBoard } from '@/components/board/KanbanBoard'
import { Button } from '@/components/ui/button'

export function Board() {
  return (
    <div className="min-h-screen bg-muted/40 p-4">
      <div className="mx-auto max-w-7xl space-y-6">
        <div className="flex items-center justify-between">
          <h1 className="text-2xl font-bold">SDLC Board</h1>
          <Link to="/">
            <Button variant="outline" size="sm">
              Dashboard
            </Button>
          </Link>
        </div>
        <KanbanBoard />
      </div>
    </div>
  )
}
