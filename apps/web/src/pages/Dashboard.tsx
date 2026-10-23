import { useAuth } from '@/lib/auth-context'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Link } from 'react-router-dom'

export function Dashboard() {
  const { user, logout } = useAuth()

  return (
    <div className="min-h-screen bg-muted/40 p-4">
      <div className="mx-auto max-w-4xl space-y-6">
        <div className="flex items-center justify-between">
          <h1 className="text-2xl font-bold">ai-sdlc-factory</h1>
          <Button variant="outline" onClick={logout}>
            Sign Out
          </Button>
        </div>

        <Card>
          <CardHeader>
            <CardTitle>Welcome</CardTitle>
            <CardDescription>{user?.email} ({user?.role})</CardDescription>
          </CardHeader>
          <CardContent className="space-y-3">
            <p className="text-muted-foreground">
              This is the Web UI scaffold for the ai-sdlc-factory platform.
            </p>
            <div className="flex gap-2 pt-2">
              <Link to="/registry/roles">
                <Button>Roles</Button>
              </Link>
              <Link to="/registry/teams">
                <Button variant="outline">Teams</Button>
              </Link>
              <Link to="/registry/processes">
                <Button variant="outline">Processes</Button>
              </Link>
            </div>
          </CardContent>
        </Card>
      </div>
    </div>
  )
}
